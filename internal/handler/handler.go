package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"k8s_ingestor/internal/config"
	"k8s_ingestor/internal/masker"
	"k8s_ingestor/internal/tracing"
)

type Handler struct {
	cfg    *config.Config
	masker *masker.Masker
	tracer *tracing.Tracer
}

type FluentBitLog struct {
	Log        string `json:"log"`
	Timestamp  string `json:"time"`
	Kubernetes struct {
		Namespace   string            `json:"namespace_name"`
		Pod         string            `json:"pod_name"`
		Container   string            `json:"container_name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations,omitempty"`
		NodeName    string            `json:"host,omitempty"`
		HostIP      string            `json:"pod_ip,omitempty"`
		PodIP       string            `json:"container_id,omitempty"`
	} `json:"kubernetes"`
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
}

var logLevelRegex = regexp.MustCompile(`(?i)\b(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL|CRITICAL)\b`)

func New(cfg *config.Config, m *masker.Masker, t *tracing.Tracer) *Handler {
	return &Handler{
		cfg:    cfg,
		masker: m,
		tracer: t,
	}
}

func (h *Handler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.StartSpan(r.Context(), "handle_logs")
	defer span.End()

	start := time.Now()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if len(body) == 0 {
		http.Error(w, "Empty body", http.StatusBadRequest)
		return
	}

	var logs []FluentBitLog
	if err := json.Unmarshal(body, &logs); err != nil {
		var single FluentBitLog
		if err := json.Unmarshal(body, &single); err != nil {
			slog.Error("decode error", "error", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		logs = []FluentBitLog{single}
	}

	entries := make([]ProcessedLog, 0, len(logs))
	for _, l := range logs {
		if entry := h.processLog(ctx, l); entry != nil {
			entries = append(entries, *entry)
		}
	}

	span.SetAttributes(attribute.Int("logs_count", len(entries)))

	slog.Info("processed logs",
		"count", len(entries),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"count":  len(entries),
		"metrics": map[string]interface{}{
			"duration_ms": time.Since(start).Milliseconds(),
		},
	})
}

func (h *Handler) processLog(ctx context.Context, l FluentBitLog) *ProcessedLog {
	if l.Log == "" {
		return nil
	}

	msg := cleanMessage(l.Log)
	level := detectLevel(msg)

	ns := strings.TrimSpace(l.Kubernetes.Namespace)
	if ns == "" {
		ns = "unknown"
	}

	pod := strings.TrimSpace(l.Kubernetes.Pod)
	if pod == "" {
		pod = "unknown"
	}

	container := strings.TrimSpace(l.Kubernetes.Container)
	if container == "" {
		container = "unknown"
	}

	msg = h.masker.Mask(msg)
	ns = h.masker.Mask(ns)
	pod = h.masker.Mask(pod)

	ts := parseTimestamp(l.Timestamp)
	if ts.IsZero() {
		ts = time.Now()
	}

	entry := ProcessedLog{
		Timestamp:   ts,
		Cluster:     h.cfg.ClusterName,
		Namespace:   ns,
		Pod:         pod,
		Container:   container,
		Level:       level,
		Message:     msg,
		Labels:      l.Kubernetes.Labels,
		Annotations: l.Kubernetes.Annotations,
		NodeName:    l.Kubernetes.NodeName,
		HostIP:      l.Kubernetes.HostIP,
		PodIP:       l.Kubernetes.PodIP,
		TraceID:     l.TraceID,
		SpanID:      l.SpanID,
		ProcessedAt: time.Now(),
		IngestorID:  h.cfg.IngestorID,
	}

	return &entry
}

type ProcessedLog struct {
	Timestamp   time.Time
	Cluster     string
	Namespace   string
	Pod         string
	Container   string
	Level       string
	Message     string
	Labels      map[string]string
	Annotations map[string]string
	NodeName    string
	HostIP      string
	PodIP       string
	TraceID     string
	SpanID      string
	ProcessedAt time.Time
	IngestorID  string
}

func cleanMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	parts := strings.SplitN(msg, " ", 4)
	if len(parts) == 4 {
		return strings.TrimSpace(parts[3])
	}
	return msg
}

func detectLevel(msg string) string {
	matches := logLevelRegex.FindStringSubmatch(msg)
	if len(matches) > 1 {
		switch strings.ToUpper(matches[1]) {
		case "ERROR", "FATAL", "CRITICAL":
			return "error"
		case "WARN", "WARNING":
			return "warn"
		case "DEBUG", "TRACE":
			return "debug"
		}
	}

	msgLower := strings.ToLower(msg)
	if strings.Contains(msgLower, "error") {
		return "error"
	}
	if strings.Contains(msgLower, "warn") {
		return "warn"
	}
	if strings.Contains(msgLower, "debug") || strings.Contains(msgLower, "trace") {
		return "debug"
	}
	return "info"
}

func parseTimestamp(ts string) time.Time {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.DateTime,
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t
		}
	}

	return time.Time{}
}

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]interface{}{
		"status":      "healthy",
		"ingestor_id": h.cfg.IngestorID,
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	http.DefaultServeMux.ServeHTTP(w, r)
}
