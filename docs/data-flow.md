# Data Flow & Volume Analysis

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              KUBERNETES CLUSTER                                       │
│                                                                                       │
│  ┌─────────────┐    ┌──────────────┐    ┌─────────────┐    ┌────────────────────────┐ │
│  │   Pods      │    │   Fluent Bit │    │   Go API    │    │      ClickHouse        │ │
│  │             │    │   DaemonSet   │    │   Ingestor  │    │                        │ │
│  │  [nginx]    │───►│              │───►│             │───►│  [logs table]          │ │
│  │  [api]      │    │  Parser CRI  │    │  Workers    │    │                        │ │
│  │  [worker]   │    │  Lua Filter  │    │  Async Ins  │    │                        │ │
│  │  [db]       │    │              │    │             │    │                        │ │
│  └─────────────┘    └──────────────┘    └─────────────┘    └────────────────────────┘ │
│                                                                                       │
└─────────────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              DATA FLOW STEPS                                          │
└─────────────────────────────────────────────────────────────────────────────────────────┘

STEP 1: Log Generation
───────────────────────
                    Pods produce logs
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  Source: /var/log/containers/*.log (JSON format)                                     │
│                                                                                       │
│  Example log entry (avg):                                                             │
│  {                                                                                   │
│    "log": "2024-01-01 12:00:00 ERROR connection failed",                           │
│    "time": "2024-01-01T12:00:00Z",                                                  │
│    "kubernetes": {                                                                  │
│      "namespace_name": "production",                                                  │
│      "pod_name": "api-server-7d8f9c-xyz",                                            │
│      "container_name": "api"                                                          │
│    }                                                                                 │
│  }                                                                                   │
│                                                                                       │
│  Raw size per log: ~500 bytes                                                        │
└─────────────────────────────────────────────────────────────────────────────────────────┘
                         │
                         ▼
STEP 2: Fluent Bit Processing
──────────────────────────────
                    Parser + Filter + Forward
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  Fluent Bit transformations:                                                          │
│  - CRI Parser (JSON decode)                                                           │
│  - Kubernetes Metadata enrichment                                                     │
│  - Lua Filter (namespace exclusion)                                                    │
│  - Output to HTTP                                                                     │
│                                                                                       │
│  After enrichment (avg): ~800 bytes per log                                           │
│  Compression (gzip): ~300 bytes per log (63% reduction)                              │
│                                                                                       │
│  Output: HTTP POST batch to k8s-ingestor                                              │
│  Typical batch size: 200-500 logs                                                    │
└─────────────────────────────────────────────────────────────────────────────────────────┘
                         │
                         ▼
STEP 3: K8s Ingestor Processing
─────────────────────────────────
                    Receive → Validate → Queue → Insert
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  Processing stages:                                                                   │
│                                                                                       │
│  1. HTTP Handler                                                                      │
│     - Rate limiting                                                                   │
│     - Auth validation                                                                  │
│     - Gzip decode (if compressed)                                                     │
│     - JSON parsing                                                                     │
│                                                                                       │
│  2. Log Enrichment                                                                   │
│     - Add cluster name                                                                 │
│     - Detect log level (ERROR/WARN/INFO/DEBUG)                                       │
│     - Mask sensitive data (if enabled)                                                │
│     - Generate unique ID                                                              │
│                                                                                       │
│  3. Queue (in-memory)                                                                 │
│     - Buffered channel                                                                │
│     - Queue size: 50,000 logs max                                                     │
│                                                                                       │
│  4. Batch Processing                                                                  │
│     - Batch size: 200 logs                                                            │
│     - Flush interval: 1 second                                                        │
│     - Async inserts (non-blocking)                                                    │
│                                                                                       │
│  Memory usage per log: ~2KB                                                          │
└─────────────────────────────────────────────────────────────────────────────────────────┘
                         │
                         ▼
STEP 4: ClickHouse Storage
────────────────────────────
                    Compressed Columnar Storage
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│  ClickHouse storage:                                                                  │
│                                                                                       │
│  Table: logs                                                                          │
│  ├── timestamp (DateTime64)                                                           │
│  ├── cluster (LowCardinality)                                                         │
│  ├── namespace (LowCardinality)                                                       │
│  ├── pod (LowCardinality)                                                             │
│  ├── container (LowCardinality)                                                       │
│  ├── level (Enum8)                                                                    │
│  ├── message (String, ZSTD compressed)                                                │
│  ├── labels (JSON)                                                                    │
│  ├── annotations (JSON)                                                              │
│  └── ...                                                                              │
│                                                                                       │
│  Compression: ZSTD for messages, LZ4 for transfers                                   │
│  Final stored size: ~200 bytes per log (60% compression)                              │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

## Data Volume Estimates

### Input Calculations

| Metric | Value | Notes |
|--------|-------|-------|
| Pods per node | 20 | Average |
| Nodes per cluster | 10 | Example prod cluster |
| Logs per pod/min | 10 | Typical application |
| Log size (raw) | 500 bytes | JSON formatted |
| **Total logs/min** | **2,000** | 20 pods × 10 nodes × 10 logs |
| **Total logs/hour** | **120,000** | |
| **Total logs/day** | **2,880,000** | ~2.9M logs/day |
| **Total logs/month** | **86,400,000** | ~86M logs/month |

### Storage Calculations

| Stage | Size per Log | Daily Total | Monthly Total |
|-------|-------------|-------------|---------------|
| Raw (source) | 500 bytes | 1.44 GB | 43.2 GB |
| After Fluent Bit | 800 bytes | 2.3 GB | 69 GB |
| Gzip compressed | 300 bytes | 0.86 GB | 25.8 GB |
| **ClickHouse (compressed)** | **200 bytes** | **0.57 GB** | **17.2 GB** |

### Network Bandwidth

| Path | Calculation | Bandwidth |
|------|-------------|-----------|
| Fluent Bit → Ingestor | 300 bytes × 120K logs/hr | ~36 MB/hr ≈ **0.6 Mbps** |
| Ingestor → ClickHouse | 200 bytes × 120K logs/hr | ~24 MB/hr ≈ **0.4 Mbps** |

### Monthly Costs Estimate (AWS)

| Resource | Calculation | Est. Cost |
|----------|-------------|-----------|
| ClickHouse storage | 17.2 GB × 3 (replication) × $0.023/GB | **$1.19/mo** |
| Compute (3 nodes) | t3.medium × 3 | **~$60/mo** |
| EKS nodes (if separate) | 10 nodes × t3.medium | **~$150/mo** |
| **Total** | | **~$211/mo** |

## Real-Time Monitoring

### Metrics to Track

```bash
# Ingestor metrics
curl http://localhost:8080/metrics | grep -E "logs_received|logs_inserted|log_queue"

# Output example:
logs_received_total{status="success"} 1523456
logs_received_total{status="rate_limited"} 23
logs_inserted_total{status="success"} 1523400
logs_inserted_total{status="failed"} 56
log_queue_size 125
```

### Dashboard Panels

1. **Logs/sec** - `rate(logs_received_total[1m])`
2. **Queue depth** - `log_queue_size`
3. **Insert latency P99** - `histogram_quantile(0.99, insert_duration_seconds)`
4. **Error rate** - `rate(logs_inserted_total{status="failed"}[5m]) / rate(logs_received_total[5m])`
5. **Bandwidth** - `rate(logs_inserted_total{status="success"}[1m]) * 200 bytes`

## Performance Benchmarks

| Scenario | Throughput | Latency | CPU | Memory |
|----------|-----------|---------|-----|--------|
| Low load | 1,000 logs/sec | <10ms | 10% | 128MB |
| Medium load | 10,000 logs/sec | <50ms | 40% | 256MB |
| High load | 50,000 logs/sec | <100ms | 80% | 512MB |

### Scaling Recommendations

```
Logs/sec    │ Workers │ Batch Size │ Queue Size │ Memory
────────────┼─────────┼────────────┼────────────┼────────
< 1,000     │    2    │    100     │   10,000   │  128MB
1,000-5,000 │    4    │    200     │   50,000   │  256MB
5,000-20K   │    8    │    500     │  100,000   │  512MB
> 20,000    │   16    │    500     │  200,000   │  1GB
```

## Troubleshooting Data Flow

```bash
# Check where bottlenecks are
# 1. Fluent Bit queue
kubectl exec -n logging daemonset/fluent-bit -- fluent-bitctl info | grep -i queue

# 2. Ingestor queue depth
curl -s http://localhost:8080/health | jq

# 3. ClickHouse insert latency
clickhouse-client --query="SELECT quantile(0.99)(duration_ms) FROM system.query_log WHERE type='QueryFinish' AND query LIKE '%INSERT INTO logs%' AND event_date=today()"

# 4. Network saturation
kubectl top pods -n logging -l app=k8s-ingestor

# 5. ClickHouse storage
clickhouse-client --query="SELECT database, table, formatReadableSize(sum(bytes)) AS size FROM system.parts WHERE database='default' AND table='logs' GROUP BY database, table"
```
