package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s_ingestor/internal/config"
	"k8s_ingestor/internal/handler"
	"k8s_ingestor/internal/masker"
	"k8s_ingestor/internal/tracing"
)

func TestHandleLogs_ValidJSON(t *testing.T) {
	cfg := &config.Config{
		ClusterName: "test-cluster",
		IngestorID:  "test-ingestor",
	}
	m := masker.New(true)
	tracer, _ := tracing.NewTracer(tracing.TracerConfig{Enabled: false})
	h := handler.New(cfg, m, tracer)

	log := handler.FluentBitLog{
		Log:       "2024-01-01 12:00:00 ERROR something went wrong",
		Timestamp: "2024-01-01T12:00:00Z",
		Kubernetes: struct {
			Namespace   string            "json:\"namespace_name\""
			Pod         string            "json:\"pod_name\""
			Container   string            "json:\"container_name\""
			Labels      map[string]string "json:\"labels\""
			Annotations map[string]string "json:\"annotations,omitempty\""
			NodeName    string            "json:\"host,omitempty\""
			HostIP      string            "json:\"pod_ip,omitempty\""
			PodIP       string            "json:\"container_id,omitempty\""
		}{
			Namespace: "test-namespace",
			Pod:       "test-pod",
			Container: "test-container",
		},
	}

	body, _ := json.Marshal([]handler.FluentBitLog{log})

	req := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandleLogs_InvalidJSON(t *testing.T) {
	cfg := &config.Config{ClusterName: "test"}
	m := masker.New(false)
	tracer, _ := tracing.NewTracer(tracing.TracerConfig{Enabled: false})
	h := handler.New(cfg, m, tracer)

	req := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader([]byte("invalid json")))
	rr := httptest.NewRecorder()

	h.HandleLogs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleLogs_EmptyBody(t *testing.T) {
	cfg := &config.Config{ClusterName: "test"}
	m := masker.New(false)
	tracer, _ := tracing.NewTracer(tracing.TracerConfig{Enabled: false})
	h := handler.New(cfg, m, tracer)

	req := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader([]byte{}))
	rr := httptest.NewRecorder()

	h.HandleLogs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleLogs_MethodNotAllowed(t *testing.T) {
	cfg := &config.Config{ClusterName: "test"}
	m := masker.New(false)
	tracer, _ := tracing.NewTracer(tracing.TracerConfig{Enabled: false})
	h := handler.New(cfg, m, tracer)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rr := httptest.NewRecorder()

	h.HandleLogs(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	cfg := &config.Config{
		ClusterName: "test-cluster",
		IngestorID:  "test-123",
	}
	m := masker.New(false)
	tracer, _ := tracing.NewTracer(tracing.TracerConfig{Enabled: false})
	h := handler.New(cfg, m, tracer)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	h.HandleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got '%v'", resp["status"])
	}

	if resp["ingestor_id"] != "test-123" {
		t.Errorf("expected ingestor_id 'test-123', got '%v'", resp["ingestor_id"])
	}
}
