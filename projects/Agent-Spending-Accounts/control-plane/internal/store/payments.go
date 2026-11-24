package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/policy"
)

const attemptCols = `id,org_id,agent_id,task_id,policy_version_id,COALESCE(reservation_id,''),COALESCE(grant_id,''),
	idempotency_key,method,url,quote,status,reject_reason,submission_ref,payment_hash,payer_ref,
	settled_amount::text,fee_actual::text,error,created_at,updated_at`

func scanAttempt(row pgx.Row) (domain.PaymentAttempt, error) {
	var a domain.PaymentAttempt
	var quote []byte
	err := row.Scan(&a.ID, &a.OrgID, &a.AgentID, &a.TaskID, &a.PolicyVersionID, &a.ReservationID, &a.GrantID,
		&a.IdempotencyKey, &a.Method, &a.URL, &quote, &a.Status, &a.RejectReason, &a.SubmissionRef, &a.PaymentHash, &a.PayerRef,
		&a.SettledAmount, &a.FeeActual, &a.Error, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	if len(quote) > 0 {
		var q domain.ResourceQuote
		if err := json.Unmarshal(quote, &q); err != nil {
			return a, fmt.Errorf("decode quote: %w", err)
		}
		a.Quote = &q
	}
	return a, nil
}

// CreatePaymentAttempt inserts an attempt or returns the existing one for the
// same task + idempotency key. created=false signals an idempotent replay.
func (s *Store) CreatePaymentAttempt(ctx context.Context, a domain.PaymentAttempt) (domain.PaymentAttempt, bool, error) {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO payment_attempts(id,org_id,agent_id,task_id,policy_version_id,idempotency_key,method,url,status,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			a.ID, a.OrgID, a.AgentID, a.TaskID, a.PolicyVersionID, a.IdempotencyKey, a.Method, a.URL, a.Status, a.CreatedAt, a.UpdatedAt); err != nil {
			return err
		}
		return appendAudit(ctx, tx, a.CreatedAt, a.OrgID, "agent:"+a.AgentID, "payment.initiated", "payment_attempt", a.ID,
			map[string]string{"url": a.URL, "method": a.Method, "idempotency_key": a.IdempotencyKey})
	})
	if err == nil {
		return a, true, nil
	}
	if !isUniqueViolation(err) {
		return a, false, err
	}
	existing, err := scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE task_id=$1 AND idempotency_key=$2`, a.TaskID, a.IdempotencyKey))
	return existing, false, err
}

// GetPaymentAttempt loads an attempt scoped to an organization.
func (s *Store) GetPaymentAttempt(ctx context.Context, orgID, id string) (domain.PaymentAttempt, error) {
	return scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE id=$1 AND org_id=$2`, id, orgID))
}

// GetPaymentAttemptAny loads an attempt by id without tenant scoping. Only
// the internal (gateway) API uses it, after the gateway obtained the id from
// this control plane.
func (s *Store) GetPaymentAttemptAny(ctx context.Context, id string) (domain.PaymentAttempt, error) {
	return scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE id=$1`, id))
}

// ListPaymentAttempts lists attempts for an org, optionally for one task.
func (s *Store) ListPaymentAttempts(ctx context.Context, orgID, taskID string, limit int) ([]domain.PaymentAttempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE org_id=$1 AND ($2='' OR task_id=$2)
		ORDER BY created_at DESC LIMIT $3`, orgID, taskID, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAttempts(rows)
}

