package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"k8s_ingestor/internal/clickhouse"
	"k8s_ingestor/internal/config"
	"k8s_ingestor/internal/masker"
	"k8s_ingestor/internal/tracing"
)

const (
	ErrCodeInvalidJSON        = "INVALID_JSON"
	ErrCodeEmptyBody          = "EMPTY_BODY"
	ErrCodeRateLimited        = "RATE_LIMITED"
	ErrCodeUnauthorized       = "UNAUTHORIZED"
	ErrCodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
)

type Handler struct {
	cfg    *config.Config
	masker *masker.Masker
	tracer *tracing.Tracer
}

var dlqChan chan FailedLog
var queueStatsFunc func() (int, int)
var logQueue chan ProcessedLog
var chClient *clickhouse.Client

var (
	jsonBufferPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Buffer{}
		},
	}
)

func init() {
	dlqChan = make(chan FailedLog, 10000)
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

type FailedLog struct {
	ID        string       `json:"id"`
	Log       FluentBitLog `json:"original_log"`
	Error     string       `json:"error"`
	Timestamp time.Time    `json:"timestamp"`
	Retries   int          `json:"retries"`
}

type Response struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"request_id"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type LogsResponse struct {
	Accepted int      `json:"accepted"`
	Rejected int      `json:"rejected"`
	LogIDs   []string `json:"log_ids"`
	Queued   int      `json:"queued"`
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
	requestID := h.getOrGenerateRequestID(r)
	ctx, span := h.tracer.StartSpan(r.Context(), "handle_logs")
	defer span.End()

	span.SetAttributes(attribute.String("request_id", requestID))

	start := time.Now()

	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "Method not allowed", "", requestID)
		return
	}

	var body []byte
	var err error

	if r.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(r.Body)
		if err != nil {
			h.sendError(w, http.StatusBadRequest, ErrCodeInvalidJSON, "Invalid gzip", err.Error(), requestID)
			return
		}
		defer gr.Close()
		body, err = io.ReadAll(gr)
	} else {
		body, err = io.ReadAll(r.Body)
	}

	if err != nil {
		h.sendError(w, http.StatusBadRequest, ErrCodeInternalError, "Failed to read request body", err.Error(), requestID)
		return
	}

	if len(body) == 0 {
		h.sendError(w, http.StatusBadRequest, ErrCodeEmptyBody, "Empty request body", "", requestID)
		return
	}

	var logs []FluentBitLog
	if err := json.Unmarshal(body, &logs); err != nil {
		var single FluentBitLog
		if err := json.Unmarshal(body, &single); err != nil {
			h.sendError(w, http.StatusBadRequest, ErrCodeInvalidJSON, "Invalid JSON format", err.Error(), requestID)
			return
		}
		logs = []FluentBitLog{single}
	}

	entries := make([]ProcessedLog, 0, len(logs))
	logIDs := make([]string, 0, len(logs))
	rejected := 0

	for _, l := range logs {
		if entry := h.processLog(ctx, l); entry != nil {
			entry.ID = uuid.New().String()
			entries = append(entries, *entry)
			logIDs = append(logIDs, entry.ID)
		} else {
			rejected++
		}
	}

	// Insert directly to ClickHouse
	if chClient != nil && len(entries) > 0 {
		chEntries := make([]clickhouse.LogEntry, 0, len(entries))
		for _, e := range entries {
			chEntries = append(chEntries, clickhouse.LogEntry{
				Timestamp:   e.Timestamp,
				Cluster:     e.Cluster,
				Namespace:   e.Namespace,
				Pod:         e.Pod,
				Container:   e.Container,
				Level:       e.Level,
				Message:     e.Message,
				Labels:      e.Labels,
				Annotations: e.Annotations,
				NodeName:    e.NodeName,
				HostIP:      e.HostIP,
				PodIP:       e.PodIP,
				TraceID:     e.TraceID,
				SpanID:      e.SpanID,
				ProcessedAt: e.ProcessedAt,
				IngestorID:  e.IngestorID,
			})
		}
		if err := chClient.InsertBatch(ctx, chEntries); err != nil {
			slog.Error("failed to insert batch", "error", err)
		} else {
			slog.Info("inserted directly", "count", len(chEntries))
		}
	}

	span.SetAttributes(
		attribute.Int("logs_accepted", len(entries)),
		attribute.Int("logs_rejected", rejected),
		attribute.String("request_id", requestID),
	)

	slog.Info("processed logs",
		"request_id", requestID,
		"accepted", len(entries),
		"rejected", rejected,
		"duration_ms", time.Since(start).Milliseconds(),
		"queue_len", len(logQueue),
	)

	h.sendSuccess(w, http.StatusOK, requestID, LogsResponse{
		Accepted: len(entries),
		Rejected: rejected,
		LogIDs:   logIDs,
		Queued:   len(entries),
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

func (h *Handler) HandleDLQ(w http.ResponseWriter, r *http.Request) {
	requestID := h.getOrGenerateRequestID(r)

	switch r.Method {
	case http.MethodGet:
		h.getDLQStats(w, requestID)
	case http.MethodPost:
		h.retryDLQ(w, r, requestID)
	case http.MethodDelete:
		h.flushDLQ(w, requestID)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "Method not allowed", "", requestID)
	}
}

func (h *Handler) getDLQStats(w http.ResponseWriter, requestID string) {
	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"dlq_size":     len(dlqChan),
		"dlq_capacity": cap(dlqChan),
	})
}

