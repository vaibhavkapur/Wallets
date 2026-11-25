package recurring

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides durable, tenant-isolated transactional persistence.
type Store interface {
	Update(ctx context.Context, tenantID string, fn func(*TenantState) error) error
	View(ctx context.Context, tenantID string) (*TenantState, error)
	Tenants(ctx context.Context) ([]string, error)
	GetGlobalPause(ctx context.Context) (GlobalPauseState, error)
	SetGlobalPause(ctx context.Context, pause GlobalPauseState) error
	Close()
}

type fileEnvelope struct {
	GlobalPause GlobalPauseState        `json:"global_pause"`
	Tenants     map[string]*TenantState `json:"tenants"`
}

// FileStore is a durable, single-process local fallback with flock and atomic rename.
// Set DATABASE_URL to use PostgreSQL in multi-process or production-like environments.
type FileStore struct {
	mu   sync.Mutex
	path string
	env  fileEnvelope
	lock *os.File
}

func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{
		path: path,
		env: fileEnvelope{
			Tenants: map[string]*TenantState{},
		},
	}
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
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("local data file is already locked by another process; use PostgreSQL for multiple processes")
	}
	s.lock = lock

	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		s.Close()
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.env); err != nil {
			s.Close()
			return nil, err
		}
		if s.env.Tenants == nil {
			s.env.Tenants = map[string]*TenantState{}
		}
	}
	return s, nil
}

func cloneTenantState(st *TenantState) *TenantState {
	b, _ := json.Marshal(st)
	var out TenantState
	_ = json.Unmarshal(b, &out)
	ensureTenantMaps(&out, st.TenantID)
	return &out
}

func ensureTenantMaps(st *TenantState, tenantID string) {
	if st.TenantID == "" {
		st.TenantID = tenantID
	}
	if st.Rules == nil {
		st.Rules = map[string]*RecurringRule{}
	}
	if st.Occurrences == nil {
		st.Occurrences = map[string]*ScheduleOccurrence{}
	}
	if st.SlotIndex == nil {
		st.SlotIndex = map[string]string{}
	}
	if st.Quotes == nil {
		st.Quotes = map[string]*MerchantQuote{}
	}
	if st.Proposals == nil {
		st.Proposals = map[string]*PurchaseProposal{}
	}
	if st.Consents == nil {
		st.Consents = map[string]*ConsentRecord{}
	}
	if st.Reservations == nil {
		st.Reservations = map[string]*BudgetReservation{}
	}
	if st.Checkouts == nil {
		st.Checkouts = map[string]*CheckoutReference{}
	}
	if st.Jobs == nil {
		st.Jobs = map[string]*WorkerJob{}
	}
	if st.JobDedupe == nil {
		st.JobDedupe = map[string]string{}
	}
	if st.Faults.PriceMultiplierBps == 0 {
		st.Faults.PriceMultiplierBps = 10000
	}
}

func (s *FileStore) persistLocked(nextEnv fileEnvelope) error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(nextEnv)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".recurring-state-*")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	dir.Close()
	return err
}

func (s *FileStore) Update(ctx context.Context, tenantID string, fn func(*TenantState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base := s.env.Tenants[tenantID]
	if base == nil {
		base = newTenantState(tenantID)
	}
	next := cloneTenantState(base)
	if err := fn(next); err != nil {
		return err
	}
	next.Revision++

	nextTenants := make(map[string]*TenantState, len(s.env.Tenants)+1)
	for k, v := range s.env.Tenants {
		nextTenants[k] = v
	}
	nextTenants[tenantID] = next
	nextEnv := fileEnvelope{
		GlobalPause: s.env.GlobalPause,
		Tenants:     nextTenants,
	}
	if err := s.persistLocked(nextEnv); err != nil {
		return err
	}
	s.env = nextEnv
	return nil
}

func (s *FileStore) View(ctx context.Context, tenantID string) (*TenantState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base := s.env.Tenants[tenantID]
	if base == nil {
		return newTenantState(tenantID), nil
	}
	return cloneTenantState(base), nil
}

func (s *FileStore) Tenants(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.env.Tenants))
	for id := range s.env.Tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *FileStore) GetGlobalPause(ctx context.Context) (GlobalPauseState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return GlobalPauseState{}, err
	}
	return s.env.GlobalPause, nil
}

