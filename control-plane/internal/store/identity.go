package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
)

// --- Organizations ----------------------------------------------------------

// CreateOrganization inserts an organization and its first owner key.
func (s *Store) CreateOrganization(ctx context.Context, org domain.Organization, key domain.OwnerKey, actor string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO organizations(id,name,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5)`,
			org.ID, org.Name, org.Status, org.CreatedAt, org.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO owner_keys(id,org_id,key_hash,label,created_at) VALUES ($1,$2,$3,$4,$5)`,
			key.ID, key.OrgID, key.KeyHash, key.Label, key.CreatedAt); err != nil {
			return err
		}
		return appendAudit(ctx, tx, org.CreatedAt, org.ID, actor, "organization.created", "organization", org.ID, map[string]string{"name": org.Name})
	})
}

func scanOrg(row pgx.Row) (domain.Organization, error) {
	var o domain.Organization
	err := row.Scan(&o.ID, &o.Name, &o.Status, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

const orgCols = `id,name,status,created_at,updated_at`

// GetOrganization loads an organization.
func (s *Store) GetOrganization(ctx context.Context, id string) (domain.Organization, error) {
	return scanOrg(s.pool.QueryRow(ctx, `SELECT `+orgCols+` FROM organizations WHERE id=$1`, id))
}

// SetOrganizationStatus pauses or resumes spending for an organization.
func (s *Store) SetOrganizationStatus(ctx context.Context, id string, status domain.OrgStatus, actor string) error {
	now := s.Now()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE organizations SET status=$2, updated_at=$3 WHERE id=$1`, id, status, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return appendAudit(ctx, tx, now, id, actor, "organization.status", "organization", id, map[string]string{"status": string(status)})
	})
}

// GetOwnerKeyByHash resolves an owner credential.
func (s *Store) GetOwnerKeyByHash(ctx context.Context, hash string) (domain.OwnerKey, error) {
	var k domain.OwnerKey
	err := s.pool.QueryRow(ctx, `SELECT id,org_id,key_hash,label,created_at,revoked_at FROM owner_keys WHERE key_hash=$1`, hash).
		Scan(&k.ID, &k.OrgID, &k.KeyHash, &k.Label, &k.CreatedAt, &k.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	return k, err
}

// --- Agents -----------------------------------------------------------------

const agentCols = `id,org_id,name,status,key_hash,COALESCE(policy_version_id,''),created_at,revoked_at`

func scanAgent(row pgx.Row) (domain.AgentPrincipal, error) {
	var a domain.AgentPrincipal
	err := row.Scan(&a.ID, &a.OrgID, &a.Name, &a.Status, &a.KeyHash, &a.PolicyVersionID, &a.CreatedAt, &a.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// CreateAgent inserts an agent principal without a policy; spending requires
// a policy version to be set by the owner.
func (s *Store) CreateAgent(ctx context.Context, a domain.AgentPrincipal, actor string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO agents(id,org_id,name,status,key_hash,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			a.ID, a.OrgID, a.Name, a.Status, a.KeyHash, a.CreatedAt); err != nil {
			return err
		}
		return appendAudit(ctx, tx, a.CreatedAt, a.OrgID, actor, "agent.created", "agent", a.ID, map[string]string{"name": a.Name})
	})
}

// GetAgent loads an agent scoped to an organization (tenant isolation).
func (s *Store) GetAgent(ctx context.Context, orgID, id string) (domain.AgentPrincipal, error) {
	return scanAgent(s.pool.QueryRow(ctx, `SELECT `+agentCols+` FROM agents WHERE id=$1 AND org_id=$2`, id, orgID))
}

// GetAgentByKeyHash resolves an agent credential.
func (s *Store) GetAgentByKeyHash(ctx context.Context, hash string) (domain.AgentPrincipal, error) {
	return scanAgent(s.pool.QueryRow(ctx, `SELECT `+agentCols+` FROM agents WHERE key_hash=$1`, hash))
}

// ListAgents lists agents for an organization.
func (s *Store) ListAgents(ctx context.Context, orgID string) ([]domain.AgentPrincipal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+agentCols+` FROM agents WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AgentPrincipal
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevokeAgent prevents any new spending by the agent. Open reservations stay
// tracked until reconciliation resolves them.
func (s *Store) RevokeAgent(ctx context.Context, orgID, id, actor string) error {
	now := s.Now()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agents SET status='revoked', revoked_at=$3 WHERE id=$1 AND org_id=$2 AND status='active'`, id, orgID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return appendAudit(ctx, tx, now, orgID, actor, "agent.revoked", "agent", id, nil)
	})
}

// --- Policies ---------------------------------------------------------------

const policyCols = `id,org_id,agent_id,version,asset,network,decimals,scheme,
	per_request_max::text,per_task_max::text,agent_daily_max::text,org_daily_max::text,fee_allowance::text,
	endpoints,max_task_seconds,grant_ttl_seconds,created_at`

func scanPolicy(row pgx.Row) (domain.BudgetPolicyVersion, error) {
	var p domain.BudgetPolicyVersion
	var endpoints []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.AgentID, &p.Version, &p.Asset.Asset, &p.Asset.Network, &p.Asset.Decimals, &p.Asset.Scheme,
		&p.PerRequestMax, &p.PerTaskMax, &p.AgentDailyMax, &p.OrgDailyMax, &p.FeeAllowance,
		&endpoints, &p.MaxTaskSeconds, &p.GrantTTLSeconds, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(endpoints, &p.Endpoints); err != nil {
		return p, fmt.Errorf("decode endpoints: %w", err)
	}
	p.MaxTaskDuration = time.Duration(p.MaxTaskSeconds) * time.Second
	return p, nil
}

// CreatePolicyVersion appends an immutable policy version for an agent and
// makes it the agent's active policy.
func (s *Store) CreatePolicyVersion(ctx context.Context, p domain.BudgetPolicyVersion, actor string) (domain.BudgetPolicyVersion, error) {
	endpoints, err := json.Marshal(p.Endpoints)
	if err != nil {
		return p, err
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		// Lock the agent row so concurrent policy updates serialize and
		// version numbers stay dense.
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM agents WHERE id=$1 AND org_id=$2 FOR UPDATE`, p.AgentID, p.OrgID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM policy_versions WHERE agent_id=$1`, p.AgentID).Scan(&p.Version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO policy_versions(id,org_id,agent_id,version,asset,network,decimals,scheme,
			per_request_max,per_task_max,agent_daily_max,org_daily_max,fee_allowance,endpoints,max_task_seconds,grant_ttl_seconds,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			p.ID, p.OrgID, p.AgentID, p.Version, p.Asset.Asset, p.Asset.Network, p.Asset.Decimals, p.Asset.Scheme,
			p.PerRequestMax.String(), p.PerTaskMax.String(), p.AgentDailyMax.String(), p.OrgDailyMax.String(), p.FeeAllowance.String(),
			endpoints, p.MaxTaskSeconds, p.GrantTTLSeconds, p.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agents SET policy_version_id=$2 WHERE id=$1`, p.AgentID, p.ID); err != nil {
			return err
		}
		return appendAudit(ctx, tx, p.CreatedAt, p.OrgID, actor, "policy.created", "policy_version", p.ID, map[string]any{
			"agent_id": p.AgentID, "version": p.Version, "per_request_max": p.PerRequestMax, "per_task_max": p.PerTaskMax,
			"agent_daily_max": p.AgentDailyMax, "org_daily_max": p.OrgDailyMax, "endpoints": p.Endpoints,
		})
	})
	return p, err
}

