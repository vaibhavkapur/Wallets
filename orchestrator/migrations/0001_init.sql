-- Card-versus-Stablecoin Payment Optimizer schema.
-- PostgreSQL is the durable source of truth. Snapshot tables are append-only:
-- a trigger rejects UPDATE and DELETE so a stored evaluation can always be
-- reproduced from the rows it references.

CREATE FUNCTION forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TABLE tenants (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE api_keys (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    key_hash    TEXT NOT NULL UNIQUE,
    scopes      TEXT[] NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);

-- Product-level pause. scope is 'global' or a tenant id.
CREATE TABLE product_controls (
    scope       TEXT PRIMARY KEY,
    paused      BOOLEAN NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE preference_policies (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    customer_id  TEXT NOT NULL,
    version      INTEGER NOT NULL CHECK (version > 0),
    content      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, customer_id, version)
);
CREATE TRIGGER preference_policies_immutable BEFORE UPDATE OR DELETE ON preference_policies
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE basket_snapshots (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    merchant_id  TEXT NOT NULL,
    basket_ref   TEXT NOT NULL,
    basket_hash  TEXT NOT NULL,
    content      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER basket_snapshots_immutable BEFORE UPDATE OR DELETE ON basket_snapshots
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- A reward rule version never changes meaning: (rule_id, rule_version) maps to
-- exactly one content hash per tenant.
CREATE TABLE reward_rule_versions (
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    rule_id        TEXT NOT NULL,
    rule_version   TEXT NOT NULL,
    content_hash   TEXT NOT NULL,
    content        JSONB NOT NULL,
    first_seen_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, rule_id, rule_version)
);
CREATE TRIGGER reward_rule_versions_immutable BEFORE UPDATE OR DELETE ON reward_rule_versions
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE evaluations (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    customer_id           TEXT NOT NULL,
    merchant_id           TEXT NOT NULL,
    basket_snapshot_id    TEXT NOT NULL REFERENCES basket_snapshots(id),
    basket_hash           TEXT NOT NULL,
    mode                  TEXT NOT NULL CHECK (mode IN ('shadow', 'assisted')),
    preference_policy_id  TEXT REFERENCES preference_policies(id),
    refreshed_from        TEXT REFERENCES evaluations(id),
    idempotency_key       TEXT,
    request_fingerprint   TEXT NOT NULL,
    -- The exact bytes sent to the engine, and their SHA-256 as confirmed by it.
    engine_request        TEXT NOT NULL,
    input_sha256          TEXT NOT NULL,
    engine_result         JSONB NOT NULL,
    contract_version      TEXT NOT NULL,
    engine_version        TEXT NOT NULL,
    formula_version       TEXT NOT NULL,
    rounding_version      TEXT NOT NULL,
    policy_version        TEXT NOT NULL,
    preference_version    TEXT NOT NULL,
    outcome               TEXT NOT NULL,
    preferred_option_id   TEXT,
    baseline_option_id    TEXT,
    warnings              TEXT[] NOT NULL DEFAULT '{}',
    attribution           JSONB,
    evaluated_at          TIMESTAMPTZ NOT NULL,
    expires_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX evaluations_tenant_created_idx ON evaluations(tenant_id, created_at);
CREATE TRIGGER evaluations_immutable BEFORE UPDATE OR DELETE ON evaluations
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE rail_quotes (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    evaluation_id  TEXT NOT NULL REFERENCES evaluations(id),
    option_id      TEXT NOT NULL,
    rail           TEXT NOT NULL,
    provider       TEXT NOT NULL,
    quote_id       TEXT NOT NULL,
    obtained_at    TIMESTAMPTZ NOT NULL,
    valid_until    TIMESTAMPTZ NOT NULL,
    content        JSONB NOT NULL,
    UNIQUE (evaluation_id, option_id)
);
CREATE TRIGGER rail_quotes_immutable BEFORE UPDATE OR DELETE ON rail_quotes
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- A handoff is a constrained reference for one approved payment. It is not a
-- payment credential. Its status is the only mutable field.
CREATE TABLE execution_handoffs (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    evaluation_id        TEXT NOT NULL REFERENCES evaluations(id),
    customer_id          TEXT NOT NULL,
    merchant_id          TEXT NOT NULL,
    basket_hash          TEXT NOT NULL,
    option_id            TEXT NOT NULL,
    rail                 TEXT NOT NULL,
    quote_id             TEXT NOT NULL,
    amount               JSONB NOT NULL,
    approval             JSONB NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('created', 'succeeded', 'failed', 'unknown')),
    idempotency_key      TEXT NOT NULL,
    request_fingerprint  TEXT NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);
-- At most one unresolved payment attempt per customer and basket. A second
-- attempt, on any rail, must wait until the first is reconciled.
CREATE UNIQUE INDEX execution_handoffs_one_unresolved
    ON execution_handoffs (tenant_id, customer_id, basket_hash)
    WHERE status IN ('created', 'unknown');
CREATE INDEX execution_handoffs_evaluation_idx ON execution_handoffs(evaluation_id);

CREATE TABLE observed_outcomes (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    evaluation_id        TEXT NOT NULL REFERENCES evaluations(id),
    handoff_id           TEXT REFERENCES execution_handoffs(id),
    source               TEXT NOT NULL CHECK (source IN ('provider', 'executor', 'customer_report')),
    event_id             TEXT NOT NULL,
    type                 TEXT NOT NULL CHECK (type IN ('payment', 'refund')),
    status               TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'unknown')),
    option_id            TEXT NOT NULL,
    content              JSONB NOT NULL,
    derived              JSONB NOT NULL,
    payload_fingerprint  TEXT NOT NULL,
    occurred_at          TIMESTAMPTZ NOT NULL,
    recorded_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, source, event_id)
);
CREATE INDEX observed_outcomes_evaluation_idx ON observed_outcomes(evaluation_id);
CREATE TRIGGER observed_outcomes_immutable BEFORE UPDATE OR DELETE ON observed_outcomes
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- Transactional outbox: rows are written in the same transaction as the state
-- change they announce and delivered afterwards by the relay.
CREATE TABLE outbox (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    topic          TEXT NOT NULL,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    available_at   TIMESTAMPTZ NOT NULL,
    claimed_until  TIMESTAMPTZ,
    attempts       INTEGER NOT NULL DEFAULT 0,
    delivered_at   TIMESTAMPTZ
);
CREATE INDEX outbox_pending_idx ON outbox(available_at) WHERE delivered_at IS NULL;