func (s *FileStore) SetGlobalPause(ctx context.Context, pause GlobalPauseState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	nextEnv := fileEnvelope{
		GlobalPause: pause,
		Tenants:     s.env.Tenants,
	}
	if err := s.persistLocked(nextEnv); err != nil {
		return err
	}
	s.env = nextEnv
	return nil
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

// PgStore persists tenant state and global controls in PostgreSQL with row-level locks.
type PgStore struct {
	pool *pgxpool.Pool
}

func NewPgStore(ctx context.Context, databaseURL string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	ddl := `
CREATE TABLE IF NOT EXISTS recurring_tenants (
  tenant_id TEXT PRIMARY KEY,
  state JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
  revision BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS recurring_global_controls (
  control_key TEXT PRIMARY KEY,
  paused BOOLEAN NOT NULL DEFAULT FALSE,
  reason TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		return nil, err
	}
	return &PgStore{pool: pool}, nil
}

func (p *PgStore) Update(ctx context.Context, tenantID string, fn func(*TenantState) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	initJSON, _ := json.Marshal(newTenantState(tenantID))
	_, err = tx.Exec(ctx,
		`INSERT INTO recurring_tenants (tenant_id, state, revision) VALUES ($1, $2::jsonb, 0) ON CONFLICT (tenant_id) DO NOTHING`,
		tenantID, string(initJSON))
	if err != nil {
		return err
	}

	var raw []byte
	var rev int64
	if err := tx.QueryRow(ctx,
		`SELECT state, revision FROM recurring_tenants WHERE tenant_id = $1 FOR UPDATE`,
		tenantID).Scan(&raw, &rev); err != nil {
		return err
	}

	var st TenantState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	ensureTenantMaps(&st, tenantID)
	st.Revision = rev

	if err := fn(&st); err != nil {
		return err
	}
	st.Revision = rev + 1
	encoded, err := json.Marshal(&st)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE recurring_tenants SET state = $2::jsonb, revision = $3, updated_at = now() WHERE tenant_id = $1`,
		tenantID, string(encoded), st.Revision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PgStore) View(ctx context.Context, tenantID string) (*TenantState, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT state FROM recurring_tenants WHERE tenant_id = $1`, tenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return newTenantState(tenantID), nil
	}
	if err != nil {
		return nil, err
	}
	var st TenantState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	ensureTenantMaps(&st, tenantID)
	return &st, nil
}

func (p *PgStore) Tenants(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT tenant_id FROM recurring_tenants ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (p *PgStore) GetGlobalPause(ctx context.Context) (GlobalPauseState, error) {
	var out GlobalPauseState
	err := p.pool.QueryRow(ctx,
		`SELECT paused, reason, updated_by, updated_at FROM recurring_global_controls WHERE control_key = 'product_pause'`,
	).Scan(&out.Paused, &out.Reason, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GlobalPauseState{}, nil
	}
	return out, err
}

func (p *PgStore) SetGlobalPause(ctx context.Context, pause GlobalPauseState) error {
	if pause.UpdatedAt.IsZero() {
		pause.UpdatedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `
INSERT INTO recurring_global_controls (control_key, paused, reason, updated_by, updated_at)
VALUES ('product_pause', $1, $2, $3, $4)
ON CONFLICT (control_key) DO UPDATE SET
  paused = EXCLUDED.paused,
  reason = EXCLUDED.reason,
  updated_by = EXCLUDED.updated_by,
  updated_at = EXCLUDED.updated_at`,
		pause.Paused, pause.Reason, pause.UpdatedBy, pause.UpdatedAt)
	return err
}

func (p *PgStore) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}
