#!/usr/bin/env bash
set -euo pipefail

SECRET_NAME=${1:-postgres-cluster-app}

APP=${APP:-postgres}
ENV=${ENV:-dev}
CLUSTER=${CLUSTER:-postgres-cluster}

PG_HOST=${PG_HOST:-postgres-pooler.default}
PG_PORT=${PG_PORT:-5432}

JWT_PARAM=${JWT_PARAM:-/auth/$ENV/jwt-secret}

echo "[1] Fetching K8s secret: $SECRET_NAME"

PG_USER=$(kubectl get secret "$SECRET_NAME" -o jsonpath='{.data.username}' | base64 -d)
PG_PASS=$(kubectl get secret "$SECRET_NAME" -o jsonpath='{.data.password}' | base64 -d)
PG_DB=$(kubectl get secret "$SECRET_NAME" -o jsonpath='{.data.dbname}' | base64 -d)

echo "[2] Construct DSN"

DSN="postgresql://${PG_USER}:${PG_PASS}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable"

DSN_PARAM="/$APP/$ENV/$CLUSTER/dsn"

echo "[3] Write DSN → SSM: $DSN_PARAM"

aws ssm put-parameter \
  --name "$DSN_PARAM" \
  --value "$DSN" \
  --type SecureString \
  --overwrite

echo "[4] Ensure JWT secret exists"

if ! aws ssm get-parameter --name "$JWT_PARAM" >/dev/null 2>&1; then
  echo "[INFO] generating JWT secret"

  JWT_SECRET=$(openssl rand -base64 48)

  aws ssm put-parameter \
    --name "$JWT_PARAM" \
    --value "$JWT_SECRET" \
    --type SecureString

  echo "[INFO] JWT secret created at $JWT_PARAM"
else
  echo "[INFO] JWT secret already exists: $JWT_PARAM"
fi

echo "[SUCCESS] SSM sync complete"
