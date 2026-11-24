import { amount, type Amount } from "./amounts.js";
import { Http, type FetchLike } from "./http.js";
import type {
  AccountBalance,
  AgentPrincipal,
  AuditEvent,
  BudgetPolicyVersion,
  LedgerEntry,
  Organization,
  PaidRequestInput,
  PaidResponse,
  PaymentAttempt,
  PolicyInput,
  Receipt,
  Task,
  TaskBudget,
} from "./types.js";

export interface AgentClientOptions {
  /** Go control plane base URL, e.g. http://127.0.0.1:8080 */
  controlPlaneUrl: string;
  /** Rust gateway base URL, e.g. http://127.0.0.1:8402 */
  gatewayUrl: string;
  /** Agent API key (agent_...). Never an owner key. */
  apiKey: string;
  fetch?: FetchLike;
  timeoutMs?: number;
}

export interface CreateTaskInput {
  description?: string;
  /** Requested budget; capped by the policy's per-task maximum. */
  budget?: Amount;
  /** Requested lifetime; capped by the policy's maximum task duration. */
  durationSeconds?: number;
}

/**
 * Agent-facing client. An agent can create tasks within its owner's policy,
 * inspect a task's spendable capacity, execute paid requests through the
 * gateway, and read its own receipts. It cannot change budgets, approve
 * beneficiaries, or see other tasks' data.
 */
export class AgentClient {
  private readonly cp: Http;
  private readonly gw: Http;

  constructor(opts: AgentClientOptions) {
    if (!opts.apiKey.startsWith("agent_")) {
      throw new Error("AgentClient requires an agent API key (agent_...)");
    }
    const base = { apiKey: opts.apiKey, timeoutMs: opts.timeoutMs } as const;
    this.cp = new Http({ baseUrl: opts.controlPlaneUrl, ...base, ...(opts.fetch ? { fetch: opts.fetch } : {}) });
    this.gw = new Http({ baseUrl: opts.gatewayUrl, ...base, ...(opts.fetch ? { fetch: opts.fetch } : {}) });
  }

  createTask(input: CreateTaskInput = {}): Promise<Task> {
    const body: Record<string, unknown> = { description: input.description ?? "" };
    if (input.budget !== undefined) body.budget = amount(input.budget);
    if (input.durationSeconds !== undefined) body.duration_seconds = input.durationSeconds;
    return this.cp.request<Task>("POST", "/v1/tasks", body);
  }

  getTask(taskId: string): Promise<Task> {
    return this.cp.request<Task>("GET", `/v1/tasks/${encodeURIComponent(taskId)}`);
  }

  getTaskBudget(taskId: string): Promise<TaskBudget> {
    return this.cp.request<TaskBudget>("GET", `/v1/tasks/${encodeURIComponent(taskId)}/budget`);
  }

  async listTaskReceipts(taskId: string, limit?: number): Promise<Receipt[]> {
    const r = await this.cp.request<{ receipts: Receipt[] }>("GET", `/v1/tasks/${encodeURIComponent(taskId)}/receipts`, undefined, { limit });
    return r.receipts;
  }

  getPaymentAttempt(attemptId: string): Promise<PaymentAttempt> {
    return this.cp.request<PaymentAttempt>("GET", `/v1/payment-attempts/${encodeURIComponent(attemptId)}`);
  }

  /**
   * Execute a paid resource request through the gateway. The gateway probes
   * the resource, validates the x402 challenge against policy, obtains an
   * execution grant from the control plane, pays, and returns the response
   * together with the payment outcome and receipt.
   *
   * Throws AsaError on denials (403 with a stable `code`). A settled payment
   * with an unusable service result is NOT an error: inspect
   * `response.service.delivered`.
   */
  paidRequest(taskId: string, input: PaidRequestInput): Promise<PaidResponse> {
    if (!input.idempotencyKey) throw new Error("idempotencyKey is required");
    const body: Record<string, unknown> = {
      idempotency_key: input.idempotencyKey,
      method: input.method ?? "GET",
      url: input.url,
    };
    if (input.headers) body.headers = input.headers;
    if (input.body !== undefined) body.body = input.body;
    if (input.bodyBase64 !== undefined) body.body_base64 = input.bodyBase64;
    if (input.maxAmount !== undefined) body.max_amount = amount(input.maxAmount);
    return this.gw.request<PaidResponse>("POST", `/v1/tasks/${encodeURIComponent(taskId)}/paid-requests`, body);
  }

  /**
   * Convenience for JSON APIs: performs a paid GET/POST and parses the body as
   * JSON when the service delivered a 2xx. Returns the parsed body plus the
   * full paid response for receipts.
   */
  async paidJson<T = unknown>(taskId: string, input: PaidRequestInput): Promise<{ data: T | undefined; response: PaidResponse }> {
    const response = await this.paidRequest(taskId, input);
    const svc = response.service;
    let data: T | undefined;
    if (svc?.delivered && svc.body !== undefined && svc.body !== null) {
      try {
        data = JSON.parse(svc.body) as T;
      } catch {
        data = undefined;
      }
    }
    return { data, response };
  }
}

export interface OwnerClientOptions {
  controlPlaneUrl: string;
  /** Owner API key (owner_...). Keep it out of agent processes. */
  apiKey: string;
  fetch?: FetchLike;
  timeoutMs?: number;
}