func (h *Handler) retryDLQ(w http.ResponseWriter, r *http.Request, requestID string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendError(w, http.StatusBadRequest, ErrCodeInternalError, "Failed to read body", err.Error(), requestID)
		return
	}

	var logIDs []string
	if err := json.Unmarshal(body, &logIDs); err != nil {
		h.sendError(w, http.StatusBadRequest, ErrCodeInvalidJSON, "Invalid JSON", err.Error(), requestID)
		return
	}

	retried := 0
	for range logIDs {
		select {
		case failed := <-dlqChan:
			dlqChan <- failed
			retried++
		default:
			break
		}
	}

	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"retried": retried,
	})
}

func (h *Handler) flushDLQ(w http.ResponseWriter, requestID string) {
	flushed := 0
	for {
		select {
		case <-dlqChan:
			flushed++
		default:
			goto done
		}
	}
done:

	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"flushed": flushed,
	})
}

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	requestID := h.getOrGenerateRequestID(r)

	resp := map[string]interface{}{
		"status":      "healthy",
		"ingestor_id": h.cfg.IngestorID,
		"request_id":  requestID,
		"timestamp":   time.Now().Format(time.RFC3339),
		"queue_size":  len(logQueue),
	}

	h.sendSuccess(w, http.StatusOK, requestID, resp)
}

func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	http.DefaultServeMux.ServeHTTP(w, r)
}

func (h *Handler) getOrGenerateRequestID(r *http.Request) string {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	return requestID
}

func (h *Handler) sendSuccess(w http.ResponseWriter, status int, requestID string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)

	resp := Response{
		Success:   true,
		RequestID: requestID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data:      data,
	}

	buf := jsonBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufferPool.Put(buf)

	enc := json.NewEncoder(buf)
	enc.Encode(resp)
	w.Write(buf.Bytes())
}

func (h *Handler) sendError(w http.ResponseWriter, status int, code, message, detail, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)

	resp := Response{
		Success:   false,
		RequestID: requestID,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
			Detail:  detail,
		},
	}

	buf := jsonBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufferPool.Put(buf)

	enc := json.NewEncoder(buf)
	enc.Encode(resp)
	w.Write(buf.Bytes())
}

func SetLogQueue(q chan ProcessedLog) {
	logQueue = q
}

func SetClickHouseClient(c *clickhouse.Client) {
	chClient = c
}

type ProcessedLog struct {
	ID          string
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

func AddToDLQ(failed FailedLog) {
	select {
	case dlqChan <- failed:
	default:
		slog.Warn("DLQ full, dropping failed log", "id", failed.ID)
	}
}

func GetQueueStats() (int, int) {
	return len(logQueue), cap(logQueue)
}
