// Package store is the PostgreSQL persistence layer. PostgreSQL is the only
// durable source of truth: every budget decision happens inside a transaction
// that holds row locks on the organization, agent, task and balance rows.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/migrations"
)

var migrationFS = migrations.FS

var (
	ErrNotFound  = errors.New("store: not found")
	ErrConflict  = errors.New("store: conflict")
	ErrForbidden = errors.New("store: forbidden")
)

// Store wraps a pgx pool.
type Store struct {
	pool *pgxpool.Pool
	Now  func() time.Time
}

// Open connects and runs migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	s := &Store{pool: pool, Now: func() time.Time { return time.Now().UTC() }}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping verifies connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies embedded migrations in lexical order exactly once.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, ".")
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
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// withTx runs fn in a transaction, rolling back on error.
func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// querier is satisfied by both the pool and a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// --- audit & outbox (shared by all transactional operations) ----------------

func appendAudit(ctx context.Context, q querier, now time.Time, orgID, actor, action, subjectType, subjectID string, details any) error {
	var raw json.RawMessage
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_events(id, org_id, actor, action, subject_type, subject_id, details, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New("aud"), orgID, actor, action, subjectType, subjectID, nullableJSON(raw), now)
	return err
}

func enqueueOutbox(ctx context.Context, q querier, now time.Time, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO outbox(id, topic, payload, created_at) VALUES ($1,$2,$3,$4)`,
		ids.New("obx"), topic, b, now)
	return err
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// AppendAudit records an audit event outside any other transaction.
func (s *Store) AppendAudit(ctx context.Context, ev domain.AuditEvent) error {
	return appendAudit(ctx, s.pool, s.Now(), ev.OrgID, ev.Actor, ev.Action, ev.SubjectType, ev.SubjectID, ev.Details)
}

// ListAudit returns the newest audit events for an organization.
func (s *Store) ListAudit(ctx context.Context, orgID string, limit int) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, actor, action, subject_type, subject_id, details, created_at
		FROM audit_events WHERE org_id=$1 ORDER BY created_at DESC LIMIT $2`, orgID, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var ev domain.AuditEvent
		var details []byte
		if err := rows.Scan(&ev.ID, &ev.OrgID, &ev.Actor, &ev.Action, &ev.SubjectType, &ev.SubjectID, &details, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Details = details
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ClaimOutbox returns unpublished messages oldest first.
func (s *Store) ClaimOutbox(ctx context.Context, limit int) ([]domain.OutboxMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, topic, payload, created_at FROM outbox
		WHERE published_at IS NULL ORDER BY created_at LIMIT $1`, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OutboxMessage
	for rows.Next() {
		var m domain.OutboxMessage
		var payload []byte
		if err := rows.Scan(&m.ID, &m.Topic, &payload, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Payload = payload
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkOutboxPublished stamps messages as delivered.
func (s *Store) MarkOutboxPublished(ctx context.Context, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET published_at=$2 WHERE id = ANY($1) AND published_at IS NULL`, msgIDs, s.Now())
	return err
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 100
	}
	return limit
}
