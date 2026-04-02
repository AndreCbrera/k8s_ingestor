-- =====================================================
-- K8S LOG INGESTOR - ClickHouse Queries
-- =====================================================

-- =====================================================
-- SCHEMA
-- =====================================================

-- Create logs table
CREATE TABLE logs (
    timestamp DateTime,
    cluster String,
    namespace String,
    pod String,
    container String,
    level LowCardinality(String),
    message String,
    labels Map(String, String)
)
ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (timestamp, namespace, pod)
SETTINGS index_granularity = 8192;


-- =====================================================
-- CLEANUP & MAINTENANCE
-- =====================================================

-- Add TTL (auto-delete after 30 days)
ALTER TABLE default.logs MODIFY TTL timestamp + INTERVAL 30 DAY;

-- Delete logs with unknown namespace
ALTER TABLE default.logs DELETE WHERE namespace = 'unknown';

-- Delete logs from excluded namespaces
ALTER TABLE default.logs DELETE WHERE namespace IN ('clickhouse-operator', 'kube-system', 'logging');

-- Delete logs with empty namespace
ALTER TABLE default.logs DELETE WHERE namespace = '';


-- =====================================================
-- STATISTICS
-- =====================================================

-- Total logs
SELECT count() as total_logs FROM default.logs;

-- Logs by namespace
SELECT namespace, count() as cnt 
FROM default.logs 
GROUP BY namespace 
ORDER BY cnt DESC;

-- Logs by level
SELECT level, count() as cnt 
FROM default.logs 
GROUP BY level 
ORDER BY cnt DESC;

-- Logs by namespace and level
SELECT namespace, level, count() as cnt 
FROM default.logs 
GROUP BY namespace, level 
ORDER BY cnt DESC;

-- Time range of logs
SELECT 
    min(timestamp) as first_log, 
    max(timestamp) as last_log 
FROM default.logs;


-- =====================================================
-- MONITORING
-- =====================================================

-- Recent logs (last 10)
SELECT timestamp, namespace, pod, container, level, left(message, 100) as msg
FROM default.logs 
ORDER BY timestamp DESC 
LIMIT 10;

-- Logs from specific pod
SELECT timestamp, level, message 
FROM default.logs 
WHERE pod = 'test-pod' 
ORDER BY timestamp DESC 
LIMIT 20;

-- Logs with errors
SELECT timestamp, namespace, pod, message 
FROM default.logs 
WHERE level = 'error' 
ORDER BY timestamp DESC 
LIMIT 20;

-- Logs by namespace in last hour
SELECT namespace, count() as cnt 
FROM default.logs 
WHERE timestamp > now() - INTERVAL 1 HOUR
GROUP BY namespace 
ORDER BY cnt DESC;

-- Logs per minute (rate)
SELECT 
    toStartOfMinute(timestamp) as minute,
    count() as logs_count
FROM default.logs 
WHERE timestamp > now() - INTERVAL 1 HOUR
GROUP BY minute 
ORDER BY minute;


-- =====================================================
-- AGGREGATIONS
-- =====================================================

-- Create materialized view for summary
CREATE MATERIALIZED VIEW IF NOT EXISTS logs_summary
ENGINE = SummingMergeTree()
PARTITION BY toDate(minute)
ORDER BY (minute, namespace, level)
AS SELECT 
    toStartOfMinute(timestamp) as minute,
    namespace,
    level,
    count() as count
FROM default.logs
GROUP BY minute, namespace, level;

-- Query aggregated summary
SELECT minute, namespace, level, count
FROM logs_summary
WHERE minute > now() - INTERVAL 1 HOUR
ORDER BY minute DESC, count DESC;

-- Top 10 pods by log count
SELECT pod, namespace, count() as cnt
FROM default.logs
GROUP BY pod, namespace
ORDER BY cnt DESC
LIMIT 10;

-- Top 10 namespaces by log volume
SELECT namespace, count() as total_logs,
       sum(level = 'error') as error_count,
       sum(level = 'warn') as warn_count
FROM default.logs
GROUP BY namespace
ORDER BY total_logs DESC
LIMIT 10;


-- =====================================================
-- DATA ANALYSIS
-- =====================================================

-- Error rate by namespace (last 24h)
SELECT 
    namespace,
    count() as total,
    sum(level = 'error') as errors,
    round(sum(level = 'error') * 100.0 / count(), 2) as error_rate_pct
FROM default.logs
WHERE timestamp > now() - INTERVAL 1 DAY
GROUP BY namespace
HAVING errors > 0
ORDER BY error_rate_pct DESC;

-- Most common messages (excluding simple errors)
SELECT left(message, 100) as msg_pattern, count() as cnt
FROM default.logs
WHERE timestamp > now() - INTERVAL 1 DAY
  AND message NOT LIKE '%error from k8s%'
GROUP BY msg_pattern
ORDER BY cnt DESC
LIMIT 20;

-- Logs with specific keywords
SELECT timestamp, namespace, pod, message
FROM default.logs
WHERE message LIKE '%connection%' 
   OR message LIKE '%timeout%'
   OR message LIKE '%failed%'
ORDER BY timestamp DESC
LIMIT 50;


-- =====================================================
-- DEBUGGING
-- =====================================================

-- Check for duplicate timestamps (data quality)
SELECT timestamp, namespace, pod, count() as cnt
FROM default.logs
GROUP BY timestamp, namespace, pod
HAVING cnt > 1
ORDER BY timestamp DESC
LIMIT 10;

-- Check table size
SELECT 
    table,
    formatReadableSize(sum(bytes)) as size,
    sum(rows) as rows
FROM system.parts
WHERE table = 'logs' AND database = 'default' AND active
GROUP BY table;

-- Check partition info
SELECT 
    partition,
    sum(rows) as rows,
    formatReadableSize(sum(bytes)) as size
FROM system.parts
WHERE table = 'logs' AND database = 'default' AND active
GROUP BY partition
ORDER BY partition DESC;

-- List all tables
SELECT * FROM system.tables WHERE database = 'default';


-- =====================================================
-- TIPS & BEST PRACTICES
-- =====================================================

-- Use SAMPLE for large tables (1% sample)
-- SELECT namespace, count() FROM default.logs SAMPLE 0.01 GROUP BY namespace;

-- Use FINAL for MergeTree tables (get latest values)
-- SELECT * FROM default.logs FINAL WHERE pod = 'test-pod';

-- Use prewhere for faster filtering on specific columns
-- SELECT timestamp, message FROM default.logs PREWHERE namespace = 'default';

-- Optimize queries with date range
-- SELECT * FROM default.logs WHERE timestamp BETWEEN '2026-04-01' AND '2026-04-02';
