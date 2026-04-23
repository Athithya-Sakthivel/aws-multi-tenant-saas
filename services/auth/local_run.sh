#!/usr/bin/env bash
# bash src/scripts/dev/ssm_sync.sh
# kubectl port-forward svc/postgres-pooler 5432:5432
# bash services/auth/local_run.sh
# cd services/auth && go test ./tests -v
#!/usr/bin/env bash
set -euo pipefail

ROOT="/workspace"
AUTH_DIR="$ROOT/services/auth"
BIN="$ROOT/bin/auth-server"

export LC_ALL=C
export LANG=C

SSM_DSN_PARAM=${SSM_DSN_PARAM:-/postgres/dev/postgres-cluster/dsn}
SSM_JWT_SECRET_PARAM=${SSM_JWT_SECRET_PARAM:-/auth/dev/jwt-secret}
PGHOST_OVERRIDE=${PGHOST_OVERRIDE:-localhost}
TENANT=${TENANT:-tenant1}

export SSM_DSN_PARAM
export SSM_JWT_SECRET_PARAM
export PGHOST_OVERRIDE

echo "[1] build auth"
cd "$AUTH_DIR"
go mod tidy
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -buildvcs=false \
  -ldflags="-s -w -buildid=" \
  -o "$BIN" ./cmd/server

echo "[2] wait for DB (port-forward expected)"
# do NOT fetch secrets here for app, only for readiness check
LOCAL_DSN="$(aws ssm get-parameter \
  --name "$SSM_DSN_PARAM" \
  --with-decryption \
  --query Parameter.Value \
  --output text | sed -E "s/@[^:]+:/@${PGHOST_OVERRIDE}:/")"

for i in $(seq 1 30); do
  if psql "$LOCAL_DSN" -c "select 1" >/dev/null 2>&1; then
    echo "DB ready"
    break
  fi
  sleep 1
done

if ! psql "$LOCAL_DSN" -c "select 1" >/dev/null 2>&1; then
  echo "FAIL: DB not reachable"
  exit 1
fi

echo "[3] migrate global (idempotent)"
"$BIN" migrate

echo "[4] ensure tenant schema (idempotent)"
psql "$LOCAL_DSN" -c "CREATE SCHEMA IF NOT EXISTS \"$TENANT\";"

echo "[5] migrate tenant (idempotent)"
psql "$LOCAL_DSN" <<SQL
SET search_path TO "$TENANT";
\i $AUTH_DIR/internal/migrations/00001_init.sql
SQL

echo "[6] start server (blocking)"
exec "$BIN"
