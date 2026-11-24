-- Merchant-funded Stablecoin Rewards PostgreSQL schema.
-- All monetary quantities use NUMERIC(39,0) integer minor/base units with explicit asset,
-- network, and decimals. Never store floating-point balances.

CREATE TABLE IF NOT EXISTS rewards_tenant_state (
    tenant_id   TEXT PRIMARY KEY,
    state       JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
    revision    BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Normalized relational tables for analytical queries, SQL-level constraints,
-- and production scale migration path alongside transactional state snapshots.

CREATE TABLE IF NOT EXISTS merchants (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL,
    name                TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('active', 'paused', 'suspended')),
    webhook_secret_hash TEXT NOT NULL,
    settlement_currency TEXT NOT NULL DEFAULT 'USD',
    terms_accepted_at   TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS merchants_tenant_idx ON merchants(tenant_id);

CREATE TABLE IF NOT EXISTS campaign_versions (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL,
    campaign_id             TEXT NOT NULL,
    merchant_id             TEXT NOT NULL REFERENCES merchants(id),
    version                 INTEGER NOT NULL CHECK (version > 0),
    name                    TEXT NOT NULL,
    status                  TEXT NOT NULL CHECK (status IN ('draft', 'active', 'paused', 'ended')),
    reward_type             TEXT NOT NULL CHECK (reward_type IN ('fixed', 'percentage')),
    fixed_reward_minor      NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (fixed_reward_minor >= 0),
    percentage_bps          INTEGER NOT NULL DEFAULT 0 CHECK (percentage_bps >= 0 AND percentage_bps <= 10000),
    currency                TEXT NOT NULL DEFAULT 'USD',
    decimals                INTEGER NOT NULL DEFAULT 2,
    budget_cap_minor        NUMERIC(39,0) NOT NULL CHECK (budget_cap_minor > 0),
    customer_cap_minor      NUMERIC(39,0) NOT NULL CHECK (customer_cap_minor > 0),
    per_order_cap_minor     NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (per_order_cap_minor >= 0),
    min_order_minor         NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (min_order_minor >= 0),
    eligible_skus           JSONB NOT NULL DEFAULT '[]'::jsonb,
    return_window_seconds   BIGINT NOT NULL CHECK (return_window_seconds >= 0),
    return_treatment        TEXT NOT NULL CHECK (return_treatment IN ('pro_rata', 'full_reversal')),
    platform_fee_bps        INTEGER NOT NULL DEFAULT 0 CHECK (platform_fee_bps >= 0 AND platform_fee_bps <= 5000),
    loss_reserve_bps        INTEGER NOT NULL DEFAULT 0 CHECK (loss_reserve_bps >= 0 AND loss_reserve_bps <= 5000),
    starts_at               TIMESTAMPTZ NOT NULL,
    ends_at                 TIMESTAMPTZ NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL,
    CHECK (ends_at > starts_at),
    UNIQUE (tenant_id, campaign_id, version)
);
CREATE INDEX IF NOT EXISTS campaign_versions_lookup_idx ON campaign_versions(tenant_id, campaign_id, version DESC);

CREATE TABLE IF NOT EXISTS funding_events (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    merchant_id       TEXT NOT NULL REFERENCES merchants(id),
    campaign_id       TEXT NOT NULL,
    provider_ref      TEXT NOT NULL,
    asset             TEXT NOT NULL,
    network           TEXT NOT NULL DEFAULT 'fiat',
    decimals          INTEGER NOT NULL DEFAULT 2,
    amount_base       NUMERIC(39,0) NOT NULL CHECK (amount_base > 0),
    status            TEXT NOT NULL CHECK (status IN ('pending', 'reconciled', 'rejected')),
    reconciled_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, provider_ref)
);

CREATE TABLE IF NOT EXISTS purchase_attributions (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL,
    merchant_id           TEXT NOT NULL REFERENCES merchants(id),
    campaign_id           TEXT NOT NULL,
    campaign_version_id   TEXT NOT NULL REFERENCES campaign_versions(id),
    event_id              TEXT NOT NULL,
    order_id              TEXT NOT NULL,
    customer_id           TEXT NOT NULL,
    order_subtotal_minor  NUMERIC(39,0) NOT NULL CHECK (order_subtotal_minor >= 0),
    eligible_minor        NUMERIC(39,0) NOT NULL CHECK (eligible_minor >= 0),
    refunded_minor        NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0),
    currency              TEXT NOT NULL DEFAULT 'USD',
    payment_settled       BOOLEAN NOT NULL DEFAULT FALSE,
    fulfilled             BOOLEAN NOT NULL DEFAULT FALSE,
    fulfilled_at          TIMESTAMPTZ,
    items                 JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, merchant_id, event_id),
    UNIQUE (tenant_id, merchant_id, order_id)
);

