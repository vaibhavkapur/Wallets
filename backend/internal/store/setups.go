package store

import (
	"context"
	"time"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/ids"
)

const setupCols = `id, user_id, idempotency_key, consent_id, provider, setup_ref, status, failure_code, saved_card_id, created_at, updated_at`

func scanSetup(row interface{ Scan(...any) error }) (domain.SetupAttempt, error) {
	var a domain.SetupAttempt
	err := row.Scan(&a.ID, &a.UserID, &a.IdempotencyKey, &a.ConsentID, &a.Provider, &a.SetupRef, &a.Status, &a.FailureCode,
		&a.SavedCardID, &a.CreatedAt, &a.UpdatedAt)
	return a, mapNotFound(err)
}

// CreateSetupAttempt inserts an attempt in the collecting state. When the
// (user, idempotency key) pair already exists the existing attempt is
// returned with ErrConflict so the caller replays the earlier result.
func (s *Store) CreateSetupAttempt(ctx context.Context, tx Tx, userID, idemKey, consentID, provider string) (domain.SetupAttempt, error) {
	now := s.Now()
	a := domain.SetupAttempt{ID: ids.New("setup"), UserID: userID, IdempotencyKey: idemKey, ConsentID: consentID,
		Provider: provider, Status: domain.SetupCollecting, CreatedAt: now, UpdatedAt: now}
	// ON CONFLICT DO NOTHING keeps the transaction usable after a duplicate,
	// unlike a raised unique violation.
	tag, err := s.q(tx).Exec(ctx, `INSERT INTO setup_attempts(id, user_id, idempotency_key, consent_id, provider, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7) ON CONFLICT (user_id, idempotency_key) DO NOTHING`, a.ID, a.UserID, a.IdempotencyKey, a.ConsentID, a.Provider, a.Status, now)
	if err != nil {
		return domain.SetupAttempt{}, err
	}
	if tag.RowsAffected() == 0 {
		existing, gerr := scanSetup(s.q(tx).QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE user_id=$1 AND idempotency_key=$2`, userID, idemKey))
		if gerr != nil {
			return domain.SetupAttempt{}, gerr
		}
		return existing, ErrConflict
	}
	return a, nil
}

// GetSetupAttempt loads an attempt owned by userID.
func (s *Store) GetSetupAttempt(ctx context.Context, tx Tx, userID, id string) (domain.SetupAttempt, error) {
	return scanSetup(s.q(tx).QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE id=$1 AND user_id=$2`, id, userID))
}

// GetSetupAttemptByID loads an attempt by ID regardless of owner (internal use).
func (s *Store) GetSetupAttemptByID(ctx context.Context, id string) (domain.SetupAttempt, error) {
	return scanSetup(s.pool.QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE id=$1`, id))
}

// SetupAttemptByKey loads the attempt for a (user, idempotency key) pair.
func (s *Store) SetupAttemptByKey(ctx context.Context, userID, idemKey string) (domain.SetupAttempt, error) {
	return scanSetup(s.pool.QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE user_id=$1 AND idempotency_key=$2`, userID, idemKey))
}

// GetSetupAttemptForUpdate locks an attempt by ID regardless of owner.
func (s *Store) GetSetupAttemptForUpdate(ctx context.Context, tx Tx, id string) (domain.SetupAttempt, error) {
	return scanSetup(tx.QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE id=$1 FOR UPDATE`, id))
}

// SetupAttemptBySetupRef resolves a provider setup reference.
func (s *Store) SetupAttemptBySetupRef(ctx context.Context, provider, ref string) (domain.SetupAttempt, error) {
	return scanSetup(s.pool.QueryRow(ctx, `SELECT `+setupCols+` FROM setup_attempts WHERE provider=$1 AND setup_ref=$2`, provider, ref))
}

// SetSetupRef records the provider's setup reference once it exists.
func (s *Store) SetSetupRef(ctx context.Context, tx Tx, id, ref string) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE setup_attempts SET setup_ref=$2, updated_at=$3 WHERE id=$1 AND setup_ref IS NULL`, id, ref, s.Now())
	return err
}

// SetSetupStatus transitions an attempt. failureCode and savedCardID may be nil.
func (s *Store) SetSetupStatus(ctx context.Context, tx Tx, id string, status domain.SetupStatus, failureCode, savedCardID *string) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE setup_attempts SET status=$2, failure_code=$3, saved_card_id=COALESCE($4, saved_card_id), updated_at=$5 WHERE id=$1`,
		id, status, failureCode, savedCardID, s.Now())
	return err
}

// StaleOpenSetupAttempts returns non-terminal attempts untouched since the
// cutoff, so the worker can reconcile or expire them.
func (s *Store) StaleOpenSetupAttempts(ctx context.Context, before time.Time, limit int) ([]domain.SetupAttempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+setupCols+` FROM setup_attempts
		WHERE status IN ('collecting','authentication_required','processing') AND updated_at < $1 ORDER BY updated_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SetupAttempt
	for rows.Next() {
		a, err := scanSetup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- provider events --------------------------------------------------------

// MarkEventProcessed records a webhook delivery. It returns false when the
// event was already recorded, letting handlers skip duplicate deliveries.
func (s *Store) MarkEventProcessed(ctx context.Context, provider, eventID, eventType string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO provider_events(provider, event_id, event_type, received_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT DO NOTHING`, provider, eventID, eventType, s.Now())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ForgetEvent removes a processed marker so a provider retry is handled.
func (s *Store) ForgetEvent(ctx context.Context, provider, eventID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM provider_events WHERE provider=$1 AND event_id=$2`, provider, eventID)
	return err
}
