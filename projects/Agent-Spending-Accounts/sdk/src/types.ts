import type { Amount } from "./amounts.js";

export type OrgStatus = "active" | "paused";
export type AgentStatus = "active" | "revoked";
export type TaskStatus = "active" | "closed";
export type PaymentStatus =
  | "initiated"
  | "rejected"
  | "authorized"
  | "submitted"
  | "settled"
  | "failed"
  | "unknown"
  | "expired";

export interface Organization {
  id: string;
  name: string;
  status: OrgStatus;
  created_at: string;
  updated_at: string;
}

export interface AgentPrincipal {
  id: string;
  organization_id: string;
  name: string;
  status: AgentStatus;
  policy_version_id?: string;
  created_at: string;
  revoked_at?: string;
}

export interface AssetSpec {
  asset: string;
  network: string;
  decimals: number;
  scheme: string;
}

export interface EndpointRule {
  url_prefix: string;
  methods?: string[];
  recipients: string[];
}

export interface PolicyInput {
  asset: AssetSpec;
  per_request_max: Amount;
  per_task_max: Amount;
  agent_daily_max: Amount;
  org_daily_max: Amount;
  fee_allowance: Amount;
  endpoints: EndpointRule[];
  max_task_seconds?: number;
  grant_ttl_seconds?: number;
}

export interface BudgetPolicyVersion extends Required<PolicyInput> {
  id: string;
  organization_id: string;
  agent_id: string;
  version: number;
  created_at: string;
}

export interface Task {
  id: string;
  organization_id: string;
  agent_id: string;
  policy_version_id: string;
  description: string;
  status: TaskStatus;
  budget: Amount;
  deadline: string;
  created_at: string;
  closed_at?: string;
}

export interface TaskBudget {
  task_id: string;
  status: TaskStatus;
  deadline: string;
  asset: AssetSpec;
  task_budget: Amount;
  held: Amount;
  committed: Amount;
  remaining: Amount;
  per_request_max: Amount;
  fee_allowance: Amount;
  spendable: boolean;
}

export interface AccountBalance {
  organization_id: string;
  asset: string;
  network: string;
  available: Amount;
  reserved: Amount;
  updated_at: string;
}

export interface ResourceQuote {
  scheme: string;
  network: string;
  asset: string;
  pay_to: string;
  max_amount_required: Amount;
  resource: string;
  description?: string;
  mime_type?: string;
  max_timeout_seconds: number;
  requirements_hash: string;
}

export interface PaymentAttempt {
  id: string;
  organization_id: string;
  agent_id: string;
  task_id: string;
  policy_version_id: string;
  reservation_id?: string;
  grant_id?: string;
  idempotency_key: string;
  method: string;
  url: string;
  quote?: ResourceQuote;
  status: PaymentStatus;
  reject_reason?: string;
  submission_ref?: string;
  payment_hash?: string;
  payer_ref?: string;
  settled_amount: Amount;
  fee_actual: Amount;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface ServiceOutcome {
  http_status: number;
  delivered: boolean;
  content_type?: string;
  body_sha256?: string;
  body_bytes: number;
  truncated: boolean;
  error?: string;
}

export interface Receipt {
  id: string;
  organization_id: string;
  task_id: string;
  agent_id: string;
  payment_attempt_id: string;
  reservation_id: string;
  policy_version_id: string;
  resource: string;
  quote: ResourceQuote;
  payment_status: PaymentStatus;
  submission_ref?: string;
  settled_amount: Amount;
  fee_actual: Amount;
  service?: ServiceOutcome;
  created_at: string;
}

export interface LedgerEntry {
  id: string;
  organization_id: string;
  journal_id: string;
  account: string;
  direction: "debit" | "credit";
  amount: Amount;
  asset: string;
  network: string;
  memo: string;
  created_at: string;
}

export interface AuditEvent {
  id: string;
  organization_id: string;
  actor: string;
  action: string;
  subject_type: string;
  subject_id: string;
  details?: unknown;
  created_at: string;
}

// --- gateway -----------------------------------------------------------------

export interface PaidRequestInput {
  /** Caller-chosen key; the same key never pays twice. */
  idempotencyKey: string;
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";
  url: string;
  headers?: Record<string, string>;
  /** UTF-8 body. */
  body?: string;
  /** Raw body, base64-encoded (mutually exclusive with body). */
  bodyBase64?: string;
  /** Optional ceiling below the policy's per-request maximum. */
  maxAmount?: Amount;
}

export type PaidPaymentStatus = PaymentStatus | "not_required" | "not_attempted";

export interface PaidPayment {
  status: PaidPaymentStatus;
  amount?: Amount;
  asset?: string;
  network?: string;
  pay_to?: string;
  transaction?: string;
  payer?: string;
  error?: string;
}

export interface PaidService {
  http_status: number;
  delivered: boolean;
  content_type: string;
  body_bytes: number;
  truncated: boolean;
  body?: string;
  body_base64?: string;
}

export interface PaidResponse {
  attempt_id: string;
  replay: boolean;
  payment: PaidPayment;
  service?: PaidService;
  receipt?: Receipt;
}