CREATE TABLE IF NOT EXISTS campaign_reservations (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL,
    campaign_id           TEXT NOT NULL,
    campaign_version_id   TEXT NOT NULL REFERENCES campaign_versions(id),
    attribution_id        TEXT NOT NULL REFERENCES purchase_attributions(id),
    reward_minor          NUMERIC(39,0) NOT NULL CHECK (reward_minor >= 0),
    platform_fee_minor    NUMERIC(39,0) NOT NULL CHECK (platform_fee_minor >= 0),
    loss_reserve_minor    NUMERIC(39,0) NOT NULL CHECK (loss_reserve_minor >= 0),
    released_minor        NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (released_minor >= 0),
    status                TEXT NOT NULL CHECK (status IN ('reserved', 'partially_released', 'converted', 'released')),
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS reward_accruals (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL,
    customer_id             TEXT NOT NULL,
    merchant_id             TEXT NOT NULL REFERENCES merchants(id),
    campaign_id             TEXT NOT NULL,
    campaign_version_id     TEXT NOT NULL REFERENCES campaign_versions(id),
    attribution_id          TEXT NOT NULL REFERENCES purchase_attributions(id),
    reservation_id          TEXT NOT NULL REFERENCES campaign_reservations(id),
    order_id                TEXT NOT NULL,
    asset                   TEXT NOT NULL DEFAULT 'USD',
    decimals                INTEGER NOT NULL DEFAULT 2,
    original_amount_minor   NUMERIC(39,0) NOT NULL CHECK (original_amount_minor > 0),
    reversed_amount_minor   NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (reversed_amount_minor >= 0),
    net_amount_minor        NUMERIC(39,0) NOT NULL CHECK (net_amount_minor >= 0),
    platform_fee_minor      NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (platform_fee_minor >= 0),
    loss_reserve_minor      NUMERIC(39,0) NOT NULL DEFAULT 0 CHECK (loss_reserve_minor >= 0),
    state                   TEXT NOT NULL CHECK (state IN ('pending', 'eligible', 'payout_requested', 'paid', 'reversed')),
    eligible_at             TIMESTAMPTZ,
    payout_id               TEXT,
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS reward_accruals_customer_idx ON reward_accruals(tenant_id, customer_id, state);

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    code        TEXT NOT NULL,
    category    TEXT NOT NULL CHECK (category IN ('asset', 'liability', 'equity', 'revenue', 'expense', 'clearing')),
    scope_id    TEXT NOT NULL DEFAULT '',
    asset       TEXT NOT NULL,
    network     TEXT NOT NULL DEFAULT 'fiat',
    decimals    INTEGER NOT NULL DEFAULT 2,
    created_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, code, scope_id, asset, network)
);

CREATE TABLE IF NOT EXISTS journal_entries (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    entry_type      TEXT NOT NULL,
    reference_id    TEXT NOT NULL,
    description     TEXT NOT NULL,
    postings        JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS beneficiary_versions (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL,
    customer_id         TEXT NOT NULL,
    version             INTEGER NOT NULL CHECK (version > 0),
    asset               TEXT NOT NULL,
    network             TEXT NOT NULL,
    decimals            INTEGER NOT NULL,
    address             TEXT NOT NULL,
    verification_status TEXT NOT NULL CHECK (verification_status IN ('pending', 'verified', 'revoked')),
    recovery_disclosed  BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at         TIMESTAMPTZ,
    revoked_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, customer_id, version)
);

CREATE TABLE IF NOT EXISTS payout_quotes (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL,
    customer_id             TEXT NOT NULL,
    beneficiary_version_id  TEXT NOT NULL REFERENCES beneficiary_versions(id),
    reward_ids              JSONB NOT NULL,
    source_asset            TEXT NOT NULL,
    source_decimals         INTEGER NOT NULL,
    source_amount_base      NUMERIC(39,0) NOT NULL CHECK (source_amount_base > 0),
    payout_asset            TEXT NOT NULL,
    payout_network          TEXT NOT NULL,
    payout_decimals         INTEGER NOT NULL,
    gross_payout_base       NUMERIC(39,0) NOT NULL CHECK (gross_payout_base > 0),
    fee_base                NUMERIC(39,0) NOT NULL CHECK (fee_base >= 0),
    net_payout_base         NUMERIC(39,0) NOT NULL CHECK (net_payout_base > 0),
    fee_bearer              TEXT NOT NULL CHECK (fee_bearer IN ('customer', 'platform', 'merchant')),
    fx_rate_bps             INTEGER NOT NULL CHECK (fx_rate_bps > 0),
    expires_at              TIMESTAMPTZ NOT NULL,
    consumed_at             TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS payout_attempts (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL,
    logical_payout_id       TEXT NOT NULL,
    attempt_number          INTEGER NOT NULL CHECK (attempt_number > 0),
    customer_id             TEXT NOT NULL,
    quote_id                TEXT NOT NULL REFERENCES payout_quotes(id),
    beneficiary_version_id  TEXT NOT NULL REFERENCES beneficiary_versions(id),
    destination_address     TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    source_amount_minor     NUMERIC(39,0) NOT NULL CHECK (source_amount_minor > 0),
    payout_asset            TEXT NOT NULL,
    payout_network          TEXT NOT NULL,
    payout_decimals         INTEGER NOT NULL,
    net_payout_base         NUMERIC(39,0) NOT NULL CHECK (net_payout_base > 0),
    fee_base                NUMERIC(39,0) NOT NULL CHECK (fee_base >= 0),
    status                  TEXT NOT NULL CHECK (status IN ('prepared', 'submitted', 'pending', 'succeeded', 'failed', 'outcome_unknown')),
    provider_ref            TEXT NOT NULL DEFAULT '',
    tx_hash                 TEXT NOT NULL DEFAULT '',
    confirmations           INTEGER NOT NULL DEFAULT 0,
    failure_reason          TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS reconciliation_exceptions (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    category        TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status          TEXT NOT NULL CHECK (status IN ('open', 'acknowledged', 'resolved')),
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    asset           TEXT NOT NULL DEFAULT 'USD',
    expected_base   NUMERIC(39,0) NOT NULL DEFAULT 0,
    actual_base     NUMERIC(39,0) NOT NULL DEFAULT 0,
    delta_base      NUMERIC(39,0) NOT NULL DEFAULT 0,
    summary         TEXT NOT NULL,
    resolution_note TEXT NOT NULL DEFAULT '',
    resolved_by     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    resolved_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'processed', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    processed_at    TIMESTAMPTZ
);
