package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type LogEntry struct {
	Timestamp      string `json:"timestamp"`
	Level          string `json:"level"`
	Service        string `json:"service"`
	Namespace      string `json:"namespace"`
	Message        string `json:"message"`
	JobID          string `json:"job_id,omitempty"`
	JobType        string `json:"job_type,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
	ItemsProcessed int    `json:"items_processed,omitempty"`
	Queue          string `json:"queue,omitempty"`
	RetryCount     int    `json:"retry_count,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

func sendLog(entry LogEntry) {
	data, _ := json.Marshal(entry)
	fmt.Println(string(data))
}

func sendToIngestor(entry LogEntry) {
	ingestorURL := os.Getenv("INGESTOR_URL")
	if ingestorURL == "" {
		ingestorURL = "http://k8s-ingestor.logging.svc.cluster.local:8080/logs"
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	go func() {
		resp, err := http.Post(ingestorURL, "application/json", bytes.NewBuffer(data))
		if err == nil {
			resp.Body.Close()
		}
	}()
}

func generateJobID() string {
	return fmt.Sprintf("job_%d_%d", time.Now().Unix(), rand.Intn(10000))
}

func getNamespace() string {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		return "demo"
	}
	return ns
}

func processEmailJob() LogEntry {
	start := time.Now()
	jobID := generateJobID()

	namespace := getNamespace()

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "worker-service",
		Namespace: namespace,
		Message:   "Processing email job",
		JobID:     jobID,
		JobType:   "send_email",
		Queue:     "email_queue",
	}
	sendLog(logEntry)

	time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)

	// Simulate occasional failure
	if rand.Float32() < 0.1 {
		logEntry.Level = "error"
		logEntry.Message = "Email job failed: SMTP connection timeout"
		logEntry.ErrorMessage = "dial tcp smtp.example.com:587: connection refused"
		logEntry.RetryCount = 1
		sendLog(logEntry)
		sendToIngestor(logEntry)
	} else {
		logEntry.Message = "Email job completed successfully"
		logEntry.ItemsProcessed = rand.Intn(100) + 1
		logEntry.DurationMs = time.Since(start).Milliseconds()
		sendLog(logEntry)
		sendToIngestor(logEntry)
	}

	return logEntry
}

func processImageJob() LogEntry {
	start := time.Now()
	jobID := generateJobID()

	namespace := getNamespace()

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "worker-service",
		Namespace: namespace,
		Message:   "Processing image resize job",
		JobID:     jobID,
		JobType:   "image_resize",
		Queue:     "image_queue",
	}
	sendLog(logEntry)

	time.Sleep(time.Duration(200+rand.Intn(300)) * time.Millisecond)

	logEntry.Message = "Image resize completed"
	logEntry.ItemsProcessed = rand.Intn(5) + 1
	logEntry.DurationMs = time.Since(start).Milliseconds()
	sendLog(logEntry)
	sendToIngestor(logEntry)

	return logEntry
}

func processDataSyncJob() LogEntry {
	start := time.Now()
	jobID := generateJobID()

	namespace := getNamespace()

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "worker-service",
		Namespace: namespace,
		Message:   "Starting data sync",
		JobID:     jobID,
		JobType:   "data_sync",
		Queue:     "sync_queue",
	}
	sendLog(logEntry)

	time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)

	logEntry.Message = "Data sync completed"
	logEntry.ItemsProcessed = rand.Intn(1000) + 100
	logEntry.DurationMs = time.Since(start).Milliseconds()
	sendLog(logEntry)
	sendToIngestor(logEntry)

	return logEntry
}

func processReportJob() LogEntry {
	start := time.Now()
	jobID := generateJobID()

	namespace := getNamespace()

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "worker-service",
		Namespace: namespace,
		Message:   "Generating analytics report",
		JobID:     jobID,
		JobType:   "generate_report",
		Queue:     "report_queue",
	}
	sendLog(logEntry)

	time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond)

	logEntry.Message = "Report generated"
	logEntry.ItemsProcessed = rand.Intn(50) + 10
	logEntry.DurationMs = time.Since(start).Milliseconds()
	sendLog(logEntry)
	sendToIngestor(logEntry)

	return logEntry
}

func logWorkerStatus() {
	namespace := getNamespace()

	messages := []string{
		"Worker pool: 5/10 workers active",
		"Queue depths: email=12, image=45, sync=3, report=7",
		"Memory usage: 256MB / 512MB limit",
		"CPU usage: 35%",
		"Jobs processed this minute: 47",
		"Dead letter queue: 0 items",
		"Worker heartbeat: alive",
	}

	levels := []string{"info", "info", "info", "debug", "info", "debug", "info"}

	idx := rand.Intn(len(messages))
	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     levels[idx],
		Service:   "worker-service",
		Namespace: namespace,
		Message:   messages[idx],
	}
	sendLog(logEntry)
	sendToIngestor(logEntry)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	log.Printf("Worker Service starting...")

	// Log initial status
	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "worker-service",
		Namespace: getNamespace(),
		Message:   "Worker service started",
	}
	sendLog(logEntry)
	sendToIngestor(logEntry)

	// Main processing loop
	ticker := time.NewTicker(3 * time.Second)
	statusTicker := time.NewTicker(15 * time.Second)

	jobHandlers := []func() LogEntry{
		processEmailJob,
		processImageJob,
		processDataSyncJob,
		processReportJob,
	}

	for {
		select {
		case <-ticker.C:
			// Process a random job
			handler := jobHandlers[rand.Intn(len(jobHandlers))]
			handler()
		case <-statusTicker.C:
			// Log worker status
			logWorkerStatus()
		}
	}
}
