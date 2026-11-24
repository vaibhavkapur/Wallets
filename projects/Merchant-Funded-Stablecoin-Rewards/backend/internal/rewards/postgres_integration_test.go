package rewards

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestPostgresStoreIntegration runs against a live PostgreSQL instance when TEST_DATABASE_URL or DATABASE_URL is set.
func TestPostgresStoreIntegration(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := NewPostgresStore(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer store.Close()

	svc := NewService(store, NewSandboxProvider(), "pg-test-secret")
	tenant := fmt.Sprintf("pg_int_%d", time.Now().UnixNano())
	if err := svc.SeedPilotData(ctx, tenant); err != nil {
		t.Fatalf("seed pilot data in postgres: %v", err)
	}
	assertLedgerHealthy(t, svc, tenant)
}
