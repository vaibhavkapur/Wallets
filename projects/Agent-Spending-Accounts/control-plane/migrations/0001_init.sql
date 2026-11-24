-- Agent Spending Accounts control plane schema.
-- Amounts are NUMERIC(39,0) base units; the application validates bounds (<= 2^128-1).

CREATE TABLE organizations (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('active', 'paused')),
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE owner_keys (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations(id),
    key_hash    TEXT NOT NULL UNIQUE,
    label       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);

CREATE TABLE agents (
    id                 TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES organizations(id),
    name               TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    key_hash           TEXT NOT NULL UNIQUE,
    policy_version_id  TEXT,
    created_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ
);
CREATE INDEX agents_org_idx ON agents(org_id);

CREATE TABLE policy_versions (
    id                 TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES organizations(id),
    agent_id           TEXT NOT NULL REFERENCES agents(id),
    version            INTEGER NOT NULL,
    asset              TEXT NOT NULL,
    network            TEXT NOT NULL,
    decimals           INTEGER NOT NULL,
    scheme             TEXT NOT NULL,
    per_request_max    NUMERIC(39,0) NOT NULL CHECK (per_request_max >= 0),
    per_task_max       NUMERIC(39,0) NOT NULL CHECK (per_task_max >= 0),
    agent_daily_max    NUMERIC(39,0) NOT NULL CHECK (agent_daily_max >= 0),
    org_daily_max      NUMERIC(39,0) NOT NULL CHECK (org_daily_max >= 0),
    fee_allowance      NUMERIC(39,0) NOT NULL CHECK (fee_allowance >= 0),
    endpoints          JSONB NOT NULL,
    max_task_seconds   BIGINT NOT NULL,
    grant_ttl_seconds  BIGINT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    UNIQUE (agent_id, version)
);

CREATE TABLE tasks (
    id                 TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES organizations(id),
    agent_id           TEXT NOT NULL REFERENCES agents(id),
    policy_version_id  TEXT NOT NULL REFERENCES policy_versions(id),
    description        TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('active', 'closed')),
    budget             NUMERIC(39,0) NOT NULL CHECK (budget >= 0),
    deadline           TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    closed_at          TIMESTAMPTZ
);
CREATE INDEX tasks_org_agent_idx ON tasks(org_id, agent_id);

CREATE TABLE account_balances (
    org_id      TEXT NOT NULL REFERENCES organizations(id),
    asset       TEXT NOT NULL,
    network     TEXT NOT NULL,
    available   NUMERIC(39,0) NOT NULL CHECK (available >= 0),
    reserved    NUMERIC(39,0) NOT NULL CHECK (reserved >= 0),
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (org_id, asset, network)
);

CREATE TABLE payment_attempts (
    id                 TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES organizations(id),
    agent_id           TEXT NOT NULL REFERENCES agents(id),
    task_id            TEXT NOT NULL REFERENCES tasks(id),
    policy_version_id  TEXT NOT NULL REFERENCES policy_versions(id),
    reservation_id     TEXT,
    grant_id           TEXT,
    idempotency_key    TEXT NOT NULL,
    method             TEXT NOT NULL,
    url                TEXT NOT NULL,
    quote              JSONB,
    status             TEXT NOT NULL CHECK (status IN
        ('initiated','rejected','authorized','submitted','settled','failed','unknown','expired')),
    reject_reason      TEXT NOT NULL DEFAULT '',
    submission_ref     TEXT NOT NULL DEFAULT '',
    payment_hash       TEXT NOT NULL DEFAULT '',
    payer_ref          TEXT NOT NULL DEFAULT '',
    settled_amount     NUMERIC(39,0) NOT NULL DEFAULT 0,
    fee_actual         NUMERIC(39,0) NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    UNIQUE (task_id, idempotency_key)
);
CREATE INDEX payment_attempts_status_idx ON payment_attempts(status, updated_at);
CREATE INDEX payment_attempts_org_idx ON payment_attempts(org_id, created_at DESC);

