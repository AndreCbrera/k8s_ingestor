package pipeline

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PipelineConfig struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
	Namespaces  []NamespaceRule `json:"namespaces"`
	Pods        []PodRule       `json:"pods"`
	Labels      []LabelRule     `json:"labels"`
	Levels      []string        `json:"levels"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type NamespaceRule struct {
	Name    string `json:"name"`
	Include bool   `json:"include"`
}

type PodRule struct {
	Pattern   string `json:"pattern"`
	Namespace string `json:"namespace"`
	Include   bool   `json:"include"`
}

type LabelRule struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Include bool   `json:"include"`
}

type Handler struct {
	pipelines map[string]*PipelineConfig
	mu        sync.RWMutex
	storePath string
}

type APIResponse struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewHandler(storePath string) *Handler {
	h := &Handler{
		pipelines: make(map[string]*PipelineConfig),
		storePath: storePath,
	}
	h.load()
	return h
}

func (h *Handler) load() {
	if h.storePath == "" {
		return
	}

	data, err := os.ReadFile(h.storePath)
	if err != nil {
		return
	}

	var pipelines []*PipelineConfig
	if err := json.Unmarshal(data, &pipelines); err != nil {
		slog.Warn("failed to load pipelines", "error", err)
		return
	}

	for _, p := range pipelines {
		h.pipelines[p.ID] = p
	}
}

func (h *Handler) save() {
	if h.storePath == "" {
		return
	}

	h.mu.RLock()
	pipelines := make([]*PipelineConfig, 0, len(h.pipelines))
	for _, p := range h.pipelines {
		pipelines = append(pipelines, p)
	}
	h.mu.RUnlock()

	data, err := json.MarshalIndent(pipelines, "", "  ")
	if err != nil {
		slog.Error("failed to marshal pipelines", "error", err)
		return
	}

	if err := os.WriteFile(h.storePath, data, 0644); err != nil {
		slog.Error("failed to save pipelines", "error", err)
	}
}

func (h *Handler) GenerateFluentBitConfig(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var namespaces []string
	for _, p := range h.pipelines {
		if !p.Enabled {
			continue
		}
		for _, ns := range p.Namespaces {
			if ns.Include {
				namespaces = append(namespaces, ns.Name)
			}
		}
	}

	config := GenerateLuaFilter(namespaces)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(config))
}

func GenerateLuaFilter(namespaces []string) string {
	lua := `function append_if_not_exists(arr, val)
    for _, v in ipairs(arr) do
        if v == val then
            return false
        end
    end
    table.insert(arr, val)
    return true
end

function filter_by_namespace(tag, timestamp, record)
    -- Namespaces to INCLUDE (drop all others)
    local include_namespaces = {`

	for i, ns := range namespaces {
		if i > 0 {
			lua += "\n        "
		}
		lua += fmt.Sprintf(`"%s"`, ns)
		if i < len(namespaces)-1 {
			lua += ","
		}
	}

	lua += `
    }

    -- Namespaces to ALWAYS exclude
    local exclude_namespaces = {
        "kube-system",
        "clickhouse-operator",
        "logging",
        "monitoring"
    }

    local ns = record["kubernetes"]["namespace_name"]
    
    if ns == nil then
        return -1, 0, 0
    end
    
    -- Check if in exclude list
    for _, exclude in ipairs(exclude_namespaces) do
        if ns == exclude then
            return -1, 0, 0
        end
    end
    
    -- Check if in include list (if not empty)
    if #include_namespaces > 0 then
        local found = false
        for _, include in ipairs(include_namespaces) do
            if ns == include then
                found = true
                break
            end
        end
        if not found then
            return -1, 0, 0
        end
    end

    return 1, 0, record
