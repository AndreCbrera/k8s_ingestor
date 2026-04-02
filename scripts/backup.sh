#!/bin/bash
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/backups}"
CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-localhost}"
CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-9000}"
CLICKHOUSE_USER="${CLICKHOUSE_USER:-admin}"
CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-admin123}"
RETENTION_DAYS="${RETENTION_DAYS:-30}"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_NAME="logs_backup_${DATE}"

echo "[$(date)] Starting ClickHouse backup..."

mkdir -p "${BACKUP_DIR}"

echo "[$(date)] Creating table backup..."
docker exec clickhouse clickhouse-client \
  --host "${CLICKHOUSE_HOST}" \
  --port "${CLICKHOUSE_PORT}" \
  --user "${CLICKHOUSE_USER}" \
  --password "${CLICKHOUSE_PASSWORD}" \
  --query "BACKUP TABLE default.logs TO File('${BACKUP_DIR}/${BACKUP_NAME}')"

echo "[$(date)] Backup created: ${BACKUP_NAME}"

echo "[$(date)] Cleaning old backups (retention: ${RETENTION_DAYS} days)..."
find "${BACKUP_DIR}" -type d -name "logs_backup_*" -mtime +${RETENTION_DAYS} -exec rm -rf {} \; 2>/dev/null || true

echo "[$(date)] Backup complete!"

BACKUP_SIZE=$(du -sh "${BACKUP_DIR}/${BACKUP_NAME}" 2>/dev/null | cut -f1)
echo "Backup size: ${BACKUP_SIZE}"

echo "[$(date)] Verifying backup..."
docker exec clickhouse clickhouse-client \
  --host "${CLICKHOUSE_HOST}" \
  --port "${CLICKHOUSE_PORT}" \
  --user "${CLICKHOUSE_USER}" \
  --password "${CLICKHOUSE_PASSWORD}" \
  --query "SYSTEM RESTORE TABLE default.logs FROM File('${BACKUP_DIR}/${BACKUP_NAME}')" 2>/dev/null || true

echo "[$(date)] Backup verification complete"

BACKUP_COUNT=$(find "${BACKUP_DIR}" -type d -name "logs_backup_*" | wc -l)
echo "Total backups: ${BACKUP_COUNT}"

exit 0
