package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

	"k8s_ingestor/internal/clickhouse"
	"k8s_ingestor/internal/config"
	"k8s_ingestor/internal/handler"
	"k8s_ingestor/internal/masker"
	"k8s_ingestor/internal/tracing"
)

var (
	cfg         *config.Config
	chClient    *clickhouse.Client
	logMasker   *masker.Masker
	tracer      *tracing.Tracer
	limiter     *rate.Limiter
	isConnected atomic.Bool
	workerWg    sync.WaitGroup
	shutdownCh  chan struct{}
	logQueue    chan handler.ProcessedLog
	batchQueue  chan []handler.ProcessedLog
	h           *handler.Handler
	insertWg    sync.WaitGroup
	semaphore   chan struct{}

	metricsLogsReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "logs_received_total",
			Help: "Total number of logs received",
		},
		[]string{"status"},
	)
	metricsLogsInserted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "logs_inserted_total",
			Help: "Total number of logs inserted into ClickHouse",
		},
		[]string{"status"},
	)
	metricsBatchSize = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "batch_size",
			Help:    "Size of batches sent to ClickHouse",
			Buckets: prometheus.LinearBuckets(50, 50, 4),
		},
	)
	metricsInsertDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "insert_duration_seconds",
			Help:    "Time taken to insert batch into ClickHouse",
			Buckets: prometheus.DefBuckets,
		},
	)
	metricsQueueSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "log_queue_size",
			Help: "Current size of the log queue",
		},
	)
	metricsDLQSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dlq_size",
			Help: "Current size of the dead letter queue",
		},
	)
	metricsActiveInserts = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_inserts",
			Help: "Number of concurrent insert operations",
		},
	)
)

func init() {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(h))
}

func main() {
	prometheus.MustRegister(
		metricsLogsReceived,
		metricsLogsInserted,
		metricsBatchSize,
		metricsInsertDuration,
		metricsQueueSize,
		metricsDLQSize,
		metricsActiveInserts,
	)

	cfg = config.Load()
	limiter = rate.NewLimiter(rate.Limit(cfg.RateLimitPerSec), cfg.RateLimitBurst)
	shutdownCh = make(chan struct{})
	logQueue = make(chan handler.ProcessedLog, cfg.QueueSize)
	batchQueue = make(chan []handler.ProcessedLog, 100)

	handler.SetLogQueue(logQueue)

	logMasker = masker.New(cfg.LogMasking)

	tracer, _ = tracing.NewTracer(tracing.TracerConfig{
		Enabled:     cfg.OtelEnabled,
		Endpoint:    cfg.OtelEndpoint,
		ServiceName: cfg.OtelServiceName,
	})

	slog.Info("starting k8s-ingestor",
		"addr", cfg.ServerAddr,
		"workers", cfg.WorkerCount,
		"batch_size", cfg.BatchSize,
		"flush_interval", cfg.FlushInterval,
		"cluster", cfg.ClusterName,
		"log_masking", cfg.LogMasking,
		"tracing", cfg.OtelEnabled,
		"insert_workers", cfg.WorkerCount,
	)

	var err error
	chClient, err = clickhouse.NewClient(clickhouse.Config{
		Addr:            cfg.ClickHouseAddr,
		Database:        cfg.ClickHouseDatabase,
		Username:        cfg.ClickHouseUsername,
		Password:        cfg.ClickHousePassword,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		DialTimeout:     cfg.DialTimeout,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
		Compression:     "lz4",
	})
	if err != nil {
		slog.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}

	isConnected.Store(true)
	go monitorConnection()

	h = handler.New(cfg, logMasker, tracer)

	for i := 0; i < cfg.WorkerCount; i++ {
		workerWg.Add(1)
		go startWorker(i)
	}

	for i := 0; i < cfg.WorkerCount; i++ {
		insertWg.Add(1)
		go startInsertWorker(i)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/logs", withAuth(withRateLimit(h.HandleLogs)))
	mux.HandleFunc("/health", h.HandleHealth)
	mux.HandleFunc("/dlq", h.HandleDLQ)
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("server listening", "addr", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	close(shutdownCh)

	slog.Info("stopping HTTP server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("server shutdown error", "error", err)
	}

	workerWg.Wait()

	close(batchQueue)

	insertWg.Wait()

	if err := chClient.Close(); err != nil {
		slog.Error("error closing ClickHouse client", "error", err)
	}

	if err := tracer.Shutdown(context.Background()); err != nil {
		slog.Error("error shutting down tracer", "error", err)
	}

	slog.Info("shutdown complete")
}

func monitorConnection() {
	for {
		select {
		case <-shutdownCh:
			return
		default:
		}

		time.Sleep(10 * time.Second)

		if err := chClient.Ping(context.Background()); err != nil {
			slog.Warn("clickhouse connection lost, reconnecting...")
			isConnected.Store(false)

			var newClient *clickhouse.Client
			var err error
			for i := 0; i < cfg.MaxRetries; i++ {
				newClient, err = clickhouse.NewClient(clickhouse.Config{
					Addr:            cfg.ClickHouseAddr,
					Database:        cfg.ClickHouseDatabase,
					Username:        cfg.ClickHouseUsername,
					Password:        cfg.ClickHousePassword,
					MaxOpenConns:    cfg.MaxOpenConns,
					MaxIdleConns:    cfg.MaxIdleConns,
					DialTimeout:     cfg.DialTimeout,
					ConnMaxLifetime: cfg.ConnMaxLifetime,
				})
				if err == nil {
					chClient = newClient
					isConnected.Store(true)
					slog.Info("successfully reconnected to ClickHouse")
					break
				}
				slog.Warn("reconnection attempt failed", "attempt", i+1, "error", err)
				time.Sleep(time.Duration(i+1) * time.Second)
			}

			if !isConnected.Load() {
				slog.Error("failed to reconnect to ClickHouse after all retries")
				return
			}
		}
	}
}

func withRateLimit(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			metricsLogsReceived.WithLabelValues("rate_limited").Inc()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"success":false,"error":{"code":"RATE_LIMITED","message":"Rate limit exceeded"}}`)
			return
		}
		fn(w, r)
	}
}

func withAuth(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.APIKey == "" {
			fn(w, r)
			return
		}

		providedKey := r.Header.Get("X-API-Key")
		if providedKey == "" {
			providedKey = r.URL.Query().Get("api_key")
		}

		if providedKey != cfg.APIKey {
			slog.Warn("unauthorized access attempt", "ip", r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"success":false,"error":{"code":"UNAUTHORIZED","message":"Invalid API key"}}`)
			return
		}

		fn(w, r)
	}
}