func collectAttempts(rows pgx.Rows) ([]domain.PaymentAttempt, error) {
	var out []domain.PaymentAttempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAttemptsForRecovery returns attempts in the given statuses whose last
// update is older than `before`. Used by the recovery and reconciliation workers.
func (s *Store) ListAttemptsForRecovery(ctx context.Context, statuses []domain.PaymentStatus, before time.Time, limit int) ([]domain.PaymentAttempt, error) {
	strs := make([]string, len(statuses))
	for i, st := range statuses {
		strs[i] = string(st)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE status = ANY($1) AND updated_at < $2
		ORDER BY updated_at LIMIT $3`, strs, before, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAttempts(rows)
}

// ListExpiredAuthorized returns authorized attempts whose grant expired before
// `before` and that never recorded a submission.
func (s *Store) ListExpiredAuthorized(ctx context.Context, before time.Time, limit int) ([]domain.PaymentAttempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+attemptColsPrefixed+` FROM payment_attempts a
		JOIN execution_grants g ON g.id = a.grant_id
		WHERE a.status='authorized' AND g.consumed_at IS NULL AND g.expires_at < $1
		ORDER BY g.expires_at LIMIT $2`, before, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAttempts(rows)
}

const attemptColsPrefixed = `a.id,a.org_id,a.agent_id,a.task_id,a.policy_version_id,COALESCE(a.reservation_id,''),COALESCE(a.grant_id,''),
	a.idempotency_key,a.method,a.url,a.quote,a.status,a.reject_reason,a.submission_ref,a.payment_hash,a.payer_ref,
	a.settled_amount::text,a.fee_actual::text,a.error,a.created_at,a.updated_at`

// RejectAttempt records a policy denial for an attempt that holds no reservation.
func (s *Store) RejectAttempt(ctx context.Context, attemptID string, code policy.Code, detail string) error {
	now := s.Now()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var orgID string
		err := tx.QueryRow(ctx, `UPDATE payment_attempts SET status='rejected', reject_reason=$2, error=$3, updated_at=$4
			WHERE id=$1 AND status='initiated' RETURNING org_id`, attemptID, string(code), detail, now).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, now, orgID, "gateway", "payment.rejected", "payment_attempt", attemptID, map[string]string{"code": string(code), "detail": detail})
	})
}

// --- Reserve ----------------------------------------------------------------

// ReserveParams carries the validated quote for an initiated attempt.
type ReserveParams struct {
	AttemptID string
	Quote     domain.ResourceQuote
}

// ReserveResult is returned on success; the grant token is signed by the
// service layer after commit (the token itself is never stored).
type ReserveResult struct {
	Attempt     domain.PaymentAttempt
	Reservation domain.Reservation
	Grant       domain.ExecutionGrant
	Policy      domain.BudgetPolicyVersion
	Rule        domain.EndpointRule
}

// Reserve atomically evaluates policy and holds amount+fee across the task,
// agent-daily, org-daily, and balance budgets.
//
// Lock order (always): organization -> agent -> task -> balance. Every
// reservation for an org serializes on the organization row, so concurrent
// requests cannot both observe the same available capacity.
//
// A policy.Denial error means the attempt was marked rejected in the same
// transaction (nothing is held). Other errors leave the attempt untouched.
func (s *Store) Reserve(ctx context.Context, p ReserveParams) (ReserveResult, error) {
	now := s.Now()
	var res ReserveResult
	var denial *policy.Denial

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		attempt, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE id=$1 FOR UPDATE`, p.AttemptID))
		if err != nil {
			return err
		}
		if attempt.Status != domain.PaymentInitiated {
			return fmt.Errorf("%w: attempt %s is %s", ErrConflict, attempt.ID, attempt.Status)
		}
		org, err := scanOrg(tx.QueryRow(ctx, `SELECT `+orgCols+` FROM organizations WHERE id=$1 FOR UPDATE`, attempt.OrgID))
		if err != nil {
			return err
		}
		agent, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentCols+` FROM agents WHERE id=$1 FOR UPDATE`, attempt.AgentID))
		if err != nil {
			return err
		}
		task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1 FOR UPDATE`, attempt.TaskID))
		if err != nil {
			return err
		}
		pol, err := scanPolicy(tx.QueryRow(ctx, `SELECT `+policyCols+` FROM policy_versions WHERE id=$1`, attempt.PolicyVersionID))
		if err != nil {
			return err
		}

		reject := func(perr error) error {
			d, ok := policy.AsDenial(perr)
			if !ok {
				return perr
			}
			denial = d
			if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='rejected', reject_reason=$2, error=$3, quote=$4, updated_at=$5 WHERE id=$1`,
				attempt.ID, string(d.Code), d.Detail, mustJSON(p.Quote), now); err != nil {
				return err
			}
			return appendAudit(ctx, tx, now, org.ID, "gateway", "payment.rejected", "payment_attempt", attempt.ID,
				map[string]string{"code": string(d.Code), "detail": d.Detail})
		}

		if err := policy.CheckPrincipals(policy.Principals{Org: org, Agent: agent, Task: task}, now); err != nil {
			return reject(err)
		}
		// The agent's active policy must still be the one the task was created
		// under; owners change limits by issuing a new version, which closes
		// the door for in-flight tasks on older versions.
		if agent.PolicyVersionID != task.PolicyVersionID {
			return reject(&policy.Denial{Code: policy.CodeTaskClosed, Detail: "task policy version superseded; create a new task"})
		}
		rule, err := policy.MatchEndpoint(pol, attempt.Method, attempt.URL)
		if err != nil {
			return reject(err)
		}
		if err := policy.ValidateQuote(pol, rule, attempt.URL, p.Quote); err != nil {
			return reject(err)
		}

		bal, err := lockBalance(ctx, tx, now, org.ID, pol.Asset.Asset, pol.Asset.Network)
		if err != nil {
			return err
		}
		usage, err := computeUsage(ctx, tx, org.ID, agent.ID, task.ID, now)
		if err != nil {
			return err
		}
		usage.OrgAvailable = bal.Available

		total, err := policy.CheckBudgets(pol, task, usage, p.Quote.MaxAmountRequired, pol.FeeAllowance)
		if err != nil {
			return reject(err)
		}

		// Hold funds: available -> reserved.
		if bal.Available, err = bal.Available.Sub(total); err != nil {
			return reject(&policy.Denial{Code: policy.CodeInsufficientBalance, Detail: err.Error()})
		}
		if bal.Reserved, err = bal.Reserved.Add(total); err != nil {
			return err
		}
		if err := saveBalance(ctx, tx, now, bal); err != nil {
			return err
		}

		ttl := time.Duration(pol.GrantTTLSeconds) * time.Second
		resv := domain.Reservation{
			ID: ids.New("rsv"), OrgID: org.ID, AgentID: agent.ID, TaskID: task.ID, PolicyVersionID: pol.ID,
			PaymentAttemptID: attempt.ID, Amount: p.Quote.MaxAmountRequired, FeeAllowance: pol.FeeAllowance,
			Status: domain.ReservationHeld, ExpiresAt: now.Add(ttl), CreatedAt: now,
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reservations(id,org_id,agent_id,task_id,policy_version_id,payment_attempt_id,amount,fee_allowance,status,expires_at,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			resv.ID, resv.OrgID, resv.AgentID, resv.TaskID, resv.PolicyVersionID, resv.PaymentAttemptID,
			resv.Amount.String(), resv.FeeAllowance.String(), resv.Status, resv.ExpiresAt, resv.CreatedAt); err != nil {
			return err
		}
		if _, err := postJournal(ctx, tx, now, org.ID, pol.Asset.Asset, pol.Asset.Network, "reserve "+resv.ID, []leg{
			{acctAvailable(org.ID), domain.Debit, total},
			{acctReserved(org.ID), domain.Credit, total},
		}); err != nil {
			return err
		}
		grant := domain.ExecutionGrant{ID: ids.New("grt"), ReservationID: resv.ID, PaymentAttemptID: attempt.ID, ExpiresAt: resv.ExpiresAt, IssuedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO execution_grants(id,reservation_id,payment_attempt_id,expires_at,issued_at) VALUES ($1,$2,$3,$4,$5)`,
			grant.ID, grant.ReservationID, grant.PaymentAttemptID, grant.ExpiresAt, grant.IssuedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='authorized', quote=$2, reservation_id=$3, grant_id=$4, updated_at=$5 WHERE id=$1`,
			attempt.ID, mustJSON(p.Quote), resv.ID, grant.ID, now); err != nil {
			return err
		}
		attempt.Status = domain.PaymentAuthorized
		attempt.Quote = &p.Quote
		attempt.ReservationID = resv.ID
		attempt.GrantID = grant.ID
		attempt.UpdatedAt = now

		if err := appendAudit(ctx, tx, now, org.ID, "gateway", "payment.authorized", "payment_attempt", attempt.ID, map[string]any{
			"reservation_id": resv.ID, "grant_id": grant.ID, "amount": resv.Amount, "fee_allowance": resv.FeeAllowance,
			"pay_to": p.Quote.PayTo, "policy_version_id": pol.ID, "expires_at": resv.ExpiresAt,
		}); err != nil {
			return err
		}
		if err := enqueueOutbox(ctx, tx, now, "reservation.held", resv); err != nil {
			return err
		}
		res = ReserveResult{Attempt: attempt, Reservation: resv, Grant: grant, Policy: pol, Rule: *rule}
		return nil
	})
	if err != nil {
		return res, err
	}
	if denial != nil {
		return res, denial
	}
	return res, nil
}

