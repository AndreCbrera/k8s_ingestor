package clickhouse

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type Client struct {
	conn    driver.Conn
	cfg     Config
	mu      sync.RWMutex
	healthy bool
}

type Config struct {
	Addr            string
	Database        string
	Username        string
	Password        string
	MaxOpenConns    int
	MaxIdleConns    int
	DialTimeout     time.Duration
	ConnMaxLifetime time.Duration
	Compression     string
}

func NewClient(cfg Config) (*Client, error) {
	c := &Client{cfg: cfg}

	options := &clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Settings: clickhouse.Settings{
			"max_execution_time":    60,
			"max_block_size":        10000,
			"max_insert_block_size": 100000,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout:  cfg.DialTimeout,
		MaxOpenConns: cfg.MaxOpenConns,
		MaxIdleConns: cfg.MaxIdleConns,
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}

	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping: %w", err)
	}

	c.conn = conn
	c.healthy = true

	slog.Info("clickhouse client connected",
		"addr", cfg.Addr,
		"database", cfg.Database,
	)

	return c, nil
}

func (c *Client) Ping(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conn == nil {
		return fmt.Errorf("connection is nil")
	}

	return c.conn.Ping(ctx)
}

func (c *Client) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy
}

func (c *Client) InsertBatch(ctx context.Context, logs []LogEntry) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	batch, err := conn.PrepareBatch(ctx, `
		INSERT INTO logs (
			timestamp, cluster, namespace, pod, container, level, message,
			labels, annotations, node_name, host_ip, pod_ip,
			trace_id, span_id, processed_at, ingestor_id
		)
	`)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, log := range logs {
		err := batch.Append(
			log.Timestamp,
			log.Cluster,
			log.Namespace,
			log.Pod,
			log.Container,
			log.Level,
			log.Message,
			log.Labels,
			log.Annotations,
			log.NodeName,
			log.HostIP,
			log.PodIP,
			log.TraceID,
			log.SpanID,
			log.ProcessedAt,
			log.IngestorID,
		)
		if err != nil {
			slog.Warn("failed to append log", "error", err, "pod", log.Pod)
			continue
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send batch: %w", err)
	}

	return nil
}

func (c *Client) InsertDLQ(ctx context.Context, entries []DLQEntry) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	batch, err := conn.PrepareBatch(ctx, `
		INSERT INTO logs_dlq (original_log, error_message, retry_count, metadata)
	`)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		batch.Append(
			entry.OriginalLog,
			entry.ErrorMessage,
			entry.RetryCount,
			entry.Metadata,
		)
	}

	return batch.Send()
}

func (c *Client) RecordAudit(ctx context.Context, entry AuditEntry) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	return conn.AsyncInsert(ctx, `
		INSERT INTO ingestor_audit (timestamp, ingestor_id, action, details, ip_address, user_agent, success, duration_ms)
	`, false, []interface{}{
		entry.Timestamp,
		entry.IngestorID,
		entry.Action,
		entry.Details,
		entry.IPAddress,
		entry.UserAgent,
		entry.Success,
		entry.DurationMs,
	})
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.healthy = false

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type LogEntry struct {
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

type DLQEntry struct {
	OriginalLog  string
	ErrorMessage string
	RetryCount   uint32
	Metadata     map[string]string
}

type AuditEntry struct {
	Timestamp  time.Time
	IngestorID string
	Action     string
	Details    string
	IPAddress  string
	UserAgent  string
	Success    bool
	DurationMs uint32
}

func (c *Client) GetStats(ctx context.Context) (*Stats, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	var stats Stats

	row := conn.QueryRow(ctx, "SELECT count() FROM default.logs")
	if err := row.Scan(&stats.TotalLogs); err != nil {
		return nil, err
	}

	row = conn.QueryRow(ctx, "SELECT count() FROM default.logs WHERE timestamp > now() - INTERVAL 1 HOUR")
	if err := row.Scan(&stats.LogsLastHour); err != nil {
		return nil, err
	}

	row = conn.QueryRow(ctx, "SELECT count() FROM default.logs WHERE level = 'error' AND timestamp > now() - INTERVAL 1 HOUR")
	if err := row.Scan(&stats.ErrorsLastHour); err != nil {
		return nil, err
	}

	return &stats, nil
}

type Stats struct {
	TotalLogs      uint64
	LogsLastHour   uint64
	ErrorsLastHour uint64
}
