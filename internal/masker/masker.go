package masker

import (
	"regexp"
	"strings"
	"sync"
)

type Masker struct {
	patterns []pattern
	mu       sync.RWMutex
}

type pattern struct {
	name     string
	regex    *regexp.Regexp
	replacer string
}

var defaultPatterns = []pattern{
	{
		name:     "email",
		regex:    regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
		replacer: "[EMAIL]",
	},
	{
		name:     "ip",
		regex:    regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
		replacer: "[IP]",
	},
	{
		name:     "credit_card",
		regex:    regexp.MustCompile(`\b(?:\d{4}[- ]?){3}\d{4}\b`),
		replacer: "[CARD]",
	},
	{
		name:     "ssn",
		regex:    regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		replacer: "[SSN]",
	},
	{
		name:     "jwt",
		regex:    regexp.MustCompile(`eyJ[a-zA-Z0-9_-]+\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`),
		replacer: "[JWT]",
	},
	{
		name:     "aws_key",
		regex:    regexp.MustCompile(`\b(?:AKIA|A3T|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[A-Z0-9]{16}\b`),
		replacer: "[AWS_KEY]",
	},
	{
		name:     "password",
		regex:    regexp.MustCompile(`(?i)(?:password|passwd|pwd)["\s:=]+\S+`),
		replacer: "[PASSWORD]",
	},
	{
		name:     "api_key",
		regex:    regexp.MustCompile(`(?i)(?:api[_-]?key|apikey)["\s:=]+\S+`),
		replacer: "[API_KEY]",
	},
	{
		name:     "token",
		regex:    regexp.MustCompile(`(?i)(?:token|bearer)["\s:=]+\S+`),
		replacer: "[TOKEN]",
	},
	{
		name:     "authorization",
		regex:    regexp.MustCompile(`(?i)authorization["\s:=]+[Bb]earer\s+\S+`),
		replacer: "[AUTH]",
	},
	{
		name:     "private_key",
		regex:    regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
		replacer: "[PRIVATE_KEY]",
	},
}

func New(enabled bool) *Masker {
	m := &Masker{
		patterns: make([]pattern, 0),
	}

	if enabled {
		m.patterns = append(m.patterns, defaultPatterns...)
	}

	return m
}

func (m *Masker) AddPattern(name, regex, replacer string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	compiled, err := regexp.Compile(regex)
	if err != nil {
		return
	}

	m.patterns = append(m.patterns, pattern{
		name:     name,
		regex:    compiled,
		replacer: replacer,
	})
}

func (m *Masker) Mask(s string) string {
	if s == "" {
		return s
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := s
	for _, p := range m.patterns {
		result = p.regex.ReplaceAllString(result, p.replacer)
	}

	return result
}

func (m *Masker) MaskLog(log, namespace, pod string) (string, string, string) {
	return m.Mask(log), m.Mask(namespace), m.Mask(pod)
}

type StructuredMasker struct {
	masker    *Masker
	sensitive []string
	mu        sync.RWMutex
}

func NewStructured(enabled bool, sensitiveFields []string) *StructuredMasker {
	return &StructuredMasker{
		masker:    New(enabled),
		sensitive: sensitiveFields,
	}
}

func (sm *StructuredMasker) AddSensitiveField(field string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sensitive = append(sm.sensitive, field)
}

type LogEntry struct {
	Message     string            `json:"message"`
	Timestamp   string            `json:"timestamp"`
	Namespace   string            `json:"namespace"`
	Pod         string            `json:"pod"`
	Container   string            `json:"container"`
	Level       string            `json:"level"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	NodeName    string            `json:"node_name"`
	HostIP      string            `json:"host_ip"`
	PodIP       string            `json:"pod_ip"`
	TraceID     string            `json:"trace_id"`
	SpanID      string            `json:"span_id"`
}

func (sm *StructuredMasker) MaskEntry(entry *LogEntry) *LogEntry {
	if entry == nil {
		return nil
	}

	sm.mu.RLock()
	sensitive := make([]string, len(sm.sensitive))
	copy(sensitive, sm.sensitive)
	sm.mu.RUnlock()

	result := &LogEntry{
		Message:   sm.masker.Mask(entry.Message),
		Timestamp: entry.Timestamp,
		Namespace: sm.masker.Mask(entry.Namespace),
		Pod:       sm.masker.Mask(entry.Pod),
		Container: entry.Container,
		Level:     entry.Level,
		NodeName:  entry.NodeName,
		HostIP:    sm.masker.Mask(entry.HostIP),
		PodIP:     sm.masker.Mask(entry.PodIP),
		TraceID:   entry.TraceID,
		SpanID:    entry.SpanID,
	}

	if entry.Labels != nil {
		result.Labels = make(map[string]string)
		for k, v := range entry.Labels {
			result.Labels[k] = sm.masker.Mask(v)
		}
	}

	if entry.Annotations != nil {
		result.Annotations = make(map[string]string)
		for k, v := range entry.Annotations {
			result.Annotations[k] = sm.masker.Mask(v)
		}
	}

	return result
}

func MaskHeaders(headers map[string][]string) map[string][]string {
	masked := make(map[string][]string)
	sensitiveHeaders := []string{
		"authorization",
		"x-api-key",
		"cookie",
		"x-auth-token",
	}

	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		isSensitive := false
		for _, sh := range sensitiveHeaders {
			if strings.Contains(lowerKey, sh) {
				isSensitive = true
				break
			}
		}

		if isSensitive {
			masked[key] = []string{"[MASKED]"}
		} else {
			masked[key] = values
		}
	}

	return masked
}