func startWorker(id int) {
	defer workerWg.Done()

	batch := make([]handler.ProcessedLog, 0, cfg.BatchSize)
	ticker := time.NewTicker(cfg.FlushInterval)
	defer ticker.Stop()

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		b := batch
		batch = batch[:0]

		select {
		case batchQueue <- b:
			metricsQueueSize.Set(float64(len(logQueue)))
		default:
			slog.Warn("batch queue full, dropping batch", "size", len(b))
			metricsLogsInserted.WithLabelValues("dropped").Add(float64(len(b)))
		}
	}

	for {
		select {
		case <-shutdownCh:
			flushBatch()
			return

		case entry := <-logQueue:
			batch = append(batch, entry)
			metricsQueueSize.Set(float64(len(logQueue)))

			if len(batch) >= cfg.BatchSize {
				flushBatch()
			}

		case <-ticker.C:
			flushBatch()

		case <-time.After(100 * time.Millisecond):
			if len(batch) > 0 {
				flushBatch()
			}
			runtime.Gosched()
		}
	}
}

func startInsertWorker(id int) {
	defer insertWg.Done()

	for batch := range batchQueue {
		if len(batch) == 0 {
			continue
		}

		metricsActiveInserts.Inc()
		insertBatchAsync(batch)
		metricsActiveInserts.Dec()
	}
}

func insertBatchAsync(logs []handler.ProcessedLog) {
	go func() {
		insertBatchWithRetry(logs)
	}()
}

func insertBatchWithRetry(logs []handler.ProcessedLog) {
	var lastErr error
	for i := 0; i < cfg.MaxRetries; i++ {
		if err := insertBatch(logs); err == nil {
			return
		} else {
			lastErr = err
			slog.Warn("insert attempt failed", "attempt", i+1, "error", err)
			time.Sleep(cfg.RetryInterval * time.Duration(i+1))
		}
	}

	slog.Error("all insert attempts failed", "count", len(logs), "error", lastErr)
	metricsLogsInserted.WithLabelValues("failed").Add(float64(len(logs)))

	for range logs {
		handler.AddToDLQ(handler.FailedLog{
			Timestamp: time.Now(),
			Retries:   cfg.MaxRetries,
		})
	}
	metricsDLQSize.Add(float64(len(logs)))
}

func insertBatch(logs []handler.ProcessedLog) error {
	if len(logs) == 0 {
		return nil
	}

	if !isConnected.Load() {
		return fmt.Errorf("clickhouse not connected")
	}

	entries := make([]clickhouse.LogEntry, 0, len(logs))
	for _, l := range logs {
		entries = append(entries, clickhouse.LogEntry{
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
			PodIP:       l.PodIP,
			TraceID:     l.TraceID,
			SpanID:      l.SpanID,
			ProcessedAt: l.ProcessedAt,
			IngestorID:  l.IngestorID,
		})
	}

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := chClient.InsertBatch(ctx, entries); err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}

	metricsLogsInserted.WithLabelValues("success").Add(float64(len(entries)))
	metricsBatchSize.Observe(float64(len(entries)))
	metricsInsertDuration.Observe(time.Since(startTime).Seconds())

	slog.Info("batch inserted",
		"count", len(entries),
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	return nil
}
