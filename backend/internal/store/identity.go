package store

import (
	"context"
	"time"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/ids"
)

const userCols = `id, apple_subject, email, status, created_at, updated_at, deleted_at`

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.AppleSubject, &u.Email, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt)
	return u, mapNotFound(err)
}

// UpsertUserByAppleSubject finds or creates the user for an Apple subject.
// The email is recorded on first sign-in only; Apple only sends it once.
func (s *Store) UpsertUserByAppleSubject(ctx context.Context, subject string, email *string) (domain.User, bool, error) {
	now := s.Now()
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE apple_subject=$1`, subject))
	if err == nil {
		return u, false, nil
	}
	if err != ErrNotFound {
		return domain.User{}, false, err
	}
	id := ids.New("usr")
	_, err = s.pool.Exec(ctx, `INSERT INTO users(id, apple_subject, email, status, created_at, updated_at)
		VALUES ($1,$2,$3,'active',$4,$4) ON CONFLICT (apple_subject) DO NOTHING`, id, subject, email, now)
	if err != nil {
		return domain.User{}, false, err
	}
	u, err = scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE apple_subject=$1`, subject))
	return u, u.ID == id, err
}

// GetUser loads a user by ID.
func (s *Store) GetUser(ctx context.Context, tx Tx, id string) (domain.User, error) {
	return scanUser(s.q(tx).QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

// GetUserForUpdate locks the user row.
func (s *Store) GetUserForUpdate(ctx context.Context, tx Tx, id string) (domain.User, error) {
	return scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1 FOR UPDATE`, id))
}

// SetUserStatus changes the lifecycle status.
func (s *Store) SetUserStatus(ctx context.Context, tx Tx, id string, status domain.UserStatus) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE users SET status=$2, updated_at=$3 WHERE id=$1`, id, status, s.Now())
	return err
}

// ScrubUser finishes account deletion: PII is removed and the Apple subject
// is released so a later sign-in creates a fresh account.
func (s *Store) ScrubUser(ctx context.Context, tx Tx, id string) error {
	now := s.Now()
	_, err := s.q(tx).Exec(ctx, `UPDATE users SET email=NULL, apple_subject='deleted:'||id, status='deleted',
		deleted_at=$2, updated_at=$2 WHERE id=$1`, id, now)
	return err
}

// --- sessions ---------------------------------------------------------------

// CreateSession issues a new bearer token and returns it once.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, domain.Session, error) {
	now := s.Now()
	token := ids.NewSecret("ses")
	sess := domain.Session{ID: ids.New("sid"), UserID: userID, TokenHash: ids.HashSecret(token), CreatedAt: now, ExpiresAt: now.Add(ttl)}
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions(id, user_id, token_hash, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5)`, sess.ID, sess.UserID, sess.TokenHash, sess.CreatedAt, sess.ExpiresAt)
	if err != nil {
		return "", domain.Session{}, err
	}
	return token, sess, nil
}

// UserBySessionToken resolves a live session to its active user.
func (s *Store) UserBySessionToken(ctx context.Context, token string) (domain.User, domain.Session, error) {
	var sess domain.Session
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, token_hash, created_at, expires_at, revoked_at FROM sessions WHERE token_hash=$1`,
		ids.HashSecret(token)).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt)
	if err != nil {
		return domain.User{}, domain.Session{}, mapNotFound(err)
	}
	if sess.RevokedAt != nil || !s.Now().Before(sess.ExpiresAt) {
		return domain.User{}, domain.Session{}, ErrNotFound
	}
	u, err := s.GetUser(ctx, nil, sess.UserID)
	if err != nil {
		return domain.User{}, domain.Session{}, err
	}
	return u, sess, nil
}

// RevokeSession ends one session.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, id, s.Now())
	return err
}

// RevokeAllSessions ends every session for a user.
func (s *Store) RevokeAllSessions(ctx context.Context, tx Tx, userID string) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, s.Now())
	return err
}

// --- provider customers -----------------------------------------------------

// GetProviderCustomer returns the mapping for a user, or ErrNotFound.
func (s *Store) GetProviderCustomer(ctx context.Context, tx Tx, userID string) (domain.ProviderCustomer, error) {
	var pc domain.ProviderCustomer
	err := s.q(tx).QueryRow(ctx, `SELECT user_id, provider, customer_ref, created_at FROM provider_customers WHERE user_id=$1`, userID).
		Scan(&pc.UserID, &pc.Provider, &pc.CustomerRef, &pc.CreatedAt)
	return pc, mapNotFound(err)
}

// PutProviderCustomer records the mapping. If one already exists the
// existing mapping is returned and ErrConflict is reported so the caller can
// clean up the provider object it created.
func (s *Store) PutProviderCustomer(ctx context.Context, pc domain.ProviderCustomer) (domain.ProviderCustomer, error) {
	pc.CreatedAt = s.Now()
	tag, err := s.pool.Exec(ctx, `INSERT INTO provider_customers(user_id, provider, customer_ref, created_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id) DO NOTHING`, pc.UserID, pc.Provider, pc.CustomerRef, pc.CreatedAt)
	if err != nil {
		return domain.ProviderCustomer{}, err
	}
	if tag.RowsAffected() == 0 {
		existing, gerr := s.GetProviderCustomer(ctx, nil, pc.UserID)
		if gerr != nil {
			return domain.ProviderCustomer{}, gerr
		}
		return existing, ErrConflict
	}
	return pc, nil
}

// --- consent ----------------------------------------------------------------

// RecordConsent stores a consent record.
func (s *Store) RecordConsent(ctx context.Context, tx Tx, userID, purpose, version string) (domain.ConsentRecord, error) {
	c := domain.ConsentRecord{ID: ids.New("cns"), UserID: userID, Purpose: purpose, ConsentVersion: version, CreatedAt: s.Now()}
	_, err := s.q(tx).Exec(ctx, `INSERT INTO consent_records(id, user_id, purpose, consent_version, created_at) VALUES ($1,$2,$3,$4,$5)`,
		c.ID, c.UserID, c.Purpose, c.ConsentVersion, c.CreatedAt)
	return c, err
}
