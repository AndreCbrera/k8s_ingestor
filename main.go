package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type FluentBitLog struct {
	Log string `json:"log"`

	Kubernetes struct {
		Namespace string `json:"namespace_name"`
		Pod       string `json:"pod_name"`
		Container string `json:"container_name"`
	} `json:"kubernetes"`
}

var conn clickhouse.Conn
var logQueue = make(chan FluentBitLog, 10000)

func main() {
	var err error

	conn, err = clickhouse.Open(&clickhouse.Options{
		Addr: []string{"127.0.0.1:9000"},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "admin",
			Password: "admin123",
		},
		MaxOpenConns: 20,
		MaxIdleConns: 10,
		DialTimeout:  5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := conn.Ping(context.Background()); err != nil {
		log.Fatal("ClickHouse connection failed:", err)
	}

	startWorker()

	http.HandleFunc("/logs", handleLogs)

	log.Println("🚀 Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	log.Println("📦 RAW:", string(body))

	r.Body = io.NopCloser(strings.NewReader(string(body)))

	var logs []FluentBitLog

	if err := json.NewDecoder(r.Body).Decode(&logs); err != nil {
		var single FluentBitLog
		if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&single); err != nil {
			log.Println("❌ decode error:", err)
			http.Error(w, err.Error(), 400)
			return
		}
		logs = []FluentBitLog{single}
	}

	log.Println("📊 logs received:", len(logs))

	for _, l := range logs {
		logQueue <- l
	}

	w.WriteHeader(200)
}

func startWorker() {
	go func() {
		batch := make([]FluentBitLog, 0, 200)

		for {
			select {
			case l := <-logQueue:
				batch = append(batch, l)

				if len(batch) >= 200 {
					insertBatch(batch)
					batch = batch[:0]
				}

			case <-time.After(1 * time.Second):
				if len(batch) > 0 {
					insertBatch(batch)
					batch = batch[:0]
				}
			}
		}
	}()
}

func insertBatch(logs []FluentBitLog) {
	if len(logs) == 0 {
		return
	}

	batch, err := conn.PrepareBatch(context.Background(), `
		INSERT INTO logs 
		(timestamp, cluster, namespace, pod, container, level, message, labels)
	`)
	if err != nil {
		log.Println("❌ prepare error:", err)
		return
	}

	now := time.Now()
	count := 0

	for _, l := range logs {
		msg := cleanMessage(l.Log)
		level := detectLevel(msg)

		// 🔥 IMPORTANTE: fallback si no hay metadata
		namespace := l.Kubernetes.Namespace
		pod := l.Kubernetes.Pod
		container := l.Kubernetes.Container

		if namespace == "" {
			namespace = "unknown"
		}
		if pod == "" {
			pod = "unknown"
		}
		if container == "" {
			container = "unknown"
		}

		err := batch.Append(
			now,
			"dev-cluster",
			namespace,
			pod,
			container,
			level,
			msg,
			map[string]string{},
		)
		if err != nil {
			log.Println("❌ append error:", err)
			continue
		}

		count++
	}

	log.Println("📦 inserting:", count)

	if err := batch.Send(); err != nil {
		log.Println("❌ send error:", err)
		return
	}

	log.Println("✅ batch inserted")
}

func cleanMessage(msg string) string {
	parts := strings.SplitN(msg, " ", 4)
	if len(parts) == 4 {
		return parts[3]
	}
	return msg
}

func detectLevel(msg string) string {
	msg = strings.ToLower(msg)

	if strings.Contains(msg, "error") {
		return "error"
	}
	if strings.Contains(msg, "warn") {
		return "warn"
	}
	return "info"
}
