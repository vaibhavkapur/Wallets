package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store interface {
	Update(context.Context, string, func(*State) error) error
	View(context.Context, string) (*State, error)
	Tenants(context.Context) ([]string, error)
	Close()
}

// FileStore is a durable, single-process sandbox fallback. PostgreSQL is used when DATABASE_URL is set.
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
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("sandbox data is already open; use PostgreSQL for multiple processes")
	}
	s.lock = lock
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		s.Close()
		return nil, e
	}
	if len(b) > 0 {
		if e = json.Unmarshal(b, &s.data); e != nil {
			s.Close()
			return nil, e
		}
	}
	return s, nil
}
func clone(s *State) *State {
	b, _ := json.Marshal(s)
	var c State
	_ = json.Unmarshal(b, &c)
	return &c
}
func (s *FileStore) Update(ctx context.Context, t string, fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base := s.data[t]
	if base == nil {
		base = newState()
	}
	next := clone(base)
	if e := fn(next); e != nil {
		return e
	}
	all := make(map[string]*State, len(s.data)+1)
	for k, v := range s.data {
		all[k] = v
	}
	all[t] = next
	if s.path != "" {
		b, e := json.Marshal(all)
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(filepath.Dir(s.path), ".state-*")
		if e != nil {
			return e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(b); e != nil {
			f.Close()
			return e
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if e = os.Rename(f.Name(), s.path); e != nil {
			return e
		}
		d, e := os.Open(filepath.Dir(s.path))
		if e != nil {
			return e
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			return e
		}
	}
	s.data = all
	return nil
}
func (s *FileStore) View(ctx context.Context, t string) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.data[t] == nil {
		return newState(), nil
	}
	return clone(s.data[t]), nil
}
func (s *FileStore) Tenants(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for t := range s.data {
		out = append(out, t)
	}
	return out, ctx.Err()
}
func (s *FileStore) Close() {
	if s.lock != nil {
		s.lock.Close()
	}
}

type PostgresStore struct{ pool *pgxpool.Pool }

const migration = `CREATE TABLE IF NOT EXISTS checkout_tenants (
 tenant_id TEXT PRIMARY KEY,
 state JSONB NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (jsonb_typeof(state) = 'object')
);`

func NewPostgresStore(ctx context.Context, url string) (*PostgresStore, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if _, e = p.Exec(ctx, migration); e != nil {
		p.Close()
		return nil, e
	}
	return &PostgresStore{p}, nil
}

// The tenant row lock atomically protects approvals, reservations, idempotency, ledger and outbox.
// The callback only mutates local state: it MUST NOT make provider/network calls.
func (s *PostgresStore) Update(ctx context.Context, t string, fn func(*State) error) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	initial, _ := json.Marshal(newState())
	if _, e = tx.Exec(ctx, "INSERT INTO checkout_tenants(tenant_id,state) VALUES($1,$2) ON CONFLICT DO NOTHING", t, initial); e != nil {
		return e
	}
	var raw []byte
	if e = tx.QueryRow(ctx, "SELECT state FROM checkout_tenants WHERE tenant_id=$1 FOR UPDATE", t).Scan(&raw); e != nil {
		return e
	}
	var state State
	if e = json.Unmarshal(raw, &state); e != nil {
		return e
	}
	if e = fn(&state); e != nil {
		return e
	}
	raw, e = json.Marshal(&state)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE checkout_tenants SET state=$2,revision=revision+1,updated_at=now() WHERE tenant_id=$1", t, raw); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) View(ctx context.Context, t string) (*State, error) {
	var raw []byte
	e := s.pool.QueryRow(ctx, "SELECT state FROM checkout_tenants WHERE tenant_id=$1", t).Scan(&raw)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return newState(), nil
		}
		return nil, e
	}
	var state State
	e = json.Unmarshal(raw, &state)
	return &state, e
}
func (s *PostgresStore) Tenants(ctx context.Context) ([]string, error) {
	rows, e := s.pool.Query(ctx, "SELECT tenant_id FROM checkout_tenants")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if e = rows.Scan(&t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *PostgresStore) Close() { s.pool.Close() }
