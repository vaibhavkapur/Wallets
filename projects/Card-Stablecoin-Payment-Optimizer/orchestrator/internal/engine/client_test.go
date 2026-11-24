package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
)

func request() *contract.Request {
	return &contract.Request{
		ContractVersion: "1",
		EvaluatedAt:     time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Preferences: contract.Preferences{MinSavingsThreshold: contract.Amount{
			Asset: contract.AssetRef{Code: "USD", Decimals: 2}, BaseUnits: "0"}},
	}
}

func serve(t *testing.T, status int, body func(received []byte) any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer token")
		}
		var received json.RawMessage
		json.NewDecoder(r.Body).Decode(&received) //nolint:errcheck
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body(received)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "tok")
}

func TestRejectsResultNotBoundToRequest(t *testing.T) {
	c := serve(t, 200, func([]byte) any {
		return map[string]any{"input_sha256": "0000", "result": map[string]any{"contract_version": "1"}}
	})
	_, err := c.Evaluate(context.Background(), request())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

func TestSurfacesEngineRejections(t *testing.T) {
	c := serve(t, 422, func([]byte) any {
		return map[string]any{"error": map[string]any{"code": "invalid_amount", "message": "zero", "field": "/options/0"}}
	})
	_, err := c.Evaluate(context.Background(), request())
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Code != "invalid_amount" || rejected.Field != "/options/0" {
		t.Fatalf("got %v", err)
	}
}

func TestServerErrorsAreUnavailable(t *testing.T) {
	c := serve(t, 500, func([]byte) any { return map[string]any{} })
	if _, err := c.Evaluate(context.Background(), request()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
