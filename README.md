# K8s Log Ingestor - Production Ready

Sistema de ingestión de logs de Kubernetes con Fluent Bit → Go Ingestor → ClickHouse.

## Tabla de Contenidos

- [Arquitectura](#arquitectura)
- [Quick Start](#quick-start)
- [API Reference](#api-reference)
- [Configuración](#configuración)
- [Deployment](#deployment)
- [Monitoring](#monitoring)
- [Performance](#performance)
- [Seguridad](#seguridad)
- [Desarrollo](#desarrollo)
- [Troubleshooting](#troubleshooting)

---

## Arquitectura

```
┌─────────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Kubernetes     │     │  Fluent Bit │     │   Go Ingestor   │
│  (pods/logs)   │ ──► │  (DaemonSet)│ ──► │   (HTTP API)    │
│                 │     │   + Lua     │     │  + Workers      │
└─────────────────┘     └──────────────┘     └────────┬────────┘
                                                      │
                         ┌─────────────────────────────┼─────────────────────────────┐
                         │                             │                             │
                         ▼                             ▼                             ▼
               ┌─────────────────┐          ┌─────────────────┐          ┌─────────────────┐
               │   ClickHouse    │          │   Prometheus    │          │    Grafana      │
               │ (Async Inserts) │          │   (Metrics)     │          │ (Dashboard)     │
               │  + Compression │          │   + Alerts      │          │                 │
               └─────────────────┘          └─────────────────┘          └─────────────────┘
```

### Flujo de Datos

1. **Fluent Bit** lee logs de `/var/log/containers/*.log`
2. **Parser CRI** decodifica formato JSON
3. **Lua Filter** excluye namespaces (kube-system, clickhouse-operator, logging)
4. **HTTP API** recibe logs en batches
5. **Workers** procesan y encolan logs
6. **Insert Workers** insertan async en ClickHouse
7. **DLQ** maneja logs fallidos

---

## Quick Start

### 1. Schema de ClickHouse

```bash
docker exec clickhouse clickhouse-client --user admin --password admin123 < schema/optimized_schema.sql
```

### 2. Ejecutar localmente

```bash
# Configurar ambiente
export CLICKHOUSE_ADDR="localhost:9000"
export CLICKHOUSE_PASSWORD="admin123"
export CLUSTER_NAME="dev-cluster"
export API_KEY="your-secret-key"

# Build y ejecutar
go build -o k8s-ingestor .
./k8s-ingestor
```

### 3. Docker

```bash
# Build
docker build -t k8s-ingestor:latest .

# Run
docker run -p 8080:8080 \
  -e CLICKHOUSE_ADDR="host.docker.internal:9000" \
  -e CLICKHOUSE_PASSWORD="admin123" \
  -e CLUSTER_NAME="docker" \
  k8s-ingestor:latest
```

### 4. Helm

```bash
helm install k8s-ingestor ./charts/k8s-ingestor \
  --set cluster.name=prod \
  --set clickhouse.password=secret \
  --set serviceMonitor.enabled=true
```

### 5. Load Testing

```bash
k6 run k6/load_test.js
```

---

## API Reference

### POST /logs

Recibe logs de Fluent Bit.

**Headers:**
- `Content-Type: application/json`
- `Content-Encoding: gzip` (opcional, gzip compression)
- `X-API-Key: <api-key>` (si auth habilitada)
- `X-Request-ID: <uuid>` (opcional, se genera si no existe)

**Request Body:**
```json
[
  {
    "log": "2024-01-01 12:00:00 ERROR something went wrong",
    "time": "2024-01-01T12:00:00Z",
    "kubernetes": {
      "namespace_name": "production",
      "pod_name": "api-server-7d8f9c",
      "container_name": "api",
      "labels": {"app": "api-server"},
      "host": "node-1"
    }
  }
]
```

**Response (200 OK):**
```json
{
  "success": true,
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2024-01-01T12:00:00.123Z",
  "data": {
    "accepted": 1,
    "rejected": 0,
    "log_ids": ["log-uuid-1"],
    "queued": 1
  }
}
```

**Error Response:**
```json
{
  "success": false,
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2024-01-01T12:00:00.123Z",
  "error": {
    "code": "INVALID_JSON",
    "message": "Invalid JSON format",
    "detail": "unexpected end of JSON input"
  }
}
```

### GET /health

Health check con estado del sistema.

**Response:**
```json
{
  "success": true,
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "data": {
    "status": "healthy",
    "ingestor_id": "prod-ingestor-1",
    "queue_size": 150,
    "timestamp": "2024-01-01T12:00:00Z"
  }
}
```

### GET /dlq

Estadísticas de la Dead Letter Queue.

**Response:**
```json
{
  "success": true,
  "request_id": "...",
  "data": {
    "dlq_size": 5,
    "dlq_capacity": 10000
  }
}
```

### POST /dlq

Reintentar logs fallidos.

**Request:**
```json
["log-id-1", "log-id-2"]
```

### DELETE /dlq

Limpiar la DLQ.

### GET /metrics

Métricas Prometheus (formato OpenMetrics).

---

## Configuración

### Variables de Entorno

| Variable | Default | Descripción |
|----------|---------|-------------|
| `SERVER_ADDR` | `:8080` | Dirección del servidor HTTP |
| `CLICKHOUSE_ADDR` | `127.0.0.1:9000` | Host de ClickHouse |
| `CLICKHOUSE_DATABASE` | `default` | Base de datos |
| `CLICKHOUSE_USERNAME` | `admin` | Usuario |
| `CLICKHOUSE_PASSWORD` | `admin123` | Contraseña |
| `CLUSTER_NAME` | `unknown` | Nombre del cluster |
| `BATCH_SIZE` | `200` | Logs por batch |
| `FLUSH_INTERVAL_SEC` | `1` | Intervalo de flush (segundos) |
| `WORKER_COUNT` | `4` | Número de workers |
| `QUEUE_SIZE` | `50000` | Tamaño de la cola |
| `MAX_RETRIES` | `3` | Reintentos en error |
| `RETRY_INTERVAL_SEC` | `1` | Intervalo entre reintentos |
| `RATE_LIMIT_PER_SEC` | `1000` | Límite de requests/segundo |
| `RATE_LIMIT_BURST` | `2000` | Burst para rate limiter |
| `API_KEY` | `` | API key para autenticación |
| `LOG_MASKING_ENABLED` | `false` | Habilitar masking de datos sensibles |
| `OTEL_ENABLED` | `false` | Habilitar OpenTelemetry tracing |
| `OTEL_ENDPOINT` | `localhost:4317` | Endpoint de OTEL collector |
| `OTEL_SERVICE_NAME` | `k8s-ingestor` | Nombre del servicio |
| `READ_TIMEOUT_SEC` | `30` | Read timeout HTTP |
| `WRITE_TIMEOUT_SEC` | `30` | Write timeout HTTP |

### ClickHouse Connection

| Variable | Default | Descripción |
|----------|---------|-------------|
| `CLICKHOUSE_MAX_OPEN_CONNS` | `10` | Conexiones abiertas máxima |
| `CLICKHOUSE_MAX_IDLE_CONNS` | `5` | Conexiones idle máxima |
| `CLICKHOUSE_DIAL_TIMEOUT_SEC` | `10` | Timeout de conexión |
| `CLICKHOUSE_CONN_MAX_LIFETIME_SEC` | `3600` | Vida máxima de conexión |

---

## Deployment

### Helm Values

```yaml
# values.yaml
replicaCount: 3

cluster:
  name: production

clickhouse:
  addr: "clickhouse.clickhouse.svc.cluster.local:9000"
  database: "logs"
  username: "admin"
  existingSecret: "clickhouse-credentials"

ingestor:
  batchSize: 500
  flushInterval: 1
  workers: 8
  queueSize: 100000
  rateLimitPerSec: 5000
  rateLimitBurst: 10000
  logMasking: true

resources:
  limits:
    cpu: 2000m
    memory: 1Gi
  requests:
    cpu: 500m
    memory: 256Mi

hpa:
  enabled: true
  minReplicas: 2
  maxReplicas: 20
  targetCPUUtilizationPercentage: 60

serviceMonitor:
  enabled: true
```

### FluxCD Integration

```yaml
# flux-helmrelease.yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: k8s-ingestor
  namespace: logging
spec:
  chart:
    spec:
      chart: k8s-ingestor
      version: "1.0.0"
      sourceRef:
        kind: HelmRepository
        name: k8s-ingestor
  values:
    cluster:
      name: prod-cluster
    hpa:
      enabled: true
```

---

## Monitoring

### Prometheus Metrics

| Métrica | Tipo | Descripción |
|---------|------|-------------|
| `logs_received_total{status}` | Counter | Logs recibidos (success, rate_limited, failed) |
| `logs_inserted_total{status}` | Counter | Logs insertados en ClickHouse |
| `batch_size` | Histogram | Tamaño de batches |
| `insert_duration_seconds` | Histogram | Duración de inserts |
| `log_queue_size` | Gauge | Logs en cola |
| `dlq_size` | Gauge | Logs en DLQ |
| `active_inserts` | Gauge | Insert workers activos |

### Dashboards

Grafana dashboard disponible en `monitoring/grafana-dashboard.json`

### Prometheus Alerts

```yaml
groups:
  - name: k8s-ingestor
    rules:
      - alert: IngestorDown
        expr: up{job="k8s-ingestor"} == 0
        for: 1m

      - alert: IngestorHighQueueSize
        expr: log_queue_size > 40000
        for: 5m

      - alert: IngestorHighErrorRate
        expr: rate(logs_inserted_total{status="failed"}[5m]) > 0.1
        for: 5m

      - alert: IngestorNoLogs
        expr: rate(logs_received_total[5m]) == 0
        for: 10m
```

---

## Performance

### Optimizaciones Implementadas

| Optimización | Impacto |
|--------------|---------|
| **Async Inserts** | HTTP no espera por inserts |
| **Parallel Workers** | Múltiples goroutines insertando |
| **Gzip Compression** | ~70% reducción bandwidth |
| **Memory Pooling** | Menos GC, mejor throughput |
| **Batch Queue** | Decoupling y backpressure |
| **ZSTD/LZ4** | Compresión en ClickHouse |

### Arquitectura de Rendimiento

```
                    ┌─────────────────────────────────────┐
                    │           HTTP Server               │
                    │  (gzip decode, rate limit, auth)   │
                    └────────────────┬────────────────────┘
                                     │
                    ┌────────────────▼────────────────────┐
                    │          logQueue (buffered)         │
                    │           capacity: 50000           │
                    └────────────────┬────────────────────┘
                                     │
                    ┌────────────────▼────────────────────┐
                    │          Workers (N)                │
                    │   (batch, flush on size/time)      │
                    └────────────────┬────────────────────┘
                                     │
                    ┌────────────────▼────────────────────┐
                    │        batchQueue (buffered)        │
                    │           capacity: 100            │
                    └────────────────┬────────────────────┘
                                     │
        ┌─────────────────────────────┼─────────────────────────────┐
        │                             │                             │
        ▼                             ▼                             ▼
┌───────────────┐           ┌───────────────┐           ┌───────────────┐
│ Insert Worker │           │ Insert Worker │           │ Insert Worker │
│  (async goroutine)       │  (async goroutine)        │  (async goroutine)
└───────┬───────┘           └───────┬───────┘           └───────┬───────┘
        │                             │                             │
        └─────────────────────────────┼─────────────────────────────┘
                                      │
                    ┌─────────────────▼─────────────────┐
                    │         ClickHouse                │
                    │    (LZ4 compression, async)      │
                    └─────────────────────────────────┘
```

### Benchmarks Esperados

- **Throughput**: 50,000+ logs/segundo
- **Latency P99**: < 50ms
- **Memory**: ~256MB baseline
- **CPU**: Escalable con workers

---

## Seguridad

### Log Masking

Patrones detectados automáticamente:

- Emails: `user@example.com` → `***@***.***`
- IPs: `192.168.1.1` → `***.***.***.***`
- Credit Cards: `4111-1111-1111-1111` → `****-****-****-****`
- SSN: `123-45-6789` → `***-**-****`
- AWS Keys: `AKIAIOSFODNN7EXAMPLE` → `***`
- JWT Tokens: `eyJhbGciOiJIUzI1NiIs...` → `***`
- Passwords: `password=secret` → `password=***`
- API Keys: `sk_live_...` → `***`
- Authorization Headers: `Bearer token` → `Bearer ***`

### API Authentication

```bash
# Con header
curl -X POST http://localhost:8080/logs \
  -H "X-API-Key: your-secret-key" \
  -d '[{"log": "...", "time": "..."}]'

# Con query param
curl -X POST "http://localhost:8080/logs?api_key=your-secret-key" \
  -d '[{"log": "...", "time": "..."}]'
```

### mTLS

```bash
# Generar certificados
./scripts/generate-certs.sh

# Configurar en Helm
helm install k8s-ingestor ./charts/k8s-ingestor \
  --set tls.enabled=true \
  --set tls.secretName=k8s-ingestor-tls
```

---

## Desarrollo

### Estructura del Proyecto

```
.
├── main.go                          # Entry point
├── internal/
│   ├── config/config.go              # Configuration management
│   ├── handler/handler.go           # HTTP handlers
│   ├── clickhouse/client.go          # ClickHouse client
│   ├── masker/masker.go             # Data masking
│   └── tracing/tracing.go           # OpenTelemetry tracing
├── schema/
│   └── optimized_schema.sql          # ClickHouse DDL
├── fluentbit_conf/
│   ├── cm.yaml                      # Fluent Bit ConfigMap
│   ├── ds.yaml                      # Fluent Bit DaemonSet
│   └── rbac.yaml                    # RBAC
├── charts/
│   └── k8s-ingestor/                # Helm chart
├── gitops/
│   ├── flux-helmrelease.yaml        # FluxCD HelmRelease
│   ├── flux-kustomization.yaml      # FluxCD Kustomization
│   └── overlays/production/         # Production values
├── monitoring/
│   ├── prometheus.yaml               # Prometheus config
│   ├── prometheus-alerts.yaml       # Alerts
│   └── grafana-dashboard.json       # Dashboard
├── scripts/
│   ├── backup.sh                    # ClickHouse backup
│   └── generate-certs.sh            # mTLS certs
├── tests/
│   ├── unit/                       # Unit tests
│   └── integration/                 # Integration tests
└── k6/
    └── load_test.js                # k6 load test
```

### Tests

```bash
# Unit tests
go test ./tests/unit/... -v

# Integration tests
go test ./tests/integration/... -v

# Todos los tests
go test ./... -v -race

# Coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Build

```bash
# Binary local
go build -o k8s-ingestor .

# Docker
docker build -t k8s-ingestor:latest .

# Docker multi-arch
docker buildx build --platform linux/amd64,linux/arm64 \
  -t k8s-ingestor:latest .
```

---

## Troubleshooting

### Health Check

```bash
curl http://localhost:8080/health
```

### Ver Logs

```bash
# Kubernetes
kubectl logs -n logging -l app=k8s-ingestor -f

# Docker
docker logs -f <container-id>
```

### Ver Métricas

```bash
curl http://localhost:8080/metrics
```

### DLQ Stats

```bash
curl http://localhost:8080/dlq
```

### Ver Cola

```bash
# Ver tamaño de cola en métricas
curl -s http://localhost:8080/metrics | grep log_queue_size
```

### Common Issues

**Queue Full**: Incrementar `QUEUE_SIZE` o agregar más workers.

**ClickHouse Connection Lost**: El ingestor auto-reconnecta. Ver logs.

**High Latency**: Reducir `BATCH_SIZE` o incrementar workers.

**Memory High**: Reducir `QUEUE_SIZE` o `WORKER_COUNT`.

---

## Consultas ClickHouse

Consultas útiles disponibles en `queries.sql`:

```sql
-- Logs por namespace (últimas 24h)
SELECT namespace, count() as logs
FROM logs
WHERE timestamp > now() - INTERVAL 24 HOUR
GROUP BY namespace
ORDER BY logs DESC;

-- Tasa de errores
SELECT namespace,
       sum(level = 'error') * 100.0 / count() as error_rate
FROM logs
WHERE timestamp > now() - INTERVAL 1 HOUR
GROUP BY namespace
HAVING error_rate > 1;

-- Logs recientes de un pod
SELECT timestamp, level, message
FROM logs
WHERE pod LIKE '%api-server%'
ORDER BY timestamp DESC
LIMIT 100;

-- Agregación por contenedor
SELECT cluster, namespace, container,
       count() as total,
       sum(level = 'error') as errors
FROM logs
WHERE timestamp > now() - INTERVAL 1 HOUR
GROUP BY cluster, namespace, container;
```

---

## Scripts

```bash
# Backup de ClickHouse
./scripts/backup.sh

# Generar certificados mTLS
./scripts/generate-certs.sh

# Restore backup
./scripts/backup.sh --restore backup-2024-01-01.tar.gz
```

---

## Licencia

MIT
