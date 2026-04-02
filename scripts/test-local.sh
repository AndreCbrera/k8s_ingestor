#!/bin/bash

# Script para probar los servicios localmente
# Uso: ./scripts/test-local.sh

BASE_URL="${1:-http://localhost:8082}"
QUERY_URL="${2:-http://localhost:8081}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓${NC} $1"; }
fail() { echo -e "${RED}✗${NC} $1"; }
info() { echo -e "${YELLOW}→${NC} $1"; }

PIPELINE_ID=""

echo "=========================================="
echo "  Testing K8s Log Ingestor Services"
echo "=========================================="
echo ""

# Test Pipeline Service
echo "--- Pipeline Service ---"
echo ""

info "GET /health"
if curl -s "$BASE_URL/health" | grep -q '"status":"healthy"'; then
    pass "Health check passed"
else
    fail "Health check failed"
fi
echo ""

info "GET /api/pipelines"
result=$(curl -s "$BASE_URL/api/pipelines")
if echo "$result" | grep -q '"success"'; then
    pass "List pipelines"
    echo "  Response: $result"
else
    fail "List pipelines"
fi
echo ""

info "POST /api/pipelines/create"
result=$(curl -s -X POST "$BASE_URL/api/pipelines/create" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "Test Pipeline",
        "description": "Pipeline de prueba",
        "namespaces": [
            {"name": "production", "include": true},
            {"name": "staging", "include": true}
        ],
        "levels": ["error", "warn", "info"],
        "enabled": true
    }')

if echo "$result" | grep -q '"success":true'; then
    pass "Create pipeline"
    PIPELINE_ID=$(echo "$result" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    echo "  Created pipeline ID: $PIPELINE_ID"
else
    fail "Create pipeline"
fi
echo ""

if [ -n "$PIPELINE_ID" ]; then
    info "GET /api/pipelines/config (Lua filter)"
    config=$(curl -s "$BASE_URL/api/pipelines/config")
    if echo "$config" | grep -q "filter_by_namespace"; then
        pass "Generate Fluent Bit config"
    else
        fail "Generate Fluent Bit config"
    fi
    echo ""

    info "POST /api/pipelines/update"
    result=$(curl -s -X POST "$BASE_URL/api/pipelines/update?id=$PIPELINE_ID" \
        -H "Content-Type: application/json" \
        -d '{"description": "Pipeline actualizado"}')
    if echo "$result" | grep -q '"success":true'; then
        pass "Update pipeline"
    else
        fail "Update pipeline"
    fi
    echo ""

    info "POST /api/pipelines/delete"
    result=$(curl -s -X POST "$BASE_URL/api/pipelines/delete?id=$PIPELINE_ID")
    if echo "$result" | grep -q '"success":true'; then
        pass "Delete pipeline"
    else
        fail "Delete pipeline"
    fi
    echo ""
fi

# Test Query Service
echo "--- Query Service ---"
echo ""

info "GET /health"
if curl -s "$QUERY_URL/health" | grep -q '"status"'; then
    pass "Health check (si ClickHouse está disponible)"
else
    warn "Query service no disponible o ClickHouse caido"
fi
echo ""

info "GET /logs/levels"
result=$(curl -s "$QUERY_URL/logs/levels")
if echo "$result" | grep -q '"success"'; then
    pass "Get log levels"
else
    warn "Get log levels (puede fallar sin ClickHouse)"
fi
echo ""

info "GET /logs/namespaces"
result=$(curl -s "$QUERY_URL/logs/namespaces")
if echo "$result" | grep -q '"success"'; then
    pass "Get namespaces"
else
    warn "Get namespaces (puede fallar sin ClickHouse)"
fi
echo ""

# Test Ingestor (si está disponible)
echo "--- K8s Ingestor ---"
echo ""

info "GET /health (puerto 8080)"
if curl -s http://localhost:8080/health 2>/dev/null | grep -q '"status"'; then
    pass "Health check"
else
    warn "Ingestor no disponible o ClickHouse caido"
fi
echo ""

info "POST /logs (simular Fluent Bit)"
result=$(curl -s -X POST http://localhost:8080/logs \
    -H "Content-Type: application/json" \
    -d '[{
        "log": "2024-01-01 12:00:00 ERROR test message",
        "time": "2024-01-01T12:00:00Z",
        "kubernetes": {
            "namespace_name": "production",
            "pod_name": "test-pod-abc123",
            "container_name": "test-container"
        }
    }]')

if echo "$result" | grep -q '"success"'; then
    pass "Send log"
else
    warn "Send log (puede fallar sin ClickHouse)"
fi
echo ""

echo "=========================================="
echo "  Tests completados"
echo "=========================================="