// computeUsage sums held and committed reservations for the current UTC day
// (org and agent) and for the whole task. Must be called under the org lock.
func computeUsage(ctx context.Context, tx pgx.Tx, orgID, agentID, taskID string, now time.Time) (policy.Usage, error) {
	dayStart := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	const used = `COALESCE(SUM(CASE WHEN status='held' THEN amount+fee_allowance
		WHEN status='committed' THEN committed_amount+committed_fee ELSE 0 END),0)::text`
	var u policy.Usage
	if err := tx.QueryRow(ctx, `SELECT `+used+` FROM reservations WHERE org_id=$1 AND created_at>=$2`, orgID, dayStart).Scan(&u.OrgDailyUsed); err != nil {
		return u, err
	}
	if err := tx.QueryRow(ctx, `SELECT `+used+` FROM reservations WHERE agent_id=$1 AND created_at>=$2`, agentID, dayStart).Scan(&u.AgentDailyUsed); err != nil {
		return u, err
	}
	if err := tx.QueryRow(ctx, `SELECT `+used+` FROM reservations WHERE task_id=$1`, taskID).Scan(&u.TaskUsed); err != nil {
		return u, err
	}
	return u, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// --- Submission (write-ahead) -----------------------------------------------

// MarkSubmitted records that the gateway is about to transmit the payment.
// It consumes the grant: a second call for the same attempt is a conflict,
// which is how grant replay is detected server-side.
func (s *Store) MarkSubmitted(ctx context.Context, attemptID, grantID, paymentHash string) (domain.PaymentAttempt, error) {
	now := s.Now()
	var out domain.PaymentAttempt
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		attempt, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE id=$1 FOR UPDATE`, attemptID))
		if err != nil {
			return err
		}
		if attempt.Status != domain.PaymentAuthorized {
			return fmt.Errorf("%w: attempt %s is %s, not authorized", ErrConflict, attempt.ID, attempt.Status)
		}
		if attempt.GrantID != grantID {
			return fmt.Errorf("%w: grant mismatch", ErrForbidden)
		}
		var expiresAt time.Time
		var consumedAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT expires_at, consumed_at FROM execution_grants WHERE id=$1 FOR UPDATE`, grantID).Scan(&expiresAt, &consumedAt); err != nil {
			return err
		}
		if consumedAt != nil {
			return fmt.Errorf("%w: grant already consumed", ErrConflict)
		}
		if !now.Before(expiresAt) {
			return fmt.Errorf("%w: grant expired", ErrConflict)
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_grants SET consumed_at=$2 WHERE id=$1`, grantID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='submitted', payment_hash=$2, updated_at=$3 WHERE id=$1`, attemptID, paymentHash, now); err != nil {
			return err
		}
		attempt.Status = domain.PaymentSubmitted
		attempt.PaymentHash = paymentHash
		attempt.UpdatedAt = now
		out = attempt
		return appendAudit(ctx, tx, now, attempt.OrgID, "gateway", "payment.submitted", "payment_attempt", attempt.ID, map[string]string{"grant_id": grantID, "payment_hash": paymentHash})
	})
	return out, err
}

