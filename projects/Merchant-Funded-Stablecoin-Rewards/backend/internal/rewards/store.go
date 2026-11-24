package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"unowallet/rewards/migrations"
)

// Store provides atomic, tenant-isolated transactional persistence.
// Update executes fn under a durable exclusive lock/transaction; provider network calls
// must never be executed inside Update.
type Store interface {
	Update(ctx context.Context, tenantID string, fn func(*State) error) error
	View(ctx context.Context, tenantID string) (*State, error)
	Tenants(ctx context.Context) ([]string, error)
	Close()
}

// FileStore is a durable, single-process local fallback. PostgreSQL is used when DATABASE_URL is set.
type FileStore struct {
	mu   sync.Mutex
	path string
	data map[string]*State
	lock *os.File
}

func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: map[string]*State{}}
	if path == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("sandbox state file is already locked by another process; use PostgreSQL for multi-process deployment")
	}
	s.lock = lock
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		s.Close()
		return nil, err
	}
	if len(b) > 0 {
		if err = json.Unmarshal(b, &s.data); err != nil {
			s.Close()
			return nil, fmt.Errorf("failed to parse state file: %w", err)
		}
	}
	return s, nil
}

func cloneState(s *State) *State {
	b, _ := json.Marshal(s)
	var c State
	_ = json.Unmarshal(b, &c)
	ensureStateMaps(&c)
	return &c
}

func ensureStateMaps(s *State) {
	if s.Merchants == nil {
		s.Merchants = map[string]*Merchant{}
	}
	if s.MerchantSecrets == nil {
		s.MerchantSecrets = map[string]string{}
	}
	if s.CampaignVersions == nil {
		s.CampaignVersions = map[string]*CampaignVersion{}
	}
	if s.ActiveCampaignVer == nil {
		s.ActiveCampaignVer = map[string]string{}
	}
	if s.FundingEvents == nil {
		s.FundingEvents = map[string]*FundingEvent{}
	}
	if s.FundingByProviderRef == nil {
		s.FundingByProviderRef = map[string]string{}
	}
	if s.Attributions == nil {
		s.Attributions = map[string]*PurchaseAttribution{}
	}
	if s.AttributionByEvent == nil {
		s.AttributionByEvent = map[string]string{}
	}
	if s.AttributionByOrder == nil {
		s.AttributionByOrder = map[string]string{}
	}
	if s.ProcessedEventIDs == nil {
		s.ProcessedEventIDs = map[string]bool{}
	}
	if s.Reservations == nil {
		s.Reservations = map[string]*CampaignReservation{}
	}
	if s.Rewards == nil {
		s.Rewards = map[string]*RewardAccrual{}
	}
	if s.Beneficiaries == nil {
		s.Beneficiaries = map[string]*BeneficiaryVersion{}
	}
	if s.ActiveBeneficiary == nil {
		s.ActiveBeneficiary = map[string]string{}
	}
	if s.Quotes == nil {
		s.Quotes = map[string]*PayoutQuote{}
	}
	if s.PayoutAttempts == nil {
		s.PayoutAttempts = map[string]*PayoutAttempt{}
	}
	if s.PayoutByIdempotency == nil {
		s.PayoutByIdempotency = map[string]string{}
	}
	if s.PayoutByLogicalID == nil {
		s.PayoutByLogicalID = map[string][]string{}
	}
	if s.ProcessedCallbacks == nil {
		s.ProcessedCallbacks = map[string]string{}
	}
	if s.LedgerAccounts == nil {
		s.LedgerAccounts = map[string]*LedgerAccount{}
	}
	if s.JournalByIdempotency == nil {
		s.JournalByIdempotency = map[string]string{}
	}
	if s.Exceptions == nil {
		s.Exceptions = map[string]*ReconciliationException{}
	}
}

func (s *FileStore) Update(ctx context.Context, tenantID string, fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base := s.data[tenantID]
	if base == nil {
		base = NewState(tenantID)
	}
	next := cloneState(base)
	if err := fn(next); err != nil {
		return err
	}
	all := make(map[string]*State, len(s.data)+1)
	for k, v := range s.data {
		all[k] = v
	}
	all[tenantID] = next
	if s.path != "" {
		b, err := json.Marshal(all)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(s.path), ".rewards-state-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if _, err = tmp.Write(b); err != nil {
			tmp.Close()
			return err
		}
		if err = tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		if err = os.Rename(tmpName, s.path); err != nil {
			return err
		}
	}
	s.data = all
	return nil
}

func (s *FileStore) View(ctx context.Context, tenantID string) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base := s.data[tenantID]
	if base == nil {
		return NewState(tenantID), nil
	}
	return cloneState(base), nil
}

func (s *FileStore) Tenants(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		out = append(out, k)
	}
	return out, nil
}

func (s *FileStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil {
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}
}

// PostgresStore persists tenant-isolated transactional state in PostgreSQL using pgx/v5.
// Every Update locks the tenant row with SELECT ... FOR UPDATE, ensuring strict serialization
// of concurrent campaign reservations, double-entry ledger postings, and outbox writes.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, migrations.InitSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (p *PostgresStore) Update(ctx context.Context, tenantID string, fn func(*State) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Ensure row exists, then acquire row-level exclusive lock for this tenant.
	emptyJSON, err := json.Marshal(NewState(tenantID))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO rewards_tenant_state (tenant_id, state, revision, updated_at)
		VALUES ($1, $2::jsonb, 0, now())
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID, emptyJSON); err != nil {
		return err
	}

	var raw []byte
	if err := tx.QueryRow(ctx, `
		SELECT state FROM rewards_tenant_state WHERE tenant_id = $1 FOR UPDATE
	`, tenantID).Scan(&raw); err != nil {
		return err
	}

	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.TenantID == "" {
		st.TenantID = tenantID
	}
	ensureStateMaps(&st)

	if err := fn(&st); err != nil {
		return err
	}

	updated, err := json.Marshal(&st)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE rewards_tenant_state
		SET state = $2::jsonb, revision = revision + 1, updated_at = now()
		WHERE tenant_id = $1
	`, tenantID, updated); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) View(ctx context.Context, tenantID string) (*State, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT state FROM rewards_tenant_state WHERE tenant_id = $1`, tenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return NewState(tenantID), nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if st.TenantID == "" {
		st.TenantID = tenantID
	}
	ensureStateMaps(&st)
	return &st, nil
}

func (p *PostgresStore) Tenants(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT tenant_id FROM rewards_tenant_state ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (p *PostgresStore) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}
