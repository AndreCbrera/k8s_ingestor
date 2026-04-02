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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)
)

type LogEntry struct {
	Timestamp    string `json:"timestamp"`
	Level        string `json:"level"`
	Service      string `json:"service"`
	Namespace    string `json:"namespace"`
	Message      string `json:"message"`
	Method       string `json:"method,omitempty"`
	Path         string `json:"path,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	ClientIP     string `json:"client_ip,omitempty"`
	UserAgent    string `json:"user_agent,omitempty"`
	ProductID    string `json:"product_id,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func init() {
	prometheus.MustRegister(requestsTotal, requestDuration)
}

func sendLog(entry LogEntry) {
	data, _ := json.Marshal(entry)
	fmt.Println(string(data))
}

func randomUserID() string {
	users := []string{"user_123", "user_456", "user_789", "user_abc", "user_xyz"}
	return users[rand.Intn(len(users))]
}

func randomProductID() string {
	products := []string{"prod_001", "prod_002", "prod_003", "prod_004", "prod_005"}
	return products[rand.Intn(len(products))]
}

func generateRequestID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomUserID())
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := generateRequestID()

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "demo"
	}

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   "api-service",
		Namespace: namespace,
		RequestID: requestID,
		ClientIP:  r.RemoteAddr,
		UserAgent: r.UserAgent(),
		Method:    r.Method,
		Path:      r.URL.Path,
	}

	// Simulate different endpoints
	switch r.URL.Path {
	case "/api/products":
		logEntry.Level = "info"
		logEntry.Message = "Fetching products list"
		logEntry.ProductID = randomProductID()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"products": [{"id": "%s", "name": "Sample Product", "price": 99.99}]}`, randomProductID())

	case "/api/products/" + randomProductID():
		logEntry.Level = "info"
		logEntry.Message = "Fetching product details"
		logEntry.ProductID = randomProductID()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"id": "%s", "name": "Sample Product", "price": 99.99, "stock": 150}`, randomProductID())

	case "/api/orders":
		if r.Method == "POST" {
			logEntry.Level = "info"
			logEntry.Message = "Creating new order"
			logEntry.UserID = randomUserID()
			logEntry.ProductID = randomProductID()
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"order_id": "ord_%d", "status": "created"}`, time.Now().Unix())
		}

	case "/api/users/" + randomUserID() + "/profile":
		logEntry.Level = "info"
		logEntry.Message = "Fetching user profile"
		logEntry.UserID = randomUserID()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"user_id": "%s", "name": "John Doe", "email": "john@example.com"}`, randomUserID())

	case "/api/health":
		logEntry.Level = "info"
		logEntry.Message = "Health check passed"
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "healthy"}`)

	default:
		logEntry.Level = "warning"
		logEntry.Message = fmt.Sprintf("Route not found: %s", r.URL.Path)
		logEntry.StatusCode = http.StatusNotFound
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error": "not found"}`)
	}

	// Simulate occasional errors
	if rand.Float32() < 0.05 {
		logEntry.Level = "error"
		logEntry.Message = "Database connection timeout"
		logEntry.ErrorMessage = "connection refused: dial tcp localhost:5432"
		logEntry.StatusCode = http.StatusInternalServerError
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error": "internal server error"}`)
	}

	if logEntry.StatusCode == 0 {
		logEntry.StatusCode = http.StatusOK
	}

	logEntry.DurationMs = time.Since(start).Milliseconds()
	requestsTotal.WithLabelValues(logEntry.Method, logEntry.Path, fmt.Sprintf("%d", logEntry.StatusCode)).Inc()
	requestDuration.WithLabelValues(logEntry.Method, logEntry.Path).Observe(time.Since(start).Seconds())

	sendLog(logEntry)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(start)

		namespace := os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			namespace = "demo"
		}

		logEntry := LogEntry{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Level:      "debug",
			Service:    "api-service",
			Namespace:  namespace,
			Message:    fmt.Sprintf("Request completed in %v", duration),
			Method:     r.Method,
			Path:       r.URL.Path,
			DurationMs: duration.Milliseconds(),
			ClientIP:   r.RemoteAddr,
			UserAgent:  r.UserAgent(),
		}
		sendLog(logEntry)
	})
}

func startLogGenerator() {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		namespace := os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			namespace = "demo"
		}

		for {
			<-ticker.C
			messages := []string{
				"Cache miss for key user_sessions",
				"Cache hit for key product_catalog",
				"Rate limit check passed",
				"Authentication token refreshed",
				"Database pool: 5/20 connections active",
				"Background sync completed",
			}
			levels := []string{"info", "debug", "info", "debug", "info", "info"}

			idx := rand.Intn(len(messages))
			logEntry := LogEntry{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Level:     levels[idx],
				Service:   "api-service",
				Namespace: namespace,
				Message:   messages[idx],
			}
			sendLog(logEntry)
			sendToIngestor(logEntry)
		}
	}()
}

func sendToIngestor(logEntry LogEntry) {
	ingestorURL := os.Getenv("INGESTOR_URL")
	if ingestorURL == "" {
		ingestorURL = "http://k8s-ingestor.logging.svc.cluster.local:8080/logs"
	}

	data, err := json.Marshal(logEntry)
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

func main() {
	rand.Seed(time.Now().UnixNano())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	startLogGenerator()

	mux := http.NewServeMux()
	mux.HandleFunc("/", apiHandler)
	mux.Handle("/metrics", promhttp.Handler())

	log.Printf("API Service starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, loggingMiddleware(mux)))
}
