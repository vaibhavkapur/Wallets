package rewards

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"unowallet/rewards/internal/money"
)

var (
	ErrUnbalancedJournal = errors.New("journal entry does not balance by asset")
	ErrInvalidPosting    = errors.New("journal posting must have positive amount and valid direction")
	ErrEmptyJournal      = errors.New("journal entry must contain at least two postings")
)

type AccountCategory string

const (
	CatAsset     AccountCategory = "asset"
	CatLiability AccountCategory = "liability"
	CatEquity    AccountCategory = "equity"
	CatRevenue   AccountCategory = "revenue"
	CatExpense   AccountCategory = "expense"
	CatClearing  AccountCategory = "clearing"
)

// Chart of accounts separating merchant funding, reserved campaign amounts,
// pending/payable reward obligations, payout clearing, loss reserves, fees, and platform revenue.
const (
	AcctProviderCustody        = "asset:provider_custody"
	AcctCampaignAvailable      = "liability:campaign_budget_available"
	AcctCampaignReserved       = "liability:campaign_reserved"
	AcctRewardPending          = "liability:reward_pending"
	AcctRewardPayable          = "liability:reward_payable"
	AcctPayoutClearing         = "liability:payout_clearing"
	AcctLossReserve            = "liability:loss_reserve"
	AcctPlatformRevenue        = "revenue:platform_fees"
	AcctProviderFeesPayable    = "liability:provider_fees"
	AcctPayoutFeeExpense       = "expense:payout_fees"
	AcctLossReserveExpense     = "expense:chargeback_losses"
	AcctFXClearingUSD          = "clearing:fx_usd"
	AcctFXClearingUSDC         = "clearing:fx_usdc"
	AcctExternalPayoutSettled  = "clearing:external_payout_settled"
)

type PostingDirection string

const (
	Debit  PostingDirection = "debit"
	Credit PostingDirection = "credit"
)

type LedgerAccount struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Code      string          `json:"code"`
	Category  AccountCategory `json:"category"`
	ScopeID   string          `json:"scope_id"` // e.g., campaign_id, merchant_id, customer_id, or "global"
	Asset     string          `json:"asset"`
	Network   string          `json:"network"`
	Decimals  int             `json:"decimals"`
	Balance   money.Amount    `json:"balance"` // Normal balance (Debit-Credit for Asset/Expense; Credit-Debit for Liability/Equity/Revenue/Clearing)
	CreatedAt time.Time       `json:"created_at"`
}

type JournalPosting struct {
	AccountID   string           `json:"account_id"`
	AccountCode string           `json:"account_code"`
	ScopeID     string           `json:"scope_id"`
	Asset       string           `json:"asset"`
	Network     string           `json:"network"`
	Decimals    int              `json:"decimals"`
	Direction   PostingDirection `json:"direction"`
	Amount      money.Amount     `json:"amount"`
}

type JournalEntry struct {
	ID             string           `json:"id"`
	TenantID       string           `json:"tenant_id"`
	IdempotencyKey string           `json:"idempotency_key"`
	EntryType      string           `json:"entry_type"`
	ReferenceID    string           `json:"reference_id"`
	Description    string           `json:"description"`
	Postings       []JournalPosting `json:"postings"`
	CreatedAt      time.Time        `json:"created_at"`
}

func accountKey(code, scopeID, asset, network string) string {
	if scopeID == "" {
		scopeID = "global"
	}
	return fmt.Sprintf("%s|%s|%s|%s", code, scopeID, asset, network)
}

func accountCategoryForCode(code string) AccountCategory {
	switch code {
	case AcctProviderCustody:
		return CatAsset
	case AcctPlatformRevenue:
		return CatRevenue
	case AcctPayoutFeeExpense, AcctLossReserveExpense:
		return CatExpense
	case AcctFXClearingUSD, AcctFXClearingUSDC, AcctExternalPayoutSettled:
		return CatClearing
	default:
		return CatLiability
	}
}

func isDebitNormal(cat AccountCategory) bool {
	return cat == CatAsset || cat == CatExpense
}

func ensureAccount(s *State, code, scopeID, asset, network string, decimals int, now time.Time) *LedgerAccount {
	if scopeID == "" {
		scopeID = "global"
	}
	id := accountKey(code, scopeID, asset, network)
	if acct, ok := s.LedgerAccounts[id]; ok {
		return acct
	}
	acct := &LedgerAccount{
		ID:        id,
		TenantID:  s.TenantID,
		Code:      code,
		Category:  accountCategoryForCode(code),
		ScopeID:   scopeID,
		Asset:     asset,
		Network:   network,
		Decimals:  decimals,
		Balance:   0,
		CreatedAt: now,
	}
	s.LedgerAccounts[id] = acct
	return acct
}

