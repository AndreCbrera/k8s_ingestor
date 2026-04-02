# K8s Log Ingestor - Production Ready

Sistema de ingestión de logs de Kubernetes con Fluent Bit, Go y ClickHouse.

## Arquitectura

```
┌─────────────────┐     ┌──────────────┐     ┌───────────────┐
│  Kubernetes     │     │  Fluent Bit │     │   Go App      │
│  (pods/logs)   │ ──► │  (DaemonSet)│ ──► │  (ingestor)   │
│                 │     │   + Lua     │     │   + Workers   │
└─────────────────┘     └──────────────┘     └───────┬───────┘
                                                      │
                    ┌────────────────────────────────┼────────────────┐
                    │                                │                │
                    ▼                                ▼                ▼
          ┌─────────────────┐          ┌───────────────┐    ┌─────────────┐
          │    ClickHouse    │          │   Prometheus  │    │   Grafana   │
          │  (Compression)   │          │   (Metrics)  │    │ (Dashboard) │
          └─────────────────┘          └───────────────┘    └─────────────┘
```

## Mejoras Implementadas

### 1. Compresión y Optimización ClickHouse
- **ZSTD compression** en todas las columnas
- **LZ4** para mensajes
- **TTL de 30 días** para auto-limpieza
- **Materialized Views** para agregaciones
- **Full-text search index**

### 2. Procesamiento Avanzado
- **Múltiples workers** para procesamiento paralelo
- **Dead Letter Queue** para logs fallidos
- **Batch inserts** optimizados
- **Retry logic** con backoff exponencial

### 3. Seguridad
- **Log masking** para datos sensibles (emails, IPs, passwords, tokens)
- **mTLS** ready con certificados
- **API key authentication**
- **Rate limiting** configurable
- **Security contexts** en Kubernetes

### 4. Observabilidad
- **OpenTelemetry** distributed tracing
- **Prometheus metrics** completas
- **Grafana dashboard** pre-configurado
- **Alertas** de Prometheus
- **Structured JSON logging**

### 5. Deployment
- **Helm chart** completo con valores configurables
- **GitOps** con FluxCD
- **Kustomize** para diferentes entornos
- **HPA** para auto-scaling
- **PodDisruptionBudget** para alta disponibilidad

### 6. Testing
- **Unit tests** con coverage
- **Integration tests**
- **k6 load testing**

## Inicio Rápido

### 1. Schema de ClickHouse
```bash
docker exec clickhouse clickhouse-client --user admin --password admin123 < schema/optimized_schema.sql
```

### 2. Ejecutar localmente
```bash
go mod download
go build -o app .
./app
```

### 3. Deploy con Helm
```bash
helm install k8s-ingestor ./charts/k8s-ingestor
```

### 4. Load Testing
```bash
k6 run k6/load_test.js
```

## Variables de Entorno

| Variable | Default | Descripción |
|----------|---------|-------------|
| `SERVER_ADDR` | `:8080` | Dirección del servidor |
| `CLICKHOUSE_ADDR` | `127.0.0.1:9000` | Host de ClickHouse |
| `CLICKHOUSE_USERNAME` | `admin` | Usuario |
| `CLICKHOUSE_PASSWORD` | `admin123` | Contraseña |
| `BATCH_SIZE` | `200` | Tamaño del batch |
| `WORKER_COUNT` | `4` | Número de workers |
| `QUEUE_SIZE` | `50000` | Tamaño de la cola |
| `MAX_RETRIES` | `3` | Reintentos en error |
| `RATE_LIMIT_PER_SEC` | `1000` | Límite de requests/seg |
| `LOG_MASKING_ENABLED` | `false` | Habilitar masking |
| `OTEL_ENABLED` | `false` | Habilitar tracing |
| `KAFKA_ENABLED` | `false` | Habilitar Kafka buffer |

## Estructura del Proyecto

```
.
├── cmd/                    # Punto de entrada
├── internal/
│   ├── config/           # Configuración
│   ├── handler/          # HTTP handlers
│   ├── worker/           # Workers de procesamiento
│   ├── clickhouse/       # Cliente ClickHouse
│   ├── queue/            # Colas (Kafka/Redis)
│   ├── tracing/          # OpenTelemetry
│   └── masker/           # Log masking
├── schema/                # DDL de ClickHouse
├── charts/               # Helm charts
├── k8s/                  # Kubernetes manifests
├── gitops/               # FluxCD configs
├── scripts/              # Scripts útiles
├── tests/
│   ├── unit/            # Tests unitarios
│   └── integration/     # Tests de integración
├── k6/                  # Load tests
└── monitoring/          # Dashboards y alertas
```

## Scripts Disponibles

```bash
# Backup de ClickHouse
./scripts/backup.sh

# Generar certificados mTLS
./scripts/generate-certs.sh

# Ejecutar tests
go test ./...

# Load test con k6
k6 run k6/load_test.js
```

## Consultas Útiles

Ver `queries.sql` para consultas completas:

```sql
-- Logs por namespace
SELECT namespace, count() FROM default.logs GROUP BY namespace;

-- Errores en la última hora
SELECT * FROM default.logs WHERE level = 'error' 
  AND timestamp > now() - INTERVAL 1 HOUR;

-- Tasa de errores
SELECT namespace, sum(level='error')*100.0/count() as error_rate
FROM default.logs GROUP BY namespace;
```

## Monitoreo

### Prometheus Metrics
- `logs_received_total{status}`
- `logs_inserted_total{status}`
- `batch_size`
- `insert_duration_seconds`
- `log_queue_size`
- `worker_busy{worker_id}`

### Alertas Configuradas
- IngestorDown
- IngestorHighQueueSize
- IngestorHighErrorRate
- IngestorNoLogsReceived
- IngestorHighLatency

## Seguridad

### Log Masking
Patrones detectados y enmascarados:
- Emails
- Direcciones IP
- Credit Cards
- SSN
- AWS Keys
- JWT Tokens
- Passwords
- API Keys
- Authorization headers

### mTLS
```bash
./scripts/generate-certs.sh
```

## Troubleshooting

```bash
# Ver logs del ingestor
kubectl logs -n logging -l app=k8s-ingestor

# Ver métricas
curl http://localhost:8080/metrics

# Health check
curl http://localhost:8080/health

# Ver Fluent Bit
kubectl logs -n logging -l app=fluent-bit
```

## Licencia

MIT
