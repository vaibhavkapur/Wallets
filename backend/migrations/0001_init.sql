-- MyWallet card-saving schema.
--
-- Full card numbers and CVVs never reach this database. Only provider
-- references (customer, setup intent, payment method IDs) and the masked
-- display metadata the provider returns are stored.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    apple_subject TEXT NOT NULL UNIQUE,
    email         TEXT,
    status        TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'deleting', 'deleted')),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    deleted_at    TIMESTAMPTZ
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX sessions_user_idx ON sessions(user_id);

-- One provider customer per user. The mapping is derived from the
-- authenticated account; callers never supply a customer reference.
CREATE TABLE provider_customers (
    user_id      TEXT PRIMARY KEY REFERENCES users(id),
    provider     TEXT NOT NULL,
    customer_ref TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (provider, customer_ref)
);

CREATE TABLE consent_records (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id),
    purpose         TEXT NOT NULL,
    consent_version TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX consent_records_user_idx ON consent_records(user_id);

CREATE TABLE saved_cards (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id),
    provider           TEXT NOT NULL,
    payment_method_ref TEXT NOT NULL,
    brand              TEXT NOT NULL,
    last4              TEXT NOT NULL CHECK (char_length(last4) = 4),
    exp_month          INT  NOT NULL CHECK (exp_month BETWEEN 1 AND 12),
    exp_year           INT  NOT NULL,
    nickname           TEXT,
    is_preferred       BOOLEAN NOT NULL DEFAULT false,
    state              TEXT NOT NULL
                       CHECK (state IN ('saved', 'removal_pending', 'removed')),
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    removed_at         TIMESTAMPTZ,
    UNIQUE (provider, payment_method_ref)
);
CREATE INDEX saved_cards_user_idx ON saved_cards(user_id);
-- At most one preferred card per user among cards that still exist.
CREATE UNIQUE INDEX saved_cards_one_preferred
    ON saved_cards(user_id) WHERE is_preferred AND state <> 'removed';

CREATE TABLE setup_attempts (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id),
    idempotency_key TEXT NOT NULL,
    consent_id      TEXT NOT NULL REFERENCES consent_records(id),
    provider        TEXT NOT NULL,
    setup_ref       TEXT,
    status          TEXT NOT NULL
                    CHECK (status IN ('collecting', 'authentication_required', 'processing',
                                      'saved', 'failed', 'canceled')),
    failure_code    TEXT,
    saved_card_id   TEXT REFERENCES saved_cards(id),
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, idempotency_key)
);
CREATE UNIQUE INDEX setup_attempts_setup_ref_idx ON setup_attempts(provider, setup_ref) WHERE setup_ref IS NOT NULL;
CREATE INDEX setup_attempts_open_idx ON setup_attempts(status, updated_at)
    WHERE status IN ('collecting', 'authentication_required', 'processing');

-- Processed webhook deliveries, used to deduplicate.
CREATE TABLE provider_events (
    provider    TEXT NOT NULL,
    event_id    TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, event_id)
);

-- Durable work the retry worker drives to completion: provider detachment,
-- account deletion, and reconciliation of setups the provider left processing.
CREATE TABLE pending_operations (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('detach_card', 'delete_account', 'reconcile_setup')),
    subject_id      TEXT NOT NULL,
    attempts        INT  NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX pending_operations_active_idx
    ON pending_operations(kind, subject_id) WHERE completed_at IS NULL;
CREATE INDEX pending_operations_due_idx ON pending_operations(next_attempt_at) WHERE completed_at IS NULL;