// ValidateJournalBalanced checks that for every asset/network in the entry,
// total debits strictly equal total credits, and all amounts are positive.
func ValidateJournalBalanced(postings []JournalPosting) error {
	if len(postings) < 2 {
		return ErrEmptyJournal
	}
	type assetKey struct {
		asset   string
		network string
	}
	debits := map[assetKey]money.Amount{}
	credits := map[assetKey]money.Amount{}
	keys := map[assetKey]bool{}

	for _, p := range postings {
		if p.Amount <= 0 {
			return fmt.Errorf("%w: amount=%s on %s", ErrInvalidPosting, p.Amount.String(), p.AccountCode)
		}
		k := assetKey{asset: p.Asset, network: p.Network}
		keys[k] = true
		switch p.Direction {
		case Debit:
			sum, err := money.CheckedAdd(debits[k], p.Amount)
			if err != nil {
				return err
			}
			debits[k] = sum
		case Credit:
			sum, err := money.CheckedAdd(credits[k], p.Amount)
			if err != nil {
				return err
			}
			credits[k] = sum
		default:
			return fmt.Errorf("%w: direction=%s", ErrInvalidPosting, p.Direction)
		}
	}

	for k := range keys {
		if debits[k] != credits[k] {
			return fmt.Errorf("%w: asset=%s/%s debits=%s credits=%s",
				ErrUnbalancedJournal, k.asset, k.network, debits[k].String(), credits[k].String())
		}
	}
	return nil
}

// PostJournal atomically appends an idempotent, balanced journal entry and updates account normal balances.
func PostJournal(s *State, idempotencyKey, entryType, refID, description string, postings []JournalPosting, now time.Time) (*JournalEntry, error) {
	if existingID, ok := s.JournalByIdempotency[idempotencyKey]; ok {
		for _, je := range s.JournalEntries {
			if je.ID == existingID {
				return je, nil
			}
		}
	}
	if err := ValidateJournalBalanced(postings); err != nil {
		return nil, err
	}

	resolved := make([]JournalPosting, len(postings))
	for i, p := range postings {
		acct := ensureAccount(s, p.AccountCode, p.ScopeID, p.Asset, p.Network, p.Decimals, now)
		p.AccountID = acct.ID
		p.ScopeID = acct.ScopeID
		resolved[i] = p
	}

	// Apply balance deltas with checked arithmetic.
	for _, p := range resolved {
		acct := s.LedgerAccounts[p.AccountID]
		var next money.Amount
		var err error
		if isDebitNormal(acct.Category) {
			if p.Direction == Debit {
				next, err = money.CheckedAdd(acct.Balance, p.Amount)
			} else {
				next, err = money.CheckedSub(acct.Balance, p.Amount)
			}
		} else {
			if p.Direction == Credit {
				next, err = money.CheckedAdd(acct.Balance, p.Amount)
			} else {
				next, err = money.CheckedSub(acct.Balance, p.Amount)
			}
		}
		if err != nil {
			return nil, err
		}
		acct.Balance = next
	}

	s.Sequence++
	entry := &JournalEntry{
		ID:             fmt.Sprintf("je_%06d", s.Sequence),
		TenantID:       s.TenantID,
		IdempotencyKey: idempotencyKey,
		EntryType:      entryType,
		ReferenceID:    refID,
		Description:    description,
		Postings:       resolved,
		CreatedAt:      now,
	}
	s.JournalEntries = append(s.JournalEntries, entry)
	s.JournalByIdempotency[idempotencyKey] = entry.ID
	return entry, nil
}

// AccountBalance returns the current normal balance for an account (0 if not created yet).
func AccountBalance(s *State, code, scopeID, asset, network string) money.Amount {
	if scopeID == "" {
		scopeID = "global"
	}
	acct, ok := s.LedgerAccounts[accountKey(code, scopeID, asset, network)]
	if !ok {
		return 0
	}
	return acct.Balance
}

// SumAccountsByCode sums balances across all scopes for a given account code and asset/network.
func SumAccountsByCode(s *State, code, asset, network string) money.Amount {
	var total money.Amount
	for _, acct := range s.LedgerAccounts {
		if acct.Code == code && acct.Asset == asset && acct.Network == network {
			total += acct.Balance
		}
	}
	return total
}

type AssetTrialBalance struct {
	Asset        string       `json:"asset"`
	Network      string       `json:"network"`
	Decimals     int          `json:"decimals"`
	TotalDebits  money.Amount `json:"total_debits"`
	TotalCredits money.Amount `json:"total_credits"`
	Balanced     bool         `json:"balanced"`
}

