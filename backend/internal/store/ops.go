package store

import (
	"context"
	"time"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/ids"
)

const opCols = `id, kind, subject_id, attempts, next_attempt_at, last_error, created_at, completed_at`

func scanOp(row interface{ Scan(...any) error }) (domain.PendingOperation, error) {
	var op domain.PendingOperation
	err := row.Scan(&op.ID, &op.Kind, &op.SubjectID, &op.Attempts, &op.NextAttemptAt, &op.LastError, &op.CreatedAt, &op.CompletedAt)
	return op, mapNotFound(err)
}

// EnqueueOperation records durable work. Re-enqueueing an active
// (kind, subject) pair is a no-op, which keeps retries idempotent.
func (s *Store) EnqueueOperation(ctx context.Context, tx Tx, kind domain.OperationKind, subjectID string) error {
	now := s.Now()
	_, err := s.q(tx).Exec(ctx, `INSERT INTO pending_operations(id, kind, subject_id, attempts, next_attempt_at, created_at)
		VALUES ($1,$2,$3,0,$4,$4) ON CONFLICT DO NOTHING`, ids.New("op"), kind, subjectID, now)
	return err
}

// ClaimDueOperations returns active operations whose retry time has passed,
// skipping rows locked by other workers. The rows are pushed into the
// future immediately so a crashed worker cannot starve the queue.
func (s *Store) ClaimDueOperations(ctx context.Context, limit int, lease time.Duration) ([]domain.PendingOperation, error) {
	var out []domain.PendingOperation
	err := s.WithTx(ctx, func(tx Tx) error {
		now := s.Now()
		rows, err := tx.Query(ctx, `SELECT `+opCols+` FROM pending_operations
			WHERE completed_at IS NULL AND next_attempt_at <= $1 ORDER BY next_attempt_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			op, err := scanOp(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, op)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, op := range out {
			if _, err := tx.Exec(ctx, `UPDATE pending_operations SET next_attempt_at=$2 WHERE id=$1`, op.ID, now.Add(lease)); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// CompleteOperation marks work finished.
func (s *Store) CompleteOperation(ctx context.Context, tx Tx, id string) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE pending_operations SET completed_at=$2, last_error=NULL WHERE id=$1 AND completed_at IS NULL`, id, s.Now())
	return err
}

// CompleteOperationBySubject marks any active operation for a subject finished.
func (s *Store) CompleteOperationBySubject(ctx context.Context, tx Tx, kind domain.OperationKind, subjectID string) error {
	_, err := s.q(tx).Exec(ctx, `UPDATE pending_operations SET completed_at=$3, last_error=NULL WHERE kind=$1 AND subject_id=$2 AND completed_at IS NULL`,
		kind, subjectID, s.Now())
	return err
}

// FailOperation records an error and schedules the next retry.
func (s *Store) FailOperation(ctx context.Context, id string, errMsg string, retryIn time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE pending_operations SET attempts=attempts+1, last_error=$2, next_attempt_at=$3 WHERE id=$1`,
		id, errMsg, s.Now().Add(retryIn))
	return err
}

// ActiveOperation returns the active operation for a subject, or ErrNotFound.
func (s *Store) ActiveOperation(ctx context.Context, kind domain.OperationKind, subjectID string) (domain.PendingOperation, error) {
	return scanOp(s.pool.QueryRow(ctx, `SELECT `+opCols+` FROM pending_operations WHERE kind=$1 AND subject_id=$2 AND completed_at IS NULL`, kind, subjectID))
}
