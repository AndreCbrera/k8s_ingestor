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
	Timestamp  string `json:"timestamp"`
	Level      string `json:"level"`
	Service    string `json:"service"`
	Namespace  string `json:"namespace"`
	Message    string `json:"message"`
	UserID     string `json:"user_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Page       string `json:"page,omitempty"`
	Action     string `json:"action,omitempty"`
	Component  string `json:"component,omitempty"`
	ErrorMsg   string `json:"error_message,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

func sendLog(entry LogEntry) {
	data, _ := json.Marshal(entry)
	fmt.Println(string(data))
}

func randomUserID() string {
	users := []string{"user_123", "user_456", "user_789", "user_abc", "user_xyz"}
	return users[rand.Intn(len(users))]
}

func generateSessionID() string {
	return fmt.Sprintf("sess_%d%s", time.Now().UnixNano(), randomUserID())
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
			Level:      "info",
			Service:    "web-frontend",
			Namespace:  namespace,
			Message:    fmt.Sprintf("Served %s in %v", r.URL.Path, duration),
			Page:       r.URL.Path,
			DurationMs: duration.Milliseconds(),
		}
		sendLog(logEntry)
	})
}

func staticHandler(w http.ResponseWriter, r *http.Request) {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "demo"
	}

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "web-frontend",
		Namespace: namespace,
		Message:   "Serving static assets",
		Page:      r.URL.Path,
	}
	sendLog(logEntry)

	content := `<!DOCTYPE html>
<html>
<head>
    <title>Demo Store</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background: #f5f5f5; }
        .container { max-width: 800px; margin: 0 auto; background: white; padding: 20px; border-radius: 8px; }
        h1 { color: #333; }
        .product { border: 1px solid #ddd; padding: 15px; margin: 10px 0; border-radius: 4px; }
        .btn { background: #007bff; color: white; padding: 10px 20px; border: none; border-radius: 4px; cursor: pointer; }
        .btn:hover { background: #0056b3; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Welcome to Demo Store</h1>
        <p>Real-time log streaming from Kubernetes</p>
        <div id="products">
            <div class="product">
                <h3>Laptop Pro</h3>
                <p>$999.99</p>
                <button class="btn" onclick="addToCart('prod_001')">Add to Cart</button>
            </div>
            <div class="product">
                <h3>Wireless Mouse</h3>
                <p>$29.99</p>
                <button class="btn" onclick="addToCart('prod_002')">Add to Cart</button>
            </div>
        </div>
        <div id="logs" style="margin-top: 30px; padding: 15px; background: #f8f9fa; border-radius: 4px;">
            <h3>Recent Logs:</h3>
            <pre id="log-stream">Waiting for logs...</pre>
        </div>
    </div>
    <script>
        let sessionId = "''' + generateSessionID() + '''";
        let eventSource = new EventSource('/events');
        eventSource.onmessage = function(e) {
            document.getElementById('log-stream').innerHTML = e.data + '\n' + document.getElementById('log-stream').innerHTML;
        };
        function addToCart(productId) {
            fetch('/api/cart/add', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({productId: productId, sessionId: sessionId})
            });
        }
    </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, content)
}

func cartHandler(w http.ResponseWriter, r *http.Request) {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "demo"
	}

	logEntry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     "info",
		Service:   "web-frontend",
		Namespace: namespace,
		Message:   "Cart action received",
		Action:    "add_to_cart",
		Page:      "/api/cart/add",
	}
	sendLog(logEntry)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"success": true, "cart_count": 1}`)
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "demo"
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	for {
		<-ticker.C
		logEntry := LogEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Level:     "info",
			Service:   "web-frontend",
			Namespace: namespace,
			Message:   fmt.Sprintf("SSE heartbeat - %d active users", rand.Intn(100)+50),
			Page:      "/events",
		}
		data, _ := json.Marshal(logEntry)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()
	}
}

func startLogGenerator() {
	ticker := time.NewTicker(8 * time.Second)
	go func() {
		namespace := os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			namespace = "demo"
		}

		for {
			<-ticker.C
			messages := []struct {
				msg   string
				level string
				page  string
			}{
				{"User session started", "info", "/login"},
				{"Rendering product catalog", "debug", "/products"},
				{"Static assets cached", "debug", "/assets"},
				{"WebSocket connection established", "info", "/ws"},
				{"Analytics event sent", "debug", "/track"},
				{"User clicked product", "info", "/product"},
			}

			idx := rand.Intn(len(messages))
			logEntry := LogEntry{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Level:     messages[idx].level,
				Service:   "web-frontend",
				Namespace: namespace,
				Message:   messages[idx].msg,
				Page:      messages[idx].page,
				UserID:    randomUserID(),
				SessionID: generateSessionID(),
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
		port = "8081"
	}

	startLogGenerator()

	mux := http.NewServeMux()
	mux.HandleFunc("/", staticHandler)
	mux.HandleFunc("/api/cart/add", cartHandler)
	mux.HandleFunc("/events", eventsHandler)

	log.Printf("Web Frontend starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, loggingMiddleware(mux)))
}