/** Owner/operator client: funding, agents, policies, revocation, views. */
export class OwnerClient {
  private readonly cp: Http;

  constructor(opts: OwnerClientOptions) {
    if (!opts.apiKey.startsWith("owner_")) {
      throw new Error("OwnerClient requires an owner API key (owner_...)");
    }
    this.cp = new Http({ baseUrl: opts.controlPlaneUrl, apiKey: opts.apiKey, timeoutMs: opts.timeoutMs, ...(opts.fetch ? { fetch: opts.fetch } : {}) });
  }

  getOrganization(): Promise<Organization> {
    return this.cp.request<Organization>("GET", "/v1/organization");
  }
  pause(): Promise<Organization> {
    return this.cp.request<Organization>("POST", "/v1/organization/pause");
  }
  resume(): Promise<Organization> {
    return this.cp.request<Organization>("POST", "/v1/organization/resume");
  }

  createAgent(name: string): Promise<{ agent: AgentPrincipal; agent_api_key: string }> {
    return this.cp.request("POST", "/v1/agents", { name });
  }
  async listAgents(): Promise<AgentPrincipal[]> {
    return (await this.cp.request<{ agents: AgentPrincipal[] }>("GET", "/v1/agents")).agents;
  }
  getAgent(agentId: string): Promise<AgentPrincipal> {
    return this.cp.request("GET", `/v1/agents/${encodeURIComponent(agentId)}`);
  }
  revokeAgent(agentId: string): Promise<AgentPrincipal> {
    return this.cp.request("POST", `/v1/agents/${encodeURIComponent(agentId)}/revoke`);
  }

  setPolicy(agentId: string, policy: PolicyInput): Promise<BudgetPolicyVersion> {
    const p = {
      ...policy,
      per_request_max: amount(policy.per_request_max),
      per_task_max: amount(policy.per_task_max),
      agent_daily_max: amount(policy.agent_daily_max),
      org_daily_max: amount(policy.org_daily_max),
      fee_allowance: amount(policy.fee_allowance),
    };
    return this.cp.request("PUT", `/v1/agents/${encodeURIComponent(agentId)}/policy`, p);
  }
  getPolicy(agentId: string): Promise<BudgetPolicyVersion> {
    return this.cp.request("GET", `/v1/agents/${encodeURIComponent(agentId)}/policy`);
  }
  async listPolicyVersions(agentId: string): Promise<BudgetPolicyVersion[]> {
    return (await this.cp.request<{ versions: BudgetPolicyVersion[] }>("GET", `/v1/agents/${encodeURIComponent(agentId)}/policy/versions`)).versions;
  }

  deposit(input: { asset: string; network: string; amount: Amount; reference?: string }): Promise<AccountBalance> {
    return this.cp.request("POST", "/v1/funding/deposits", { ...input, amount: amount(input.amount), reference: input.reference ?? "" });
  }
  withdraw(input: { asset: string; network: string; amount: Amount; reference?: string }): Promise<AccountBalance> {
    return this.cp.request("POST", "/v1/funding/withdrawals", { ...input, amount: amount(input.amount), reference: input.reference ?? "" });
  }
  async listBalances(): Promise<AccountBalance[]> {
    return (await this.cp.request<{ balances: AccountBalance[] }>("GET", "/v1/balances")).balances;
  }
  async listLedger(limit?: number): Promise<LedgerEntry[]> {
    return (await this.cp.request<{ entries: LedgerEntry[] }>("GET", "/v1/ledger", undefined, { limit })).entries;
  }

  async listTasks(opts: { agentId?: string; limit?: number } = {}): Promise<Task[]> {
    return (await this.cp.request<{ tasks: Task[] }>("GET", "/v1/tasks", undefined, { agent_id: opts.agentId, limit: opts.limit })).tasks;
  }
  closeTask(taskId: string): Promise<Task> {
    return this.cp.request("POST", `/v1/tasks/${encodeURIComponent(taskId)}/close`);
  }
  async listPaymentAttempts(opts: { taskId?: string; limit?: number } = {}): Promise<PaymentAttempt[]> {
    return (await this.cp.request<{ payment_attempts: PaymentAttempt[] }>("GET", "/v1/payment-attempts", undefined, { task_id: opts.taskId, limit: opts.limit })).payment_attempts;
  }
  async listReceipts(opts: { taskId?: string; limit?: number } = {}): Promise<Receipt[]> {
    return (await this.cp.request<{ receipts: Receipt[] }>("GET", "/v1/receipts", undefined, { task_id: opts.taskId, limit: opts.limit })).receipts;
  }
  async listAudit(limit?: number): Promise<AuditEvent[]> {
    return (await this.cp.request<{ events: AuditEvent[] }>("GET", "/v1/audit", undefined, { limit })).events;
  }
}

/** Operator bootstrap: create an organization and its first owner key. */
export async function createOrganization(
  controlPlaneUrl: string,
  name: string,
  opts: { operatorToken?: string; fetch?: FetchLike } = {},
): Promise<{ organization: Organization; owner_api_key: string }> {
  const http = new Http({
    baseUrl: controlPlaneUrl,
    ...(opts.operatorToken ? { apiKey: opts.operatorToken } : {}),
    ...(opts.fetch ? { fetch: opts.fetch } : {}),
  });
  return http.request("POST", "/v1/organizations", { name });
}
