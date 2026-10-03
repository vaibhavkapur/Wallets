package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mywallet/backend/internal/appleauth"
	"mywallet/backend/internal/devpg"
	"mywallet/backend/internal/enrollment"
	"mywallet/backend/internal/ids"
	"mywallet/backend/internal/provider/fake"
	"mywallet/backend/internal/store"
	"mywallet/backend/internal/workers"
)

var testDSN string

func TestMain(m *testing.M) {
	code := func() int {
		if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
			testDSN = dsn
		} else {
			inst, err := devpg.Start()
			if err != nil {
				fmt.Fprintln(os.Stderr, "embedded postgres:", err)
				return 1
			}
			defer inst.Stop()
			testDSN = inst.DSN
		}
		return m.Run()
	}()
	os.Exit(code)
}

// env is a running API over a shared database with its own fake provider.
// Tests isolate by user (each signs in with a fresh subject).
type env struct {
	t      *testing.T
	st     *store.Store
	pv     *fake.Provider
	enroll *enrollment.Service
	worker *workers.Worker
	srv    *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pv := fake.New()
	enroll := enrollment.New(st, pv, log)
	api, err := New(st, enroll, appleauth.Dev{}, Config{SessionTTL: time.Hour, DevHooks: fake.Hooks{P: pv}, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	w := workers.New(st, enroll, workers.Config{Logger: log, MinBackoff: time.Millisecond, Lease: time.Millisecond})
	return &env{t: t, st: st, pv: pv, enroll: enroll, worker: w, srv: srv}
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (r resp) str(path string) string {
	v := r.get(path)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (r resp) get(path string) any {
	var cur any = r.Body
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func (e *env) do(method, path, token string, body any, headers map[string]string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

// rawPost sends a pre-encoded body (webhook payloads must be byte-exact).
func (e *env) rawPost(path string, payload []byte, headers map[string]string) resp {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(payload))
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func (e *env) signIn(subject string) (token string, userID string) {
	e.t.Helper()
	r := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "dev:" + subject + ":" + subject + "@example.test"}, nil)
	if r.Status != http.StatusCreated && r.Status != http.StatusOK {
		e.t.Fatalf("sign in: %d %s", r.Status, r.Raw)
	}
	return r.str("session_token"), r.str("user.id")
}

func (e *env) newSubject() string { return ids.New("subj") }

func (e *env) startSetup(token string, key string) resp {
	e.t.Helper()
	r := e.do(http.MethodPost, "/v1/card-setups", token, map[string]any{"consent": map[string]any{"accepted": true, "version": "2026-10"}},
		map[string]string{"Idempotency-Key": key})
	if r.Status != http.StatusCreated && r.Status != http.StatusOK {
		e.t.Fatalf("start setup: %d %s", r.Status, r.Raw)
	}
	return r
}

// setupRef resolves the provider reference for an attempt (test-only).
func (e *env) setupRef(attemptID string) string {
	e.t.Helper()
	a, err := e.st.GetSetupAttemptByID(context.Background(), attemptID)
	if err != nil || a.SetupRef == nil {
		e.t.Fatalf("attempt %s has no setup ref: %v", attemptID, err)
	}
	return *a.SetupRef
}

// addCard runs the whole happy path and returns the card ID.
func (e *env) addCard(token string, card fake.Card) string {
	e.t.Helper()
	r := e.startSetup(token, ids.New("key"))
	id := r.str("id")
	if _, err := e.pv.CompleteSetup(e.setupRef(id), card); err != nil {
		e.t.Fatal(err)
	}
	g := e.do(http.MethodGet, "/v1/card-setups/"+id, token, nil, nil)
	if g.str("status") != "saved" {
		e.t.Fatalf("expected saved, got %s", g.Raw)
	}
	return g.str("card.id")
}

// tickAfterBackoff waits past the worker's (1ms) minimum backoff so a retry
// scheduled by the previous tick is due, then runs one pass.
func (e *env) tickAfterBackoff() {
	time.Sleep(20 * time.Millisecond)
	e.worker.Tick(context.Background())
}

func expectStatus(t *testing.T, r resp, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d, want %d: %s", r.Status, want, r.Raw)
	}
}
