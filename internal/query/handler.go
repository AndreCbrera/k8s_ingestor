package query

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"k8s_ingestor/internal/clickhouse"
)

type Handler struct {
	ch *clickhouse.Client
}

type LogEntry struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Cluster     string            `json:"cluster"`
	Namespace   string            `json:"namespace"`
	Pod         string            `json:"pod"`
	Container   string            `json:"container"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	NodeName    string            `json:"node_name,omitempty"`
	HostIP      string            `json:"host_ip,omitempty"`
}

type QueryParams struct {
	Namespace  string
	Pod        string
	Container  string
	Level      string
	Cluster    string
	Message    string
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
	OrderBy    string
	Descending bool
}

type QueryResponse struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
}

type LogsData struct {
	Logs    []LogEntry `json:"logs"`
	Total   int64      `json:"total"`
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	HasMore bool       `json:"has_more"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(ch *clickhouse.Client) *Handler {
	return &Handler{ch: ch}
}

func (h *Handler) parseQueryParams(r *http.Request) QueryParams {
	q := QueryParams{
		Limit:      100,
		Offset:     0,
		OrderBy:    "timestamp",
		Descending: true,
		StartTime:  time.Now().Add(-24 * time.Hour),
		EndTime:    time.Now(),
	}

	q.Namespace = r.URL.Query().Get("namespace")
	q.Pod = r.URL.Query().Get("pod")
	q.Container = r.URL.Query().Get("container")
	q.Level = r.URL.Query().Get("level")
	q.Cluster = r.URL.Query().Get("cluster")
	q.Message = r.URL.Query().Get("search")

	if limit := r.URL.Query().Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 && l <= 1000 {
			q.Limit = l
		}
	}

	if offset := r.URL.Query().Get("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			q.Offset = o
		}
	}

	if order := r.URL.Query().Get("order_by"); order != "" {
		q.OrderBy = order
	}

	if desc := r.URL.Query().Get("desc"); desc == "false" || desc == "0" {
		q.Descending = false
	}

	if start := r.URL.Query().Get("start"); start != "" {
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			q.StartTime = t
		}
	}

	if end := r.URL.Query().Get("end"); end != "" {
		if t, err := time.Parse(time.RFC3339, end); err == nil {
			q.EndTime = t
		}
	}

	return q
}

func (h *Handler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := r.Context()
	start := time.Now()

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", requestID)
		return
	}

	params := h.parseQueryParams(r)

	logs, total, err := h.ch.QueryLogs(ctx, clickhouse.QueryOptions{
		Namespace:  params.Namespace,
		Pod:        params.Pod,
		Container:  params.Container,
		Level:      params.Level,
		Cluster:    params.Cluster,
		Message:    params.Message,
		StartTime:  params.StartTime,
		EndTime:    params.EndTime,
		Limit:      params.Limit,
		Offset:     params.Offset,
		OrderBy:    params.OrderBy,
		Descending: params.Descending,
	})

	if err != nil {
		slog.Error("query failed", "error", err)
		h.sendError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), requestID)
		return
	}

	entries := make([]LogEntry, 0, len(logs))
	for _, l := range logs {
		entries = append(entries, LogEntry{
			Timestamp:   l.Timestamp,
			Cluster:     l.Cluster,
			Namespace:   l.Namespace,
			Pod:         l.Pod,
			Container:   l.Container,
			Level:       l.Level,
			Message:     l.Message,
			Labels:      l.Labels,
			Annotations: l.Annotations,
			NodeName:    l.NodeName,
			HostIP:      l.HostIP,
		})
	}

	h.sendSuccess(w, http.StatusOK, requestID, LogsData{
		Logs:    entries,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
		HasMore: int64(params.Offset+params.Limit) < total,
	})

	slog.Info("query executed",
		"request_id", requestID,
		"duration_ms", time.Since(start).Milliseconds(),
		"results", len(entries),
	)
}

func (h *Handler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := r.Context()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendError(w, http.StatusInternalServerError, "STREAMING_NOT_SUPPORTED", "Streaming not supported", requestID)
		return
	}

	params := h.parseQueryParams(r)
	params.Limit = 50

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	sendLogs := func() {
		logs, _, _ := h.ch.QueryLogs(ctx, clickhouse.QueryOptions{
			Namespace:  params.Namespace,
			Pod:        params.Pod,
			Container:  params.Container,
			Level:      params.Level,
			Cluster:    params.Cluster,
			Message:    params.Message,
			StartTime:  params.StartTime,
			EndTime:    time.Now(),
			Limit:      params.Limit,
			Offset:     0,
			OrderBy:    "timestamp",
			Descending: true,
		})

		for _, l := range logs {
			entry := LogEntry{
				Timestamp: l.Timestamp,
				Cluster:   l.Cluster,
				Namespace: l.Namespace,
				Pod:       l.Pod,
				Container: l.Container,
				Level:     l.Level,
				Message:   l.Message,
				NodeName:  l.NodeName,
			}
			data, _ := json.Marshal(map[string]interface{}{
				"success":    true,
				"request_id": requestID,
				"data":       entry,
			})
			fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	}

	sendLogs()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendLogs()
		}
	}
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := r.Context()

	stats, err := h.ch.GetStatsFull(ctx)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "STATS_FAILED", err.Error(), requestID)
		return
	}

	h.sendSuccess(w, http.StatusOK, requestID, stats)
}

func (h *Handler) GetNamespaces(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := r.Context()

	namespaces, err := h.ch.GetDistinctValues(ctx, "namespace", r.URL.Query().Get("prefix"))
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), requestID)
		return
	}

	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"namespaces": namespaces,
	})
}

func (h *Handler) GetPods(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := r.Context()

	namespace := r.URL.Query().Get("namespace")
	pods, err := h.ch.GetDistinctValues(ctx, "pod", r.URL.Query().Get("prefix"))
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), requestID)
		return
	}

	if namespace != "" {
		filtered := make([]string, 0)
		for _, p := range pods {
			if strings.HasPrefix(p, namespace+"-") || strings.Contains(p, namespace) {
				filtered = append(filtered, p)
			}
		}
		pods = filtered
	}

	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"pods": pods,
	})
}

func (h *Handler) GetLevels(w http.ResponseWriter, r *http.Request) {
	h.sendSuccess(w, http.StatusOK, uuid.New().String(), map[string]interface{}{
		"levels": []string{"error", "warn", "info", "debug"},
	})
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()

	if err := h.ch.Ping(r.Context()); err != nil {
		h.sendError(w, http.StatusServiceUnavailable, "CLICKHOUSE_DOWN", err.Error(), requestID)
		return
	}

	h.sendSuccess(w, http.StatusOK, requestID, map[string]interface{}{
		"status":  "healthy",
		"service": "query-service",
	})
}

func (h *Handler) sendSuccess(w http.ResponseWriter, status int, requestID string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)

	resp := QueryResponse{
		Success:   true,
		RequestID: requestID,
		Data:      data,
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) sendError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)

	resp := QueryResponse{
		Success:   false,
		RequestID: requestID,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}