// GetPolicyVersion loads an immutable policy version scoped to an org.
func (s *Store) GetPolicyVersion(ctx context.Context, orgID, id string) (domain.BudgetPolicyVersion, error) {
	return scanPolicy(s.pool.QueryRow(ctx, `SELECT `+policyCols+` FROM policy_versions WHERE id=$1 AND org_id=$2`, id, orgID))
}

// ListPolicyVersions returns an agent's policy history, newest first.
func (s *Store) ListPolicyVersions(ctx context.Context, orgID, agentID string) ([]domain.BudgetPolicyVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+policyCols+` FROM policy_versions WHERE org_id=$1 AND agent_id=$2 ORDER BY version DESC`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BudgetPolicyVersion
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Tasks ------------------------------------------------------------------

const taskCols = `id,org_id,agent_id,policy_version_id,description,status,budget::text,deadline,created_at,closed_at`

func scanTask(row pgx.Row) (domain.Task, error) {
	var t domain.Task
	err := row.Scan(&t.ID, &t.OrgID, &t.AgentID, &t.PolicyVersionID, &t.Description, &t.Status, &t.Budget, &t.Deadline, &t.CreatedAt, &t.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// CreateTask inserts a task.
func (s *Store) CreateTask(ctx context.Context, t domain.Task, actor string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tasks(id,org_id,agent_id,policy_version_id,description,status,budget,deadline,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			t.ID, t.OrgID, t.AgentID, t.PolicyVersionID, t.Description, t.Status, t.Budget.String(), t.Deadline, t.CreatedAt); err != nil {
			return err
		}
		return appendAudit(ctx, tx, t.CreatedAt, t.OrgID, actor, "task.created", "task", t.ID, map[string]any{"budget": t.Budget, "deadline": t.Deadline})
	})
}

// GetTask loads a task scoped to an organization.
func (s *Store) GetTask(ctx context.Context, orgID, id string) (domain.Task, error) {
	return scanTask(s.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1 AND org_id=$2`, id, orgID))
}

// ListTasks lists tasks for an org, optionally filtered by agent.
func (s *Store) ListTasks(ctx context.Context, orgID, agentID string, limit int) ([]domain.Task, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+taskCols+` FROM tasks WHERE org_id=$1 AND ($2='' OR agent_id=$2)
		ORDER BY created_at DESC LIMIT $3`, orgID, agentID, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CloseTask stops new spending on a task.
func (s *Store) CloseTask(ctx context.Context, orgID, id, actor string) error {
	now := s.Now()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET status='closed', closed_at=$3 WHERE id=$1 AND org_id=$2 AND status='active'`, id, orgID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return appendAudit(ctx, tx, now, orgID, actor, "task.closed", "task", id, nil)
	})
}

// TaskUsage is the spend summary returned to agents for a single task.
type TaskUsage struct {
	Held      money.Amount `json:"held"`
	Committed money.Amount `json:"committed"`
}

// GetTaskUsage sums open and committed reservations for one task.
func (s *Store) GetTaskUsage(ctx context.Context, orgID, taskID string) (TaskUsage, error) {
	var u TaskUsage
	err := s.pool.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status='held' THEN amount+fee_allowance ELSE 0 END),0)::text,
		COALESCE(SUM(CASE WHEN status='committed' THEN committed_amount+committed_fee ELSE 0 END),0)::text
		FROM reservations WHERE org_id=$1 AND task_id=$2`, orgID, taskID).Scan(&u.Held, &u.Committed)
	return u, err
}