CREATE TABLE reservations (
    id                  TEXT PRIMARY KEY,
    org_id              TEXT NOT NULL REFERENCES organizations(id),
    agent_id            TEXT NOT NULL REFERENCES agents(id),
    task_id             TEXT NOT NULL REFERENCES tasks(id),
    policy_version_id   TEXT NOT NULL REFERENCES policy_versions(id),
    payment_attempt_id  TEXT NOT NULL UNIQUE REFERENCES payment_attempts(id),
    amount              NUMERIC(39,0) NOT NULL CHECK (amount >= 0),
    fee_allowance       NUMERIC(39,0) NOT NULL CHECK (fee_allowance >= 0),
    status              TEXT NOT NULL CHECK (status IN ('held','committed','released')),
    committed_amount    NUMERIC(39,0) NOT NULL DEFAULT 0,
    committed_fee       NUMERIC(39,0) NOT NULL DEFAULT 0,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    resolved_at         TIMESTAMPTZ,
    resolution_reason   TEXT NOT NULL DEFAULT ''
);
-- Daily usage queries: held + committed per org/agent/task within a UTC day.
CREATE INDEX reservations_org_day_idx ON reservations(org_id, created_at);
CREATE INDEX reservations_agent_day_idx ON reservations(agent_id, created_at);
CREATE INDEX reservations_task_idx ON reservations(task_id);

CREATE TABLE execution_grants (
    id                  TEXT PRIMARY KEY,
    reservation_id      TEXT NOT NULL REFERENCES reservations(id),
    payment_attempt_id  TEXT NOT NULL REFERENCES payment_attempts(id),
    expires_at          TIMESTAMPTZ NOT NULL,
    issued_at           TIMESTAMPTZ NOT NULL,
    consumed_at         TIMESTAMPTZ
);

CREATE TABLE receipts (
    id                  TEXT PRIMARY KEY,
    org_id              TEXT NOT NULL REFERENCES organizations(id),
    task_id             TEXT NOT NULL REFERENCES tasks(id),
    agent_id            TEXT NOT NULL REFERENCES agents(id),
    payment_attempt_id  TEXT NOT NULL UNIQUE REFERENCES payment_attempts(id),
    reservation_id      TEXT NOT NULL,
    policy_version_id   TEXT NOT NULL,
    resource            TEXT NOT NULL,
    quote               JSONB NOT NULL,
    payment_status      TEXT NOT NULL,
    submission_ref      TEXT NOT NULL DEFAULT '',
    settled_amount      NUMERIC(39,0) NOT NULL DEFAULT 0,
    fee_actual          NUMERIC(39,0) NOT NULL DEFAULT 0,
    service             JSONB,
    created_at          TIMESTAMPTZ NOT NULL
);
CREATE INDEX receipts_task_idx ON receipts(task_id, created_at);

CREATE TABLE ledger_entries (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations(id),
    journal_id  TEXT NOT NULL,
    account     TEXT NOT NULL,
    direction   TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount      NUMERIC(39,0) NOT NULL CHECK (amount >= 0),
    asset       TEXT NOT NULL,
    network     TEXT NOT NULL,
    memo        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX ledger_org_idx ON ledger_entries(org_id, created_at DESC);
CREATE INDEX ledger_journal_idx ON ledger_entries(journal_id);

CREATE TABLE audit_events (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL,
    actor         TEXT NOT NULL,
    action        TEXT NOT NULL,
    subject_type  TEXT NOT NULL,
    subject_id    TEXT NOT NULL,
    details       JSONB,
    created_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX audit_org_idx ON audit_events(org_id, created_at DESC);

CREATE TABLE outbox (
    id            TEXT PRIMARY KEY,
    topic         TEXT NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    published_at  TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox(created_at) WHERE published_at IS NULL;