// --- Resolution -------------------------------------------------------------

// Outcome is what the gateway (or the reconciliation worker) learned.
type Outcome struct {
	Status        domain.PaymentStatus // settled | failed | unknown
	SubmissionRef string
	PayerRef      string
	SettledAmount money.Amount // required for settled; must be <= reservation amount
	FeeActual     money.Amount // must be <= fee allowance
	Error         string
	Service       *domain.ServiceOutcome
	Actor         string
}

// ResolveAttempt applies a payment outcome:
//   - settled: commit the reservation (actual cost + fee), release the remainder, write a receipt
//   - failed:  release the reservation entirely (only legal from authorized/submitted when definitively unpaid)
//   - unknown: keep the reservation held and mark the attempt for reconciliation
//
// Transitions are checked so that a settled attempt can never be released
// and an unknown one is never released without reconciliation evidence.
func (s *Store) ResolveAttempt(ctx context.Context, attemptID string, o Outcome) (domain.PaymentAttempt, *domain.Receipt, error) {
	now := s.Now()
	var outAttempt domain.PaymentAttempt
	var outReceipt *domain.Receipt
	actor := o.Actor
	if actor == "" {
		actor = "gateway"
	}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		attempt, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptCols+` FROM payment_attempts WHERE id=$1 FOR UPDATE`, attemptID))
		if err != nil {
			return err
		}
		switch attempt.Status {
		case domain.PaymentAuthorized, domain.PaymentSubmitted, domain.PaymentUnknown:
		default:
			return fmt.Errorf("%w: attempt %s is already %s", ErrConflict, attempt.ID, attempt.Status)
		}
		if attempt.Status == domain.PaymentAuthorized && o.Status == domain.PaymentSettled {
			return fmt.Errorf("%w: settlement reported without prior submission", ErrConflict)
		}
		var resv domain.Reservation
		if err := tx.QueryRow(ctx, `SELECT id,org_id,agent_id,task_id,policy_version_id,amount::text,fee_allowance::text,status,expires_at,created_at
			FROM reservations WHERE id=$1 FOR UPDATE`, attempt.ReservationID).
			Scan(&resv.ID, &resv.OrgID, &resv.AgentID, &resv.TaskID, &resv.PolicyVersionID, &resv.Amount, &resv.FeeAllowance, &resv.Status, &resv.ExpiresAt, &resv.CreatedAt); err != nil {
			return err
		}
		if resv.Status != domain.ReservationHeld {
			return fmt.Errorf("%w: reservation %s is %s", ErrConflict, resv.ID, resv.Status)
		}
		pol, err := scanPolicy(tx.QueryRow(ctx, `SELECT `+policyCols+` FROM policy_versions WHERE id=$1`, attempt.PolicyVersionID))
		if err != nil {
			return err
		}
		// Lock org then balance to respect the global lock order.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id=$1 FOR UPDATE`, attempt.OrgID); err != nil {
			return err
		}
		held, err := resv.Amount.Add(resv.FeeAllowance)
		if err != nil {
			return err
		}
		service := nullableJSON(nil)
		if o.Service != nil {
			service = mustJSON(o.Service)
		}

		switch o.Status {
		case domain.PaymentSettled:
			if o.SettledAmount.Cmp(resv.Amount) > 0 {
				return fmt.Errorf("%w: settled amount %s exceeds reserved %s", ErrConflict, o.SettledAmount, resv.Amount)
			}
			if o.FeeActual.Cmp(resv.FeeAllowance) > 0 {
				return fmt.Errorf("%w: fee %s exceeds allowance %s", ErrConflict, o.FeeActual, resv.FeeAllowance)
			}
			spent, err := o.SettledAmount.Add(o.FeeActual)
			if err != nil {
				return err
			}
			unused, err := held.Sub(spent)
			if err != nil {
				return err
			}
			bal, err := lockBalance(ctx, tx, now, attempt.OrgID, pol.Asset.Asset, pol.Asset.Network)
			if err != nil {
				return err
			}
			if bal.Reserved, err = bal.Reserved.Sub(held); err != nil {
				return fmt.Errorf("reserved balance underflow: %w", err)
			}
			if bal.Available, err = bal.Available.Add(unused); err != nil {
				return err
			}
			if err := saveBalance(ctx, tx, now, bal); err != nil {
				return err
			}
			if _, err := postJournal(ctx, tx, now, attempt.OrgID, pol.Asset.Asset, pol.Asset.Network, "settle "+resv.ID, []leg{
				{acctReserved(attempt.OrgID), domain.Debit, held},
				{acctSettlement(pol.Asset.Network), domain.Credit, spent},
				{acctAvailable(attempt.OrgID), domain.Credit, unused},
			}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET status='committed', committed_amount=$2, committed_fee=$3, resolved_at=$4, resolution_reason='settled' WHERE id=$1`,
				resv.ID, o.SettledAmount.String(), o.FeeActual.String(), now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='settled', submission_ref=$2, payer_ref=$3, settled_amount=$4, fee_actual=$5, error=$6, updated_at=$7 WHERE id=$1`,
				attempt.ID, o.SubmissionRef, o.PayerRef, o.SettledAmount.String(), o.FeeActual.String(), o.Error, now); err != nil {
				return err
			}
			attempt.Status = domain.PaymentSettled
			attempt.SubmissionRef = o.SubmissionRef
			attempt.PayerRef = o.PayerRef
			attempt.SettledAmount = o.SettledAmount
			attempt.FeeActual = o.FeeActual

		case domain.PaymentFailed, domain.PaymentExpired:
			bal, err := lockBalance(ctx, tx, now, attempt.OrgID, pol.Asset.Asset, pol.Asset.Network)
			if err != nil {
				return err
			}
			if bal.Reserved, err = bal.Reserved.Sub(held); err != nil {
				return fmt.Errorf("reserved balance underflow: %w", err)
			}
			if bal.Available, err = bal.Available.Add(held); err != nil {
				return err
			}
			if err := saveBalance(ctx, tx, now, bal); err != nil {
				return err
			}
			if _, err := postJournal(ctx, tx, now, attempt.OrgID, pol.Asset.Asset, pol.Asset.Network, "release "+resv.ID, []leg{
				{acctReserved(attempt.OrgID), domain.Debit, held},
				{acctAvailable(attempt.OrgID), domain.Credit, held},
			}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET status='released', resolved_at=$2, resolution_reason=$3 WHERE id=$1`, resv.ID, now, string(o.Status)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status=$2, submission_ref=$3, error=$4, updated_at=$5 WHERE id=$1`,
				attempt.ID, string(o.Status), o.SubmissionRef, o.Error, now); err != nil {
				return err
			}
			attempt.Status = o.Status
			attempt.Error = o.Error

		case domain.PaymentUnknown:
			if attempt.Status == domain.PaymentAuthorized {
				return fmt.Errorf("%w: unknown outcome requires prior submission", ErrConflict)
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='unknown', submission_ref=$2, error=$3, updated_at=$4 WHERE id=$1`,
				attempt.ID, o.SubmissionRef, o.Error, now); err != nil {
				return err
			}
			attempt.Status = domain.PaymentUnknown
			attempt.Error = o.Error
		default:
			return fmt.Errorf("%w: invalid outcome status %q", ErrConflict, o.Status)
		}
		attempt.UpdatedAt = now

		// A receipt is written for every terminal outcome and also for unknown
		// (so the service outcome is retained); it is upserted on reconciliation.
		if attempt.Quote != nil {
			rc := domain.Receipt{
				ID: ids.New("rcp"), OrgID: attempt.OrgID, TaskID: attempt.TaskID, AgentID: attempt.AgentID,
				PaymentAttemptID: attempt.ID, ReservationID: resv.ID, PolicyVersionID: attempt.PolicyVersionID,
				Resource: attempt.URL, Quote: *attempt.Quote, PaymentStatus: attempt.Status, SubmissionRef: attempt.SubmissionRef,
				SettledAmount: attempt.SettledAmount, FeeActual: attempt.FeeActual, Service: o.Service, CreatedAt: now,
			}
			var storedService []byte
			if err := tx.QueryRow(ctx, `INSERT INTO receipts(id,org_id,task_id,agent_id,payment_attempt_id,reservation_id,policy_version_id,resource,quote,payment_status,submission_ref,settled_amount,fee_actual,service,created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
				ON CONFLICT (payment_attempt_id) DO UPDATE SET payment_status=EXCLUDED.payment_status, submission_ref=EXCLUDED.submission_ref,
					settled_amount=EXCLUDED.settled_amount, fee_actual=EXCLUDED.fee_actual,
					service=COALESCE(EXCLUDED.service, receipts.service)
				RETURNING id, service`,
				rc.ID, rc.OrgID, rc.TaskID, rc.AgentID, rc.PaymentAttemptID, rc.ReservationID, rc.PolicyVersionID, rc.Resource, mustJSON(rc.Quote),
				rc.PaymentStatus, rc.SubmissionRef, rc.SettledAmount.String(), rc.FeeActual.String(), service, rc.CreatedAt).Scan(&rc.ID, &storedService); err != nil {
				return err
			}
			if len(storedService) > 0 {
				var so domain.ServiceOutcome
				if err := json.Unmarshal(storedService, &so); err == nil {
					rc.Service = &so
				}
			}
			outReceipt = &rc
		}
		if err := appendAudit(ctx, tx, now, attempt.OrgID, actor, "payment."+string(attempt.Status), "payment_attempt", attempt.ID, map[string]any{
			"reservation_id": resv.ID, "submission_ref": o.SubmissionRef, "settled_amount": o.SettledAmount, "fee_actual": o.FeeActual, "error": o.Error,
		}); err != nil {
			return err
		}
		if err := enqueueOutbox(ctx, tx, now, "payment."+string(attempt.Status), attempt); err != nil {
			return err
		}
		outAttempt = attempt
		return nil
	})
	return outAttempt, outReceipt, err
}

// --- Receipts ---------------------------------------------------------------

const receiptCols = `id,org_id,task_id,agent_id,payment_attempt_id,reservation_id,policy_version_id,resource,quote,payment_status,submission_ref,settled_amount::text,fee_actual::text,service,created_at`

func scanReceipt(row pgx.Row) (domain.Receipt, error) {
	var r domain.Receipt
	var quote, service []byte
	err := row.Scan(&r.ID, &r.OrgID, &r.TaskID, &r.AgentID, &r.PaymentAttemptID, &r.ReservationID, &r.PolicyVersionID, &r.Resource, &quote,
		&r.PaymentStatus, &r.SubmissionRef, &r.SettledAmount, &r.FeeActual, &service, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(quote, &r.Quote); err != nil {
		return r, err
	}
	if len(service) > 0 {
		var so domain.ServiceOutcome
		if err := json.Unmarshal(service, &so); err != nil {
			return r, err
		}
		r.Service = &so
	}
	return r, nil
}

// ListReceipts returns receipts for an org, optionally for one task.
func (s *Store) ListReceipts(ctx context.Context, orgID, taskID string, limit int) ([]domain.Receipt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+receiptCols+` FROM receipts WHERE org_id=$1 AND ($2='' OR task_id=$2) ORDER BY created_at DESC LIMIT $3`,
		orgID, taskID, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Receipt
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReservation loads a reservation scoped to an org.
func (s *Store) GetReservation(ctx context.Context, orgID, id string) (domain.Reservation, error) {
	var r domain.Reservation
	err := s.pool.QueryRow(ctx, `SELECT id,org_id,agent_id,task_id,policy_version_id,payment_attempt_id,amount::text,fee_allowance::text,status,
		committed_amount::text,committed_fee::text,expires_at,created_at,resolved_at,resolution_reason FROM reservations WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&r.ID, &r.OrgID, &r.AgentID, &r.TaskID, &r.PolicyVersionID, &r.PaymentAttemptID, &r.Amount, &r.FeeAllowance, &r.Status,
			&r.CommittedAmount, &r.CommittedFee, &r.ExpiresAt, &r.CreatedAt, &r.ResolvedAt, &r.ResolutionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}
