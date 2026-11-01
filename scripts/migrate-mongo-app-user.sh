#!/usr/bin/env bash
# ==============================================================================
# Safe MongoDB Application User Migration Script for Existing Volumes
# ==============================================================================
# This script provisions or updates the dedicated least-privilege MongoDB
# application user without dropping databases, recreating containers, or deleting
# existing data volumes.
#
# Usage:
#   chmod +x scripts/migrate-mongo-app-user.sh
#   ./scripts/migrate-mongo-app-user.sh
# ==============================================================================

set -euo pipefail

if [ -f .env ]; then
  # shellcheck disable=SC1091
  set -a && source .env && set +a
fi

MONGO_ROOT_USER="${MONGO_ROOT_USERNAME:-}"
MONGO_ROOT_PASS="${MONGO_ROOT_PASSWORD:-}"
MONGO_APP_USER="${MONGO_APP_USERNAME:-session_app_user}"
MONGO_APP_PASS="${MONGO_APP_PASSWORD:-}"
MONGO_DB="${MONGODB_DATABASE:-telegram_session_bot}"

if [ -z "$MONGO_ROOT_USER" ] || [ -z "$MONGO_ROOT_PASS" ]; then
  echo "[-] ERROR: MONGO_ROOT_USERNAME and MONGO_ROOT_PASSWORD must be set."
  exit 1
fi

if [ -z "$MONGO_APP_PASS" ]; then
  echo "[-] ERROR: MONGO_APP_PASSWORD must be provided for dedicated user setup."
  exit 1
fi

echo "[*] Connecting to MongoDB container to configure dedicated user '$MONGO_APP_USER'..."

docker compose exec -e MONGO_APP_USERNAME="$MONGO_APP_USER" \
  -e MONGO_APP_PASSWORD="$MONGO_APP_PASS" \
  -e MONGODB_DATABASE="$MONGO_DB" \
  mongo mongosh -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASS" \
  --authenticationDatabase admin \
  /docker-entrypoint-initdb.d/init-app-user.js

echo "[+] Migration completed successfully. Existing volumes and user data preserved."
