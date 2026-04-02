#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

cleanup() {
    log "Deteniendo servicios..."
    kill $PIPELINE_PID $QUERY_PID $INGESTOR_PID 2>/dev/null || true
    exit 0
}

trap cleanup SIGINT SIGTERM

echo "=========================================="
echo "  K8s Log Ingestor - Local Setup"
echo "=========================================="
echo ""

# Verificar que los binarios existen
log "Verificando binarios..."

for binary in k8s-ingestor query-service pipeline-service; do
    if [ ! -f "./$binary" ]; then
        warn "Binario $binary no encontrado, compilando..."
        go build -o "$binary" $([ "$binary" = "k8s-ingestor" ] && echo "." || echo "./cmd/$binary")
    fi
done

# Verificar que ClickHouse esté disponible
CLICKHOUSE_ADDR=${CLICKHOUSE_ADDR:-localhost:9000}
log "Verificando ClickHouse en $CLICKHOUSE_ADDR..."

if ! timeout 2 bash -c "echo > /dev/tcp/${CLICKHOUSE_ADDR%:*}/${CLICKHOUSE_ADDR#*:}" 2>/dev/null; then
    warn "ClickHouse no está disponible en $CLICKHOUSE_ADDR"
    warn "Los servicios query e ingestor no funcionarán sin ClickHouse"
    warn "Puedes iniciar solo el pipeline-service con: ./scripts/run-local.sh --pipeline-only"
    
    if [ "$1" != "--pipeline-only" ]; then
        read -p "¿Iniciar solo pipeline-service? (y/n) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 0
        fi
    fi
fi

# Crear directorio de datos si no existe
mkdir -p data

# Iniciar Pipeline Service
log "Iniciando Pipeline Service (puerto 8082)..."
./pipeline-service &
PIPELINE_PID=$!
echo "Pipeline Service: PID $PIPELINE_PID"

sleep 1

# Iniciar Query Service
log "Iniciando Query Service (puerto 8081)..."
export CLICKHOUSE_ADDR="${CLICKHOUSE_ADDR}"
export CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-admin123}"
./query-service &
QUERY_PID=$!
echo "Query Service: PID $QUERY_PID"

sleep 1

# Iniciar Ingestor
log "Iniciando K8s Ingestor (puerto 8080)..."
export CLICKHOUSE_ADDR="${CLICKHOUSE_ADDR}"
export CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-admin123}"
export CLUSTER_NAME="${CLUSTER_NAME:-local}"
./k8s-ingestor &
INGESTOR_PID=$!
echo "K8s Ingestor: PID $INGESTOR_PID"

sleep 2

echo ""
echo "=========================================="
echo "  Servicios iniciados"
echo "=========================================="
echo ""
echo -e "  ${GREEN}Pipeline Service${NC}: http://localhost:8082"
echo "    - UI Web para configurar pipelines"
echo ""
echo -e "  ${GREEN}Query Service${NC}:   http://localhost:8081"
echo "    - API para consultar logs"
echo ""
echo -e "  ${GREEN}K8s Ingestor${NC}:    http://localhost:8080"
echo "    - Recibe logs de Fluent Bit"
echo ""
echo "=========================================="
echo ""
log "Presiona Ctrl+C para detener todos los servicios"

# Verificar que los servicios están corriendo
sleep 2

if curl -s http://localhost:8082/health > /dev/null 2>&1; then
    log "Pipeline Service está corriendo"
else
    warn "Pipeline Service no responde"
fi

if curl -s http://localhost:8081/health > /dev/null 2>&1; then
    log "Query Service está corriendo"
else
    warn "Query Service no responde"
fi

if curl -s http://localhost:8080/health > /dev/null 2>&1; then
    log "K8s Ingestor está corriendo"
else
    warn "K8s Ingestor no responde"
fi

echo ""
log "Abrir UI en navegador: http://localhost:8082/"
echo ""

# Esperar
wait
