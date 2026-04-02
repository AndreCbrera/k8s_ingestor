-- =====================================================
-- K8S LOG INGESTOR - Optimized Schema
-- =====================================================

-- Drop existing table if needed (BE CAREFUL!)
-- DROP TABLE IF EXISTS default.logs;

-- Main logs table with compression and optimized settings
CREATE TABLE IF NOT EXISTS default.logs
(
    timestamp DateTime CODEC(ZSTD(3)),
    cluster String CODEC(ZSTD(3)),
    namespace String CODEC(ZSTD(3)),
    pod String CODEC(ZSTD(3)),
    container String CODEC(ZSTD(3)),
    level LowCardinality(String) CODEC(ZSTD(3)),
    message String CODEC(ZSTD(3), LZ4),
    
    -- Advanced metadata
    labels Map(String, String) CODEC(ZSTD(3)),
    annotations Map(String, String) CODEC(ZSTD(3)),
    
    -- Enrichment fields
    node_name String DEFAULT '',
    host_ip String DEFAULT '',
    pod_ip String DEFAULT '',
    
    -- Trace IDs for distributed tracing
    trace_id String DEFAULT '',
    span_id String DEFAULT '',
    
    -- Processing metadata
    processed_at DateTime DEFAULT now(),
    ingestor_id String DEFAULT ''
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (timestamp, cluster, namespace, pod, level)
SETTINGS 
    index_granularity = 8192,
    bytes_to_compress = 102400,
    max_compress_block_size = 65536;

-- TTL: Auto-delete after 30 days
ALTER TABLE default.logs MODIFY TTL timestamp + INTERVAL 30 DAY;

-- =====================================================
-- Full-Text Search Index
-- =====================================================
CREATE TABLE IF NOT EXISTS default.logs_fts
(
    id UUID DEFAULT generateUUIDv4(),
    timestamp DateTime DEFAULT now(),
    namespace String,
    pod String,
    message String,
    
    -- Tokenized message for search
    message_tokens Array(String)
)
ENGINE = MergeTree()
ORDER BY (timestamp, namespace)
SETTINGS index_granularity = 8192;

-- =====================================================
-- Materialized Views for Aggregations
-- =====================================================

-- Per-minute aggregation
CREATE MATERIALIZED VIEW IF NOT EXISTS logs_by_minute
ENGINE = SummingMergeTree()
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, cluster, namespace, level)
AS SELECT 
    toStartOfMinute(timestamp) as minute,
    cluster,
    namespace,
    level,
    count() as count,
    sum(length(message)) as total_bytes
FROM default.logs
GROUP BY minute, cluster, namespace, level;

-- Per-hour aggregation
CREATE MATERIALIZED VIEW IF NOT EXISTS logs_by_hour
ENGINE = SummingMergeTree()
PARTITION BY toYYYYMMDD(hour)
ORDER BY (hour, cluster, namespace, level)
AS SELECT 
    toStartOfHour(timestamp) as hour,
    cluster,
    namespace,
    level,
    count() as count
FROM default.logs
GROUP BY hour, cluster, namespace, level;

-- Error tracking
CREATE MATERIALIZED VIEW IF NOT EXISTS logs_errors
ENGINE = SummingMergeTree()
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, namespace, pod)
AS SELECT 
    toStartOfMinute(timestamp) as minute,
    namespace,
    pod,
    count() as error_count
FROM default.logs
WHERE level = 'error'
GROUP BY minute, namespace, pod;

-- =====================================================
-- Sampling table for fast queries
-- =====================================================
CREATE TABLE IF NOT EXISTS logs_sample
ENGINE = MergeTree()
ORDER BY (timestamp, namespace)
SAMPLE BY timestamp
AS SELECT * FROM default.logs WHERE 1=0;

-- =====================================================
-- Dead Letter Queue Table
-- =====================================================
CREATE TABLE IF NOT EXISTS default.logs_dlq
(
    id UUID DEFAULT generateUUIDv4(),
    original_log String,
    error_message String,
    retry_count UInt32 DEFAULT 0,
    created_at DateTime DEFAULT now(),
    last_retry_at DateTime DEFAULT now(),
    resolved_at DateTime DEFAULT toDateTime(0),
    metadata Map(String, String)
)
ENGINE = MergeTree()
ORDER BY (created_at, id)
TTL created_at + INTERVAL 7 DAY;

-- =====================================================
-- Audit Log Table
-- =====================================================
CREATE TABLE IF NOT EXISTS default.ingestor_audit
(
    timestamp DateTime DEFAULT now(),
    ingestor_id String,
    action String,
    details String,
    ip_address String,
    user_agent String,
    success Boolean,
    duration_ms UInt32
)
ENGINE = MergeTree()
ORDER BY (timestamp, ingestor_id)
TTL timestamp + INTERVAL 90 DAY;

-- =====================================================
-- Optimized Views
-- =====================================================

-- Recent errors view
CREATE VIEW IF NOT EXISTS recent_errors AS
SELECT 
    timestamp,
    namespace,
    pod,
    container,
    left(message, 500) as message
FROM default.logs
WHERE level = 'error'
ORDER BY timestamp DESC
LIMIT 1000;

-- Error rate by namespace
CREATE VIEW IF NOT EXISTS error_rates AS
SELECT 
    namespace,
    count() as total,
    sum(level = 'error') as errors,
    round(sum(level = 'error') * 100.0 / count(), 2) as error_rate
FROM default.logs
WHERE timestamp > now() - INTERVAL 1 DAY
GROUP BY namespace
HAVING errors > 0
ORDER BY error_rate DESC;

-- Top error patterns
CREATE VIEW IF NOT EXISTS error_patterns AS
SELECT 
    arrayMap(x -> lower(x), extractAll(message, '[a-zA-Z]+')) as words,
    count() as cnt
FROM default.logs
WHERE level = 'error'
  AND timestamp > now() - INTERVAL 1 DAY
GROUP BY words
ORDER BY cnt DESC
LIMIT 50;
