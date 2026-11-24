package store

import (
	"context"
	"errors"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/ids"
)

const cardCols = `id, user_id, provider, payment_method_ref, brand, last4, exp_month, exp_year, nickname,
	is_preferred, state, created_at, updated_at, removed_at`

func scanCard(row interface{ Scan(...any) error }) (domain.SavedCard, error) {
	var c domain.SavedCard
	err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.PaymentMethodRef, &c.Brand, &c.Last4, &c.ExpMonth, &c.ExpYear,
		&c.Nickname, &c.IsPreferred, &c.State, &c.CreatedAt, &c.UpdatedAt, &c.RemovedAt)
	return c, mapNotFound(err)
}

// ListCards returns the user's cards that have not been removed, preferred
// first, then newest first.
func (s *Store) ListCards(ctx context.Context, tx Tx, userID string) ([]domain.SavedCard, error) {
	rows, err := s.q(tx).Query(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE user_id=$1 AND state <> 'removed'
		ORDER BY is_preferred DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SavedCard{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCardsIncludingRemoved returns every card row for a user.
func (s *Store) ListCardsIncludingRemoved(ctx context.Context, tx Tx, userID string) ([]domain.SavedCard, error) {
	rows, err := s.q(tx).Query(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SavedCard
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCard loads a card owned by userID. Ownership is part of the query so a
// foreign ID is indistinguishable from a missing one.
func (s *Store) GetCard(ctx context.Context, tx Tx, userID, cardID string) (domain.SavedCard, error) {
	return scanCard(s.q(tx).QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE id=$1 AND user_id=$2`, cardID, userID))
}

// GetCardByID loads a card by ID regardless of owner (internal use).
func (s *Store) GetCardByID(ctx context.Context, id string) (domain.SavedCard, error) {
	return scanCard(s.pool.QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE id=$1`, id))
}

// UpdateCardMetadata refreshes the masked display fields after the provider
// reports an update (e.g. network-issued expiry change).
func (s *Store) UpdateCardMetadata(ctx context.Context, id, brand, last4 string, expMonth, expYear int) error {
	_, err := s.pool.Exec(ctx, `UPDATE saved_cards SET brand=$2, last4=$3, exp_month=$4, exp_year=$5, updated_at=$6 WHERE id=$1 AND state <> 'removed'`,
		id, brand, last4, expMonth, expYear, s.Now())
	return err
}

// GetCardForUpdate locks the card row (any owner; used by the worker).
func (s *Store) GetCardForUpdate(ctx context.Context, tx Tx, cardID string) (domain.SavedCard, error) {
	return scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE id=$1 FOR UPDATE`, cardID))
}

// InsertCard records a newly saved card. If the same provider reference was
// already recorded (replayed webhook, concurrent poll) the existing row is
// returned with ErrConflict.
func (s *Store) InsertCard(ctx context.Context, tx Tx, c domain.SavedCard) (domain.SavedCard, error) {
	now := s.Now()
	c.ID = ids.New("card")
	c.State = domain.CardSaved
	c.CreatedAt, c.UpdatedAt = now, now
	tag, err := s.q(tx).Exec(ctx, `INSERT INTO saved_cards(`+cardCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (provider, payment_method_ref) DO NOTHING`,
		c.ID, c.UserID, c.Provider, c.PaymentMethodRef, c.Brand, c.Last4, c.ExpMonth, c.ExpYear, c.Nickname,
		c.IsPreferred, c.State, c.CreatedAt, c.UpdatedAt, c.RemovedAt)
	if err != nil {
		return domain.SavedCard{}, err
	}
	if tag.RowsAffected() == 0 {
		existing, gerr := scanCard(s.q(tx).QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE provider=$1 AND payment_method_ref=$2`,
			c.Provider, c.PaymentMethodRef))
		if gerr != nil {
			return domain.SavedCard{}, gerr
		}
		return existing, ErrConflict
	}
	return c, nil
}

// HasPreferredCard reports whether the user already has a preferred card.
func (s *Store) HasPreferredCard(ctx context.Context, tx Tx, userID string) (bool, error) {
	var ok bool
	err := s.q(tx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_cards WHERE user_id=$1 AND is_preferred AND state <> 'removed')`, userID).Scan(&ok)
	return ok, err
}

// UpdateCardDisplay changes the nickname and/or preferred flag of a card the
// user owns. Setting preferred clears the flag from any other card.
func (s *Store) UpdateCardDisplay(ctx context.Context, userID, cardID string, nickname **string, preferred *bool) (domain.SavedCard, error) {
	var out domain.SavedCard
	err := s.WithTx(ctx, func(tx Tx) error {
		c, err := scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE id=$1 AND user_id=$2 FOR UPDATE`, cardID, userID))
		if err != nil {
			return err
		}
		if c.State == domain.CardRemoved {
			return ErrNotFound
		}
		now := s.Now()
		if nickname != nil {
			if _, err := tx.Exec(ctx, `UPDATE saved_cards SET nickname=$2, updated_at=$3 WHERE id=$1`, cardID, *nickname, now); err != nil {
				return err
			}
		}
		if preferred != nil {
			if *preferred {
				if _, err := tx.Exec(ctx, `UPDATE saved_cards SET is_preferred=false, updated_at=$2 WHERE user_id=$1 AND is_preferred AND id <> $3`,
					userID, now, cardID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE saved_cards SET is_preferred=$2, updated_at=$3 WHERE id=$1`, cardID, *preferred, now); err != nil {
				return err
			}
		}
		out, err = scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE id=$1`, cardID))
		return err
	})
	return out, err
}

// SetCardState transitions a card. Removed cards lose their preferred flag.
func (s *Store) SetCardState(ctx context.Context, tx Tx, cardID string, state domain.CardState) error {
	now := s.Now()
	var err error
	switch state {
	case domain.CardRemoved:
		_, err = s.q(tx).Exec(ctx, `UPDATE saved_cards SET state='removed', is_preferred=false, removed_at=$2, updated_at=$2 WHERE id=$1`, cardID, now)
	case domain.CardRemovalPending, domain.CardSaved:
		_, err = s.q(tx).Exec(ctx, `UPDATE saved_cards SET state=$2, updated_at=$3 WHERE id=$1`, cardID, state, now)
	default:
		err = errors.New("store: invalid card state")
	}
	return err
}

// CardByPaymentMethodRef resolves a provider reference to our record.
func (s *Store) CardByPaymentMethodRef(ctx context.Context, provider, ref string) (domain.SavedCard, error) {
	return scanCard(s.pool.QueryRow(ctx, `SELECT `+cardCols+` FROM saved_cards WHERE provider=$1 AND payment_method_ref=$2`, provider, ref))
}
