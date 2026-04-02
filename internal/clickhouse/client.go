package clickhouse

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

func (c *Client) Query(ctx context.Context, sql string) (driver.Rows, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return conn.Query(queryCtx, sql)
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

type QueryOptions struct {
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

func (c *Client) QueryLogs(ctx context.Context, opts QueryOptions) ([]LogEntry, uint64, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, 0, fmt.Errorf("connection is nil")
	}

	var conditions []string

	if opts.Namespace != "" {
		conditions = append(conditions, fmt.Sprintf("namespace = '%s'", opts.Namespace))
	}
	if opts.Pod != "" {
		conditions = append(conditions, fmt.Sprintf("pod LIKE '%%%s%%'", opts.Pod))
	}
	if opts.Container != "" {
		conditions = append(conditions, fmt.Sprintf("container = '%s'", opts.Container))
	}
	if opts.Level != "" {
		conditions = append(conditions, fmt.Sprintf("level = '%s'", opts.Level))
	}
	if opts.Cluster != "" {
		conditions = append(conditions, fmt.Sprintf("cluster = '%s'", opts.Cluster))
	}
	if opts.Message != "" {
		conditions = append(conditions, fmt.Sprintf("message LIKE '%%%s%%'", opts.Message))
	}

	if !opts.StartTime.IsZero() {
		conditions = append(conditions, fmt.Sprintf("timestamp >= '%s'", opts.StartTime.Format("2006-01-02 15:04:05")))
	}
	if !opts.EndTime.IsZero() {
		conditions = append(conditions, fmt.Sprintf("timestamp <= '%s'", opts.EndTime.Format("2006-01-02 15:04:05")))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	orderDir := "DESC"
	if !opts.Descending {
		orderDir = "ASC"
	}

	orderBy := opts.OrderBy
	if orderBy == "" {
		orderBy = "timestamp"
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	countQuery := fmt.Sprintf("SELECT count() FROM logs %s", whereClause)
	var total uint64
	if err := conn.QueryRow(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count query: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT 
			timestamp, cluster, namespace, pod, container, level, message,
			labels, annotations, node_name, host_ip
		FROM logs 
		%s
		ORDER BY %s %s
		LIMIT %d OFFSET %d
	`, whereClause, orderBy, orderDir, limit, opts.Offset)

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var log LogEntry
		if err := rows.Scan(
			&log.Timestamp, &log.Cluster, &log.Namespace, &log.Pod, &log.Container,
			&log.Level, &log.Message, &log.Labels, &log.Annotations,
			&log.NodeName, &log.HostIP,
		); err != nil {
			return nil, 0, fmt.Errorf("scan: %w", err)
		}
		logs = append(logs, log)
	}

	return logs, total, nil
}

func (c *Client) GetDistinctValues(ctx context.Context, field, prefix string) ([]string, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	query := fmt.Sprintf("SELECT DISTINCT %s FROM logs WHERE timestamp > now() - INTERVAL 7 DAY ORDER BY %s", field, field)

	if prefix != "" {
		query = fmt.Sprintf("SELECT DISTINCT %s FROM logs WHERE timestamp > now() - INTERVAL 7 DAY AND %s LIKE '%s%%' ORDER BY %s", field, field, prefix, field)
	}

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			continue
		}
		if v != "" {
			values = append(values, v)
		}
	}

	return values, nil
}

func (c *Client) GetStatsFull(ctx context.Context) (*StatsFull, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}

	stats := &StatsFull{
		ByLevel:     make(map[string]int64),
		ByNamespace: make(map[string]int64),
		ByCluster:   make(map[string]int64),
	}

	row := conn.QueryRow(ctx, "SELECT count() FROM logs")
	row.Scan(&stats.TotalLogs)

	row = conn.QueryRow(ctx, "SELECT max(timestamp) FROM logs")
	row.Scan(&stats.LastIngestedAt)

	rows, _ := conn.Query(ctx, "SELECT level, count() FROM logs WHERE timestamp > now() - INTERVAL 1 HOUR GROUP BY level")
	for rows.Next() {
		var level string
		var count int64
		rows.Scan(&level, &count)
		stats.ByLevel[level] = count
	}
	rows.Close()

	rows, _ = conn.Query(ctx, "SELECT namespace, count() FROM logs WHERE timestamp > now() - INTERVAL 1 HOUR GROUP BY namespace ORDER BY count() DESC LIMIT 20")
	for rows.Next() {
		var ns string
		var count int64
		rows.Scan(&ns, &count)
		stats.ByNamespace[ns] = count
	}
	rows.Close()

	rows, _ = conn.Query(ctx, "SELECT cluster, count() FROM logs WHERE timestamp > now() - INTERVAL 1 HOUR GROUP BY cluster")
	for rows.Next() {
		var cluster string
		var count int64
		rows.Scan(&cluster, &count)
		stats.ByCluster[cluster] = count
	}
	rows.Close()

	return stats, nil
}

type StatsFull struct {
	TotalLogs      int64
	ByLevel        map[string]int64
	ByNamespace    map[string]int64
	ByCluster      map[string]int64
	LogsPerHour    []HourlyCount
	LastIngestedAt time.Time
}

type HourlyCount struct {
	Hour  time.Time
	Count int64
}
