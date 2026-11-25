-- Recurring Purchase Agent PostgreSQL Schema
-- Durable source of truth for versioned rules, unique schedule occurrences,
-- immutable quotes/proposals/consents, atomic budget reservations, checkout
-- references, notification outbox, durable worker leases, and audit logs.

CREATE TABLE IF NOT EXISTS recurring_tenants (
  tenant_id TEXT PRIMARY KEY,
  state JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
  revision BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recurring_global_controls (
  control_key TEXT PRIMARY KEY,
  paused BOOLEAN NOT NULL DEFAULT FALSE,
  reason TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Relational projection tables for pilot-scale querying, auditing, and
-- database-enforced uniqueness constraints (e.g. one occurrence per rule slot).

CREATE TABLE IF NOT EXISTS recurring_rule_versions (
  tenant_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  status TEXT NOT NULL,
  approval_mode TEXT NOT NULL,
  schedule_kind TEXT NOT NULL,
  timezone TEXT NOT NULL,
  dst_policy TEXT NOT NULL,
  next_run_at_utc TIMESTAMPTZ,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, rule_id, version)
);

CREATE TABLE IF NOT EXISTS schedule_occurrences (
  tenant_id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  slot_key TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  status TEXT NOT NULL,
  payment_status TEXT NOT NULL,
  fulfillment_status TEXT NOT NULL,
  scheduled_for_utc TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, occurrence_id),
  CONSTRAINT uq_occurrence_rule_slot UNIQUE (tenant_id, rule_id, slot_key),
  CONSTRAINT uq_occurrence_idempotency UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_occurrences_rule_status
  ON schedule_occurrences (tenant_id, rule_id, status);

CREATE TABLE IF NOT EXISTS merchant_quotes (
  tenant_id TEXT NOT NULL,
  quote_id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  merchant_id TEXT NOT NULL,
  delivered_total_minor BIGINT NOT NULL,
  currency TEXT NOT NULL,
  digest TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, quote_id)
);

CREATE TABLE IF NOT EXISTS purchase_proposals (
  tenant_id TEXT NOT NULL,
  proposal_id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  quote_id TEXT NOT NULL,
  status TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, proposal_id)
);

CREATE TABLE IF NOT EXISTS consent_records (
  tenant_id TEXT NOT NULL,
  consent_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  proposal_id TEXT,
  revoked_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, consent_id)
);

CREATE TABLE IF NOT EXISTS budget_reservations (
  tenant_id TEXT NOT NULL,
  reservation_id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  budget_period TEXT NOT NULL,
  reserved_minor BIGINT NOT NULL,
  settled_minor BIGINT NOT NULL DEFAULT 0,
  refunded_minor BIGINT NOT NULL DEFAULT 0,
  currency TEXT NOT NULL,
  status TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, reservation_id),
  CONSTRAINT uq_reservation_occurrence UNIQUE (tenant_id, occurrence_id)
);

CREATE TABLE IF NOT EXISTS checkout_references (
  tenant_id TEXT NOT NULL,
  checkout_id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  merchant_order_id TEXT,
  payment_status TEXT NOT NULL,
  fulfillment_status TEXT NOT NULL,
  payload JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, checkout_id),
  CONSTRAINT uq_checkout_occurrence UNIQUE (tenant_id, occurrence_id)
);

CREATE TABLE IF NOT EXISTS notification_events (
  tenant_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  occurrence_id TEXT,
  rule_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  dispatched_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, event_id)
);

CREATE TABLE IF NOT EXISTS worker_jobs (
  job_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  status TEXT NOT NULL,
  lease_owner TEXT,
  lease_until TIMESTAMPTZ,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_worker_job_dedupe UNIQUE (tenant_id, kind, dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_worker_jobs_poll
  ON worker_jobs (status, next_attempt_at, lease_until);

CREATE TABLE IF NOT EXISTS audit_events (
  tenant_id TEXT NOT NULL,
  audit_id TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  detail JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, audit_id)
);