type LedgerSummary struct {
	Balanced                 bool                `json:"balanced"`
	JournalCount             int                 `json:"journal_count"`
	TrialBalance             []AssetTrialBalance `json:"trial_balance"`
	ProviderCustodyUSD       money.Amount        `json:"provider_custody_usd"`
	CampaignAvailableUSD     money.Amount        `json:"campaign_available_usd"`
	CampaignReservedUSD      money.Amount        `json:"campaign_reserved_usd"`
	RewardPendingUSD         money.Amount        `json:"reward_pending_usd"`
	RewardPayableUSD         money.Amount        `json:"reward_payable_usd"`
	PayoutClearingUSD        money.Amount        `json:"payout_clearing_usd"`
	LossReserveUSD           money.Amount        `json:"loss_reserve_usd"`
	PlatformRevenueUSD       money.Amount        `json:"platform_revenue_usd"`
	TotalObligationsUSD      money.Amount        `json:"total_obligations_usd"`
	FundingCoversObligations bool                `json:"funding_covers_obligations"`
	Accounts                 []*LedgerAccount    `json:"accounts"`
}

// BuildLedgerSummary verifies every journal balances by asset and checks that
// allocated provider custody funding covers all campaign budgets, pending obligations,
// payable rewards, in-flight payout clearing, and loss reserves.
func BuildLedgerSummary(s *State) LedgerSummary {
	type key struct {
		asset    string
		network  string
		decimals int
	}
	debits := map[key]money.Amount{}
	credits := map[key]money.Amount{}
	allBalanced := true

	for _, je := range s.JournalEntries {
		if err := ValidateJournalBalanced(je.Postings); err != nil {
			allBalanced = false
		}
		for _, p := range je.Postings {
			k := key{asset: p.Asset, network: p.Network, decimals: p.Decimals}
			if p.Direction == Debit {
				debits[k] += p.Amount
			} else {
				credits[k] += p.Amount
			}
		}
	}

	keys := make([]key, 0, len(debits))
	seen := map[key]bool{}
	for k := range debits {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range credits {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].asset != keys[j].asset {
			return keys[i].asset < keys[j].asset
		}
		return keys[i].network < keys[j].network
	})

	tb := make([]AssetTrialBalance, 0, len(keys))
	for _, k := range keys {
		bal := debits[k] == credits[k]
		if !bal {
			allBalanced = false
		}
		tb = append(tb, AssetTrialBalance{
			Asset:        k.asset,
			Network:      k.network,
			Decimals:     k.decimals,
			TotalDebits:  debits[k],
			TotalCredits: credits[k],
			Balanced:     bal,
		})
	}

	custody := SumAccountsByCode(s, AcctProviderCustody, money.AssetUSD, money.NetworkFiat)
	avail := SumAccountsByCode(s, AcctCampaignAvailable, money.AssetUSD, money.NetworkFiat)
	reserved := SumAccountsByCode(s, AcctCampaignReserved, money.AssetUSD, money.NetworkFiat)
	pending := SumAccountsByCode(s, AcctRewardPending, money.AssetUSD, money.NetworkFiat)
	payable := SumAccountsByCode(s, AcctRewardPayable, money.AssetUSD, money.NetworkFiat)
	clearing := SumAccountsByCode(s, AcctPayoutClearing, money.AssetUSD, money.NetworkFiat)
	lossReserve := SumAccountsByCode(s, AcctLossReserve, money.AssetUSD, money.NetworkFiat)
	revenue := SumAccountsByCode(s, AcctPlatformRevenue, money.AssetUSD, money.NetworkFiat)

	obligations := pending + payable + clearing
	totalLiabilities := avail + reserved + obligations + lossReserve

	accounts := make([]*LedgerAccount, 0, len(s.LedgerAccounts))
	for _, a := range s.LedgerAccounts {
		cp := *a
		accounts = append(accounts, &cp)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Code != accounts[j].Code {
			return accounts[i].Code < accounts[j].Code
		}
		return accounts[i].ScopeID < accounts[j].ScopeID
	})

	return LedgerSummary{
		Balanced:                 allBalanced,
		JournalCount:             len(s.JournalEntries),
		TrialBalance:             tb,
		ProviderCustodyUSD:       custody,
		CampaignAvailableUSD:     avail,
		CampaignReservedUSD:      reserved,
		RewardPendingUSD:         pending,
		RewardPayableUSD:         payable,
		PayoutClearingUSD:        clearing,
		LossReserveUSD:           lossReserve,
		PlatformRevenueUSD:       revenue,
		TotalObligationsUSD:      obligations,
		FundingCoversObligations: custody >= totalLiabilities && custody >= obligations,
		Accounts:                 accounts,
	}
}
