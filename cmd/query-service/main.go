package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s_ingestor/internal/clickhouse"
	"k8s_ingestor/internal/config"
	queryhandler "k8s_ingestor/internal/query"
)

var (
	cfg      *config.Config
	chClient *clickhouse.Client

	metricsQueries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "queries_total",
			Help: "Total number of queries",
		},
		[]string{"status"},
	)
	metricsQueryDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "query_duration_seconds",
			Help:    "Query duration",
			Buckets: prometheus.DefBuckets,
		},
	)
)

func init() {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
}

func main() {
	prometheus.MustRegister(metricsQueries, metricsQueryDuration)

	cfg = config.Load()

	var err error
	chClient, err = clickhouse.NewClient(clickhouse.Config{
		Addr:            cfg.ClickHouseAddr,
		Database:        cfg.ClickHouseDatabase,
		Username:        cfg.ClickHouseUsername,
		Password:        cfg.ClickHousePassword,
		MaxOpenConns:    5,
		MaxIdleConns:    3,
		DialTimeout:     10 * time.Second,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		slog.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}

	q := queryhandler.New(chClient)

	mux := http.NewServeMux()
	mux.HandleFunc("/logs", q.QueryLogs)
	mux.HandleFunc("/logs/stats", q.GetStats)
	mux.HandleFunc("/logs/namespaces", q.GetNamespaces)
	mux.HandleFunc("/logs/pods", q.GetPods)
	mux.HandleFunc("/logs/levels", q.GetLevels)
	mux.HandleFunc("/logs/stream", q.StreamLogs)
	mux.HandleFunc("/health", q.Health)
	mux.Handle("/metrics", promhttp.Handler())

	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	slog.Info("query-service starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server error", "error", err)
	}
}