end
`
	return lua
}

func (h *Handler) ListPipelines(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	pipelines := make([]*PipelineConfig, 0, len(h.pipelines))
	for _, p := range h.pipelines {
		pipelines = append(pipelines, p)
	}

	h.sendSuccess(w, http.StatusOK, uuid.New().String(), map[string]interface{}{
		"pipelines": pipelines,
		"count":     len(pipelines),
	})
}

func (h *Handler) GetPipeline(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		h.sendError(w, http.StatusBadRequest, "MISSING_ID", "Pipeline ID required", uuid.New().String())
		return
	}

	h.mu.RLock()
	p, ok := h.pipelines[id]
	h.mu.RUnlock()

	if !ok {
		h.sendError(w, http.StatusNotFound, "NOT_FOUND", "Pipeline not found", uuid.New().String())
		return
	}

	h.sendSuccess(w, http.StatusOK, uuid.New().String(), p)
}

func (h *Handler) CreatePipeline(w http.ResponseWriter, r *http.Request) {
	var pipeline PipelineConfig
	if err := json.NewDecoder(r.Body).Decode(&pipeline); err != nil {
		h.sendError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), uuid.New().String())
		return
	}

	pipeline.ID = uuid.New().String()
	pipeline.CreatedAt = time.Now()
	pipeline.UpdatedAt = time.Now()

	if pipeline.Name == "" {
		pipeline.Name = "Pipeline " + pipeline.ID[:8]
	}

	h.mu.Lock()
	h.pipelines[pipeline.ID] = &pipeline
	h.mu.Unlock()

	h.save()

	slog.Info("pipeline created", "id", pipeline.ID, "name", pipeline.Name)

	h.sendSuccess(w, http.StatusCreated, uuid.New().String(), pipeline)
}

func (h *Handler) UpdatePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		h.sendError(w, http.StatusBadRequest, "MISSING_ID", "Pipeline ID required", uuid.New().String())
		return
	}

	var updates struct {
		Name        *string         `json:"name,omitempty"`
		Description *string         `json:"description,omitempty"`
		Enabled     *bool           `json:"enabled,omitempty"`
		Namespaces  []NamespaceRule `json:"namespaces,omitempty"`
		Pods        []PodRule       `json:"pods,omitempty"`
		Labels      []LabelRule     `json:"labels,omitempty"`
		Levels      []string        `json:"levels,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		h.sendError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), uuid.New().String())
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	p, ok := h.pipelines[id]
	if !ok {
		h.sendError(w, http.StatusNotFound, "NOT_FOUND", "Pipeline not found", uuid.New().String())
		return
	}

	if updates.Name != nil {
		p.Name = *updates.Name
	}
	if updates.Description != nil {
		p.Description = *updates.Description
	}
	if updates.Enabled != nil {
		p.Enabled = *updates.Enabled
	}
	if updates.Namespaces != nil {
		p.Namespaces = updates.Namespaces
	}
	if updates.Pods != nil {
		p.Pods = updates.Pods
	}
	if updates.Labels != nil {
		p.Labels = updates.Labels
	}
	if updates.Levels != nil {
		p.Levels = updates.Levels
	}
	p.UpdatedAt = time.Now()

	h.save()

	slog.Info("pipeline updated", "id", id)

	h.sendSuccess(w, http.StatusOK, uuid.New().String(), p)
}

func (h *Handler) DeletePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		h.sendError(w, http.StatusBadRequest, "MISSING_ID", "Pipeline ID required", uuid.New().String())
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.pipelines[id]; !ok {
		h.sendError(w, http.StatusNotFound, "NOT_FOUND", "Pipeline not found", uuid.New().String())
		return
	}

	delete(h.pipelines, id)
	h.save()

	slog.Info("pipeline deleted", "id", id)

	h.sendSuccess(w, http.StatusOK, uuid.New().String(), map[string]string{
		"message": "Pipeline deleted",
	})
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.sendSuccess(w, http.StatusOK, uuid.New().String(), map[string]interface{}{
		"status":    "healthy",
		"service":   "pipeline-service",
		"pipelines": len(h.pipelines),
	})
}

func (h *Handler) sendSuccess(w http.ResponseWriter, status int, requestID string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)

	resp := APIResponse{
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

	resp := APIResponse{
		Success:   false,
		RequestID: requestID,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}
