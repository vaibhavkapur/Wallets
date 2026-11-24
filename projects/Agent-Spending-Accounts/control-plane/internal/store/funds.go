package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
)

// Ledger account names.
func acctAvailable(orgID string) string { return "org:" + orgID + ":available" }
func acctReserved(orgID string) string  { return "org:" + orgID + ":reserved" }
func acctSettlement(network string) string {
	return "settlement:" + network
}

const acctFunding = "funding:external"

// leg is one side of a journal.
type leg struct {
	account   string
	direction domain.EntryDirection
	amount    money.Amount
}

// postJournal writes a balanced set of ledger entries. It refuses unbalanced
// journals so that the ledger can always be reconciled.
func postJournal(ctx context.Context, q querier, now time.Time, orgID, asset, network, memo string, legs []leg) (string, error) {
	debits, credits := money.Zero(), money.Zero()
	var err error
	for _, l := range legs {
		switch l.direction {
		case domain.Debit:
			debits, err = debits.Add(l.amount)
		case domain.Credit:
			credits, err = credits.Add(l.amount)
		default:
			return "", fmt.Errorf("ledger: invalid direction %q", l.direction)
		}
		if err != nil {
			return "", err
		}
	}
	if debits.Cmp(credits) != 0 {
		return "", fmt.Errorf("ledger: unbalanced journal debits=%s credits=%s", debits, credits)
	}
	journalID := ids.New("jrn")
	for _, l := range legs {
		if l.amount.IsZero() {
			continue
		}
		if _, err := q.Exec(ctx, `INSERT INTO ledger_entries(id,org_id,journal_id,account,direction,amount,asset,network,memo,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			ids.New("led"), orgID, journalID, l.account, l.direction, l.amount.String(), asset, network, memo, now); err != nil {
			return "", err
		}
	}
	return journalID, nil
}

// lockBalance loads the balance row FOR UPDATE, creating it when absent.
func lockBalance(ctx context.Context, tx pgx.Tx, now time.Time, orgID, asset, network string) (domain.AccountBalance, error) {
	b := domain.AccountBalance{OrgID: orgID, Asset: asset, Network: network}
	err := tx.QueryRow(ctx, `SELECT available::text, reserved::text, updated_at FROM account_balances
		WHERE org_id=$1 AND asset=$2 AND network=$3 FOR UPDATE`, orgID, asset, network).Scan(&b.Available, &b.Reserved, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `INSERT INTO account_balances(org_id,asset,network,available,reserved,updated_at)
			VALUES ($1,$2,$3,0,0,$4) ON CONFLICT DO NOTHING`, orgID, asset, network, now); err != nil {
			return b, err
		}
		// Re-select to obtain the lock (another tx may have inserted it).
		err = tx.QueryRow(ctx, `SELECT available::text, reserved::text, updated_at FROM account_balances
			WHERE org_id=$1 AND asset=$2 AND network=$3 FOR UPDATE`, orgID, asset, network).Scan(&b.Available, &b.Reserved, &b.UpdatedAt)
	}
	return b, err
}

func saveBalance(ctx context.Context, tx pgx.Tx, now time.Time, b domain.AccountBalance) error {
	_, err := tx.Exec(ctx, `UPDATE account_balances SET available=$4, reserved=$5, updated_at=$6
		WHERE org_id=$1 AND asset=$2 AND network=$3`, b.OrgID, b.Asset, b.Network, b.Available.String(), b.Reserved.String(), now)
	return err
}

// GetBalance returns the balance for an org/asset/network (zero if none).
func (s *Store) GetBalance(ctx context.Context, orgID, asset, network string) (domain.AccountBalance, error) {
	b := domain.AccountBalance{OrgID: orgID, Asset: asset, Network: network, UpdatedAt: s.Now()}
	err := s.pool.QueryRow(ctx, `SELECT available::text, reserved::text, updated_at FROM account_balances
		WHERE org_id=$1 AND asset=$2 AND network=$3`, orgID, asset, network).Scan(&b.Available, &b.Reserved, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, nil
	}
	return b, err
}

// ListBalances returns all balances for an organization.
func (s *Store) ListBalances(ctx context.Context, orgID string) ([]domain.AccountBalance, error) {
	rows, err := s.pool.Query(ctx, `SELECT org_id, asset, network, available::text, reserved::text, updated_at
		FROM account_balances WHERE org_id=$1 ORDER BY asset, network`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AccountBalance
	for rows.Next() {
		var b domain.AccountBalance
		if err := rows.Scan(&b.OrgID, &b.Asset, &b.Network, &b.Available, &b.Reserved, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Deposit credits owner-funded balance. The provider funding flow is
// confirmed before this is called; it records the accounting consequence.
func (s *Store) Deposit(ctx context.Context, orgID, asset, network string, amount money.Amount, reference, actor string) (domain.AccountBalance, error) {
	now := s.Now()
	var out domain.AccountBalance
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		b, err := lockBalance(ctx, tx, now, orgID, asset, network)
		if err != nil {
			return err
		}
		if b.Available, err = b.Available.Add(amount); err != nil {
			return err
		}
		if err := saveBalance(ctx, tx, now, b); err != nil {
			return err
		}
		jrn, err := postJournal(ctx, tx, now, orgID, asset, network, "deposit "+reference, []leg{
			{acctFunding, domain.Debit, amount},
			{acctAvailable(orgID), domain.Credit, amount},
		})
		if err != nil {
			return err
		}
		out = b
		return appendAudit(ctx, tx, now, orgID, actor, "funds.deposit", "journal", jrn, map[string]any{"amount": amount, "reference": reference})
	})
	return out, err
}

// Withdraw debits available balance. Reserved funds cannot be withdrawn.
func (s *Store) Withdraw(ctx context.Context, orgID, asset, network string, amount money.Amount, reference, actor string) (domain.AccountBalance, error) {
	now := s.Now()
	var out domain.AccountBalance
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		b, err := lockBalance(ctx, tx, now, orgID, asset, network)
		if err != nil {
			return err
		}
		if b.Available, err = b.Available.Sub(amount); err != nil {
			return fmt.Errorf("%w: insufficient available balance", ErrConflict)
		}
		if err := saveBalance(ctx, tx, now, b); err != nil {
			return err
		}
		jrn, err := postJournal(ctx, tx, now, orgID, asset, network, "withdrawal "+reference, []leg{
			{acctAvailable(orgID), domain.Debit, amount},
			{acctFunding, domain.Credit, amount},
		})
		if err != nil {
			return err
		}
		out = b
		return appendAudit(ctx, tx, now, orgID, actor, "funds.withdraw", "journal", jrn, map[string]any{"amount": amount, "reference": reference})
	})
	return out, err
}

// ListLedger returns recent ledger entries for an organization.
func (s *Store) ListLedger(ctx context.Context, orgID string, limit int) ([]domain.LedgerEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,org_id,journal_id,account,direction,amount::text,asset,network,memo,created_at
		FROM ledger_entries WHERE org_id=$1 ORDER BY created_at DESC, id LIMIT $2`, orgID, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.LedgerEntry
	for rows.Next() {
		var e domain.LedgerEntry
		if err := rows.Scan(&e.ID, &e.OrgID, &e.JournalID, &e.Account, &e.Direction, &e.Amount, &e.Asset, &e.Network, &e.Memo, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LedgerInvariant checks that, for an org/asset/network, the ledger balances
// equal the denormalized balance row. Used by tests and the reconciliation worker.
func (s *Store) LedgerInvariant(ctx context.Context, orgID, asset, network string) error {
	var available, reserved money.Amount
	err := s.pool.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN account=$4 AND direction='credit' THEN amount WHEN account=$4 AND direction='debit' THEN -amount ELSE 0 END),0)::text,
		COALESCE(SUM(CASE WHEN account=$5 AND direction='credit' THEN amount WHEN account=$5 AND direction='debit' THEN -amount ELSE 0 END),0)::text
		FROM ledger_entries WHERE org_id=$1 AND asset=$2 AND network=$3`,
		orgID, asset, network, acctAvailable(orgID), acctReserved(orgID)).Scan(&available, &reserved)
	if err != nil {
		return fmt.Errorf("ledger sums: %w", err)
	}
	b, err := s.GetBalance(ctx, orgID, asset, network)
	if err != nil {
		return err
	}
	if b.Available.Cmp(available) != 0 || b.Reserved.Cmp(reserved) != 0 {
		return fmt.Errorf("ledger mismatch: balance available=%s reserved=%s, ledger available=%s reserved=%s",
			b.Available, b.Reserved, available, reserved)
	}
	return nil
}
