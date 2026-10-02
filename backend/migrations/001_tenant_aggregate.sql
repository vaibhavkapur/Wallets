-- Tenant-scoped aggregate persistence for the sandbox MVP. Every spending mutation
-- locks the tenant row, writes immutable approvals, ledger and outbox together.
-- Provider calls occur after commit. Extract normalized tables when pilot scale requires it.
CREATE TABLE IF NOT EXISTS checkout_tenants (
  tenant_id TEXT PRIMARY KEY,
  state JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
  revision BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
