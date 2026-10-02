// Package store is the PostgreSQL persistence layer. PostgreSQL is the only
// durable source of truth. Every query is scoped by tenant, and no transaction
// here spans a call to the engine or a provider.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/migrations"
)

var (
	ErrNotFound = errors.New("store: not found")
	// ErrDuplicateKey means the idempotency key was already used.
	ErrDuplicateKey = errors.New("store: idempotency key already used")
	// ErrUnresolvedAttempt means a prior payment attempt for the same customer
	// and basket has not been reconciled.
	ErrUnresolvedAttempt = errors.New("store: unresolved payment attempt exists")
	// ErrRuleConflict means a reward rule version was seen with new contents.
	ErrRuleConflict = errors.New("store: reward rule version changed contents")
)

// Store wraps a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects and applies migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping verifies connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// migrate applies embedded migrations in lexical order exactly once. An
// advisory lock serialises concurrent starters.
func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7420261002)`); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(7420261002)`) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// lockKey derives a 64-bit advisory lock key from identifiers.
func lockKey(parts ...string) int64 {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// ---------------------------------------------------------------------------
// Tenants, keys and the pause control
// ---------------------------------------------------------------------------

// Principal is an authenticated API key.
type Principal struct {
	KeyID    string
	TenantID string
	Scopes   []string
}

// HasScope reports whether the key was granted scope.
func (p *Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// EnsureTenant creates the tenant if it does not exist.
func (s *Store) EnsureTenant(ctx context.Context, id, name string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`, id, name, now)
	return err
}

// CreateAPIKey stores the hash of secret with its scopes and returns the key id.
// Creating the same secret twice for a tenant is a no-op.
func (s *Store) CreateAPIKey(ctx context.Context, tenantID, label string, scopes []string, secret string, now time.Time) (string, error) {
	id := ids.New("key")
	var out string
	err := s.pool.QueryRow(ctx, `INSERT INTO api_keys (id, tenant_id, key_hash, scopes, label, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key_hash) DO UPDATE SET label = api_keys.label
		RETURNING id`, id, tenantID, ids.HashSecret(secret), scopes, label, now).Scan(&out)
	return out, err
}

// RevokeAPIKey revokes a key by id.
func (s *Store) RevokeAPIKey(ctx context.Context, keyID string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, keyID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate resolves a presented secret to a principal.
func (s *Store) Authenticate(ctx context.Context, secret string) (*Principal, error) {
	p := &Principal{}
	err := s.pool.QueryRow(ctx, `SELECT id, tenant_id, scopes FROM api_keys
		WHERE key_hash = $1 AND revoked_at IS NULL`, ids.HashSecret(secret)).Scan(&p.KeyID, &p.TenantID, &p.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// Paused reports whether the product is paused globally or for the tenant.
func (s *Store) Paused(ctx context.Context, tenantID string) (bool, string, error) {
	var reason string
	err := s.pool.QueryRow(ctx, `SELECT reason FROM product_controls
		WHERE paused AND scope IN ('global', $1) ORDER BY (scope = 'global') DESC LIMIT 1`, tenantID).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	return err == nil, reason, err
}

// SetPaused sets the pause control for 'global' or a tenant id.
func (s *Store) SetPaused(ctx context.Context, scope string, paused bool, reason string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO product_controls (scope, paused, reason, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope) DO UPDATE SET paused = $2, reason = $3, updated_at = $4`, scope, paused, reason, now)
	return err
}

// ---------------------------------------------------------------------------
// Preference policies
// ---------------------------------------------------------------------------

// PreferencePolicy is one immutable version of a customer's preferences.
type PreferencePolicy struct {
	ID         string
	TenantID   string
	CustomerID string
	Version    int
	Content    json.RawMessage
	CreatedAt  time.Time
}

// LatestPreferences returns the newest version, or nil when none exists.
func (s *Store) LatestPreferences(ctx context.Context, tenantID, customerID string) (*PreferencePolicy, error) {
	p := &PreferencePolicy{TenantID: tenantID, CustomerID: customerID}
	err := s.pool.QueryRow(ctx, `SELECT id, version, content, created_at FROM preference_policies
		WHERE tenant_id = $1 AND customer_id = $2 ORDER BY version DESC LIMIT 1`, tenantID, customerID).
		Scan(&p.ID, &p.Version, &p.Content, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, err
}

// PutPreferences appends a new version. Concurrent writers retry on the
// version uniqueness constraint so versions stay dense and ordered.
func (s *Store) PutPreferences(ctx context.Context, tenantID, customerID string, content json.RawMessage, now time.Time) (*PreferencePolicy, error) {
	for attempt := 0; attempt < 5; attempt++ {
		p := &PreferencePolicy{ID: ids.New("pref"), TenantID: tenantID, CustomerID: customerID, Content: content, CreatedAt: now}
		err := s.pool.QueryRow(ctx, `INSERT INTO preference_policies (id, tenant_id, customer_id, version, content, created_at)
			SELECT $1, $2, $3, COALESCE(MAX(version), 0) + 1, $4, $5
			FROM preference_policies WHERE tenant_id = $2 AND customer_id = $3
			RETURNING version`, p.ID, tenantID, customerID, content, now).Scan(&p.Version)
		if isUniqueViolation(err, "") {
			continue
		}
		if err != nil {
			return nil, err
		}
		return p, nil
	}
	return nil, errors.New("store: could not allocate a preference version")
}

// ---------------------------------------------------------------------------
// Evaluations
// ---------------------------------------------------------------------------

// Evaluation is an immutable evaluation snapshot.
type Evaluation struct {
	ID                 string
	TenantID           string
	CustomerID         string
	MerchantID         string
	BasketSnapshotID   string
	BasketRef          string
	BasketHash         string
	Basket             json.RawMessage
	Mode               string
	PreferencePolicyID *string
	RefreshedFrom      *string
	IdempotencyKey     *string
	RequestFingerprint string
	EngineRequest      string
	InputSHA256        string
	EngineResult       json.RawMessage
	ContractVersion    string
	EngineVersion      string
	FormulaVersion     string
	RoundingVersion    string
	PolicyVersion      string
	PreferenceVersion  string
	Outcome            string
	PreferredOptionID  *string
	BaselineOptionID   *string
	Warnings           []string
	Attribution        json.RawMessage
	EvaluatedAt        time.Time
	ExpiresAt          *time.Time
	CreatedAt          time.Time
}

// RailQuoteRow is one option's quote as it was used.
type RailQuoteRow struct {
	OptionID   string
	Rail       string
	Provider   string
	QuoteID    string
	ObtainedAt time.Time
	ValidUntil time.Time
	Content    json.RawMessage
}

// RewardRuleRow is one reward rule version seen in a quote.
type RewardRuleRow struct {
	RuleID      string
	RuleVersion string
	Content     json.RawMessage
}

// InsertEvaluation writes the basket snapshot, rail quotes, reward rule
// versions and evaluation in one transaction.
func (s *Store) InsertEvaluation(ctx context.Context, e *Evaluation, quotes []RailQuoteRow, rules []RewardRuleRow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `INSERT INTO basket_snapshots (id, tenant_id, merchant_id, basket_ref, basket_hash, content, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.BasketSnapshotID, e.TenantID, e.MerchantID, e.BasketRef, e.BasketHash, e.Basket, e.CreatedAt); err != nil {
		return err
	}

	for _, r := range rules {
		hash := ids.Fingerprint(r.Content)
		var stored string
		err := tx.QueryRow(ctx, `INSERT INTO reward_rule_versions (tenant_id, rule_id, rule_version, content_hash, content, first_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, rule_id, rule_version) DO NOTHING
			RETURNING content_hash`, e.TenantID, r.RuleID, r.RuleVersion, hash, r.Content, e.CreatedAt).Scan(&stored)
		if errors.Is(err, pgx.ErrNoRows) {
			// The version exists; its contents must be identical.
			err = tx.QueryRow(ctx, `SELECT content_hash FROM reward_rule_versions
				WHERE tenant_id = $1 AND rule_id = $2 AND rule_version = $3`, e.TenantID, r.RuleID, r.RuleVersion).Scan(&stored)
		}
		if err != nil {
			return err
		}
		if stored != hash {
			return fmt.Errorf("%w: %s@%s", ErrRuleConflict, r.RuleID, r.RuleVersion)
		}
	}

	_, err = tx.Exec(ctx, `INSERT INTO evaluations (
			id, tenant_id, customer_id, merchant_id, basket_snapshot_id, basket_hash, mode,
			preference_policy_id, refreshed_from, idempotency_key, request_fingerprint,
			engine_request, input_sha256, engine_result,
			contract_version, engine_version, formula_version, rounding_version, policy_version, preference_version,
			outcome, preferred_option_id, baseline_option_id, warnings, attribution,
			evaluated_at, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
		e.ID, e.TenantID, e.CustomerID, e.MerchantID, e.BasketSnapshotID, e.BasketHash, e.Mode,
		e.PreferencePolicyID, e.RefreshedFrom, e.IdempotencyKey, e.RequestFingerprint,
		e.EngineRequest, e.InputSHA256, e.EngineResult,
		e.ContractVersion, e.EngineVersion, e.FormulaVersion, e.RoundingVersion, e.PolicyVersion, e.PreferenceVersion,
		e.Outcome, e.PreferredOptionID, e.BaselineOptionID, e.Warnings, e.Attribution,
		e.EvaluatedAt, e.ExpiresAt, e.CreatedAt)
	if isUniqueViolation(err, "evaluations_tenant_id_idempotency_key_key") {
		return ErrDuplicateKey
	}
	if err != nil {
		return err
	}

	for _, q := range quotes {
		if _, err := tx.Exec(ctx, `INSERT INTO rail_quotes (id, tenant_id, evaluation_id, option_id, rail, provider, quote_id, obtained_at, valid_until, content)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			ids.New("rq"), e.TenantID, e.ID, q.OptionID, q.Rail, q.Provider, q.QuoteID, q.ObtainedAt, q.ValidUntil, q.Content); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const evaluationColumns = `e.id, e.tenant_id, e.customer_id, e.merchant_id, e.basket_snapshot_id, b.basket_ref, e.basket_hash, b.content, e.mode,
	e.preference_policy_id, e.refreshed_from, e.idempotency_key, e.request_fingerprint,
	e.engine_request, e.input_sha256, e.engine_result,
	e.contract_version, e.engine_version, e.formula_version, e.rounding_version, e.policy_version, e.preference_version,
	e.outcome, e.preferred_option_id, e.baseline_option_id, e.warnings, e.attribution,
	e.evaluated_at, e.expires_at, e.created_at`

func scanEvaluation(row pgx.Row) (*Evaluation, error) {
	e := &Evaluation{}
	err := row.Scan(&e.ID, &e.TenantID, &e.CustomerID, &e.MerchantID, &e.BasketSnapshotID, &e.BasketRef, &e.BasketHash, &e.Basket, &e.Mode,
		&e.PreferencePolicyID, &e.RefreshedFrom, &e.IdempotencyKey, &e.RequestFingerprint,
		&e.EngineRequest, &e.InputSHA256, &e.EngineResult,
		&e.ContractVersion, &e.EngineVersion, &e.FormulaVersion, &e.RoundingVersion, &e.PolicyVersion, &e.PreferenceVersion,
		&e.Outcome, &e.PreferredOptionID, &e.BaselineOptionID, &e.Warnings, &e.Attribution,
		&e.EvaluatedAt, &e.ExpiresAt, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	e.EvaluatedAt, e.CreatedAt = e.EvaluatedAt.UTC(), e.CreatedAt.UTC()
	if e.ExpiresAt != nil {
		utc := e.ExpiresAt.UTC()
		e.ExpiresAt = &utc
	}
	return e, err
}

// GetEvaluation returns a tenant's evaluation by id.
func (s *Store) GetEvaluation(ctx context.Context, tenantID, id string) (*Evaluation, error) {
	return scanEvaluation(s.pool.QueryRow(ctx, `SELECT `+evaluationColumns+`
		FROM evaluations e JOIN basket_snapshots b ON b.id = e.basket_snapshot_id
		WHERE e.tenant_id = $1 AND e.id = $2`, tenantID, id))
}

// GetEvaluationByKey returns a tenant's evaluation by idempotency key.
func (s *Store) GetEvaluationByKey(ctx context.Context, tenantID, key string) (*Evaluation, error) {
	return scanEvaluation(s.pool.QueryRow(ctx, `SELECT `+evaluationColumns+`
		FROM evaluations e JOIN basket_snapshots b ON b.id = e.basket_snapshot_id
		WHERE e.tenant_id = $1 AND e.idempotency_key = $2`, tenantID, key))
}

// ---------------------------------------------------------------------------
// Execution handoffs
// ---------------------------------------------------------------------------

// Handoff statuses.
const (
	HandoffCreated   = "created"
	HandoffSucceeded = "succeeded"
	HandoffFailed    = "failed"
	HandoffUnknown   = "unknown"
)

// Handoff is a constrained reference for one approved payment.
type Handoff struct {
	ID                 string
	TenantID           string
	EvaluationID       string
	CustomerID         string
	MerchantID         string
	BasketHash         string
	OptionID           string
	Rail               string
	QuoteID            string
	Amount             json.RawMessage
	Approval           json.RawMessage
	Status             string
	IdempotencyKey     string
	RequestFingerprint string
	ExpiresAt          time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateHandoff inserts a handoff and its outbox event atomically. The
// partial unique index guarantees that two concurrent requests cannot both
// open an attempt for the same customer and basket.
func (s *Store) CreateHandoff(ctx context.Context, h *Handoff, topic string, payload json.RawMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `INSERT INTO execution_handoffs (
			id, tenant_id, evaluation_id, customer_id, merchant_id, basket_hash, option_id, rail, quote_id,
			amount, approval, status, idempotency_key, request_fingerprint, expires_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`,
		h.ID, h.TenantID, h.EvaluationID, h.CustomerID, h.MerchantID, h.BasketHash, h.OptionID, h.Rail, h.QuoteID,
		h.Amount, h.Approval, h.Status, h.IdempotencyKey, h.RequestFingerprint, h.ExpiresAt, h.CreatedAt)
	switch {
	case isUniqueViolation(err, "execution_handoffs_tenant_id_idempotency_key_key"):
		return ErrDuplicateKey
	case isUniqueViolation(err, "execution_handoffs_one_unresolved"):
		return ErrUnresolvedAttempt
	case err != nil:
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, payload, created_at, available_at)
		VALUES ($1, $2, $3, $4, $4)`, h.TenantID, topic, payload, h.CreatedAt); err != nil {
		return err
	}
	h.UpdatedAt = h.CreatedAt
	return tx.Commit(ctx)
}

const handoffColumns = `id, tenant_id, evaluation_id, customer_id, merchant_id, basket_hash, option_id, rail, quote_id,
	amount, approval, status, idempotency_key, request_fingerprint, expires_at, created_at, updated_at`

func scanHandoff(row pgx.Row) (*Handoff, error) {
	h := &Handoff{}
	err := row.Scan(&h.ID, &h.TenantID, &h.EvaluationID, &h.CustomerID, &h.MerchantID, &h.BasketHash, &h.OptionID, &h.Rail, &h.QuoteID,
		&h.Amount, &h.Approval, &h.Status, &h.IdempotencyKey, &h.RequestFingerprint, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	h.ExpiresAt, h.CreatedAt, h.UpdatedAt = h.ExpiresAt.UTC(), h.CreatedAt.UTC(), h.UpdatedAt.UTC()
	return h, err
}

// GetHandoff returns a tenant's handoff by id.
func (s *Store) GetHandoff(ctx context.Context, tenantID, id string) (*Handoff, error) {
	return scanHandoff(s.pool.QueryRow(ctx, `SELECT `+handoffColumns+` FROM execution_handoffs
		WHERE tenant_id = $1 AND id = $2`, tenantID, id))
}

// GetHandoffByKey returns a tenant's handoff by idempotency key.
func (s *Store) GetHandoffByKey(ctx context.Context, tenantID, key string) (*Handoff, error) {
	return scanHandoff(s.pool.QueryRow(ctx, `SELECT `+handoffColumns+` FROM execution_handoffs
		WHERE tenant_id = $1 AND idempotency_key = $2`, tenantID, key))
}

// MarkExpiredHandoffsUnknown moves handoffs that expired without any reported
// outcome to 'unknown'. Whether the executor attempted the payment is not
// known, so the attempt stays unresolved until an outcome is reported.
func (s *Store) MarkExpiredHandoffsUnknown(ctx context.Context, now time.Time, grace time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE execution_handoffs SET status = 'unknown', updated_at = $1
		WHERE status = 'created' AND expires_at < $2`, now, now.Add(-grace))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Observed outcomes
// ---------------------------------------------------------------------------

// Outcome is an immutable observation reported by a provider or executor.
type Outcome struct {
	ID                 string
	TenantID           string
	EvaluationID       string
	HandoffID          *string
	Source             string
	EventID            string
	Type               string
	Status             string
	OptionID           string
	Content            json.RawMessage
	Derived            json.RawMessage
	PayloadFingerprint string
	OccurredAt         time.Time
	RecordedAt         time.Time
}

// OutcomeDecision is what the caller derives from the locked state.
type OutcomeDecision struct {
	Derived json.RawMessage
	// NewHandoffStatus is applied when non-empty.
	NewHandoffStatus string
}

// ErrPayloadMismatch means an event id was replayed with a different payload.
var ErrPayloadMismatch = errors.New("store: event replayed with a different payload")

const outcomeColumns = `id, tenant_id, evaluation_id, handoff_id, source, event_id, type, status, option_id,
	content, derived, payload_fingerprint, occurred_at, recorded_at`

func scanOutcome(row pgx.Row) (*Outcome, error) {
	o := &Outcome{}
	err := row.Scan(&o.ID, &o.TenantID, &o.EvaluationID, &o.HandoffID, &o.Source, &o.EventID, &o.Type, &o.Status, &o.OptionID,
		&o.Content, &o.Derived, &o.PayloadFingerprint, &o.OccurredAt, &o.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	o.OccurredAt, o.RecordedAt = o.OccurredAt.UTC(), o.RecordedAt.UTC()
	return o, err
}

// RecordOutcome stores an outcome exactly once per (tenant, source, event id).
// The handoff row, when present, is locked for the duration so that concurrent
// outcomes for one payment are applied one at a time. decide runs inside the
// transaction and must not perform I/O.
//
// The returned bool is true when the event had already been recorded.
func (s *Store) RecordOutcome(ctx context.Context, o *Outcome, decide func(h *Handoff, prior []Outcome) (*OutcomeDecision, error)) (*Outcome, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var handoff *Handoff
	if o.HandoffID != nil {
		handoff, err = scanHandoff(tx.QueryRow(ctx, `SELECT `+handoffColumns+` FROM execution_handoffs
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, o.TenantID, *o.HandoffID))
		if err != nil {
			return nil, false, err
		}
	} else {
		// Serialise replays of the same event when there is no handoff row to lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey(o.TenantID, o.Source, o.EventID)); err != nil {
			return nil, false, err
		}
	}

	existing, err := scanOutcome(tx.QueryRow(ctx, `SELECT `+outcomeColumns+` FROM observed_outcomes
		WHERE tenant_id = $1 AND source = $2 AND event_id = $3`, o.TenantID, o.Source, o.EventID))
	switch {
	case err == nil:
		if existing.PayloadFingerprint != o.PayloadFingerprint {
			return nil, false, ErrPayloadMismatch
		}
		return existing, true, nil
	case !errors.Is(err, ErrNotFound):
		return nil, false, err
	}

	var prior []Outcome
	if handoff != nil {
		rows, err := tx.Query(ctx, `SELECT `+outcomeColumns+` FROM observed_outcomes
			WHERE tenant_id = $1 AND handoff_id = $2 ORDER BY recorded_at, id`, o.TenantID, handoff.ID)
		if err != nil {
			return nil, false, err
		}
		for rows.Next() {
			p, err := scanOutcome(rows)
			if err != nil {
				rows.Close()
				return nil, false, err
			}
			prior = append(prior, *p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}

	decision, err := decide(handoff, prior)
	if err != nil {
		return nil, false, err
	}
	o.Derived = decision.Derived

	if _, err := tx.Exec(ctx, `INSERT INTO observed_outcomes (`+outcomeColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		o.ID, o.TenantID, o.EvaluationID, o.HandoffID, o.Source, o.EventID, o.Type, o.Status, o.OptionID,
		o.Content, o.Derived, o.PayloadFingerprint, o.OccurredAt, o.RecordedAt); err != nil {
		return nil, false, err
	}
	if handoff != nil && decision.NewHandoffStatus != "" && decision.NewHandoffStatus != handoff.Status {
		if _, err := tx.Exec(ctx, `UPDATE execution_handoffs SET status = $1, updated_at = $2 WHERE id = $3`,
			decision.NewHandoffStatus, o.RecordedAt, handoff.ID); err != nil {
			return nil, false, err
		}
	}
	return o, false, tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Outbox
// ---------------------------------------------------------------------------

// OutboxEvent is a pending notification.
type OutboxEvent struct {
	ID       int64
	TenantID string
	Topic    string
	Payload  json.RawMessage
	Attempts int
}

// ClaimOutbox leases up to limit pending events. The lease is a short
// transaction of its own; delivery happens after it commits.
func (s *Store) ClaimOutbox(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]OutboxEvent, error) {
	rows, err := s.pool.Query(ctx, `UPDATE outbox SET claimed_until = $2, attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM outbox
			WHERE delivered_at IS NULL AND available_at <= $1 AND (claimed_until IS NULL OR claimed_until < $1)
			ORDER BY id LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING id, tenant_id, topic, payload, attempts`, now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Topic, &e.Payload, &e.Attempts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, rows.Err()
}

// MarkDelivered records a successful delivery.
func (s *Store) MarkDelivered(ctx context.Context, id int64, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET delivered_at = $2, claimed_until = NULL WHERE id = $1`, id, now)
	return err
}

// RetryLater releases a claim and schedules the next attempt.
func (s *Store) RetryLater(ctx context.Context, id int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET available_at = $2, claimed_until = NULL WHERE id = $1`, id, at)
	return err
}

// PendingOutbox counts undelivered events for a tenant.
func (s *Store) PendingOutbox(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND delivered_at IS NULL`, tenantID).Scan(&n)
	return n, err
}
