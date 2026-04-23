-- orders
CREATE TABLE IF NOT EXISTS orders (
    id              UUID PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    amount          BIGINT NOT NULL,
    status          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- idempotency
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    response_code   INT NOT NULL,
    response_body   JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- outbox
CREATE TABLE IF NOT EXISTS outbox (
    id              UUID PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed       BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS idx_outbox_unprocessed ON outbox(processed);
