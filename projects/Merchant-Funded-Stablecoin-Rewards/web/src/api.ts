import type { AmountString } from "./money";

export interface Merchant {
  id: string;
  tenant_id: string;
  name: string;
  status: string;
  webhook_secret_hint: string;
  settlement_currency: string;
  terms_accepted_at: string;
  created_at: string;
}

export interface CampaignView {
  id: string;
  campaign_id: string;
  merchant_id: string;
  merchant_name: string;
  version: number;
  name: string;
  status: "draft" | "active" | "paused" | "ended";
  reward_type: "fixed" | "percentage";
  fixed_reward_minor: AmountString;
  percentage_bps: number;
  currency: string;
  decimals: number;
  budget_cap_minor: AmountString;
  customer_cap_minor: AmountString;
  per_order_cap_minor: AmountString;
  min_order_minor: AmountString;
  eligible_skus: string[];
  return_window_seconds: number;
  return_treatment: "pro_rata" | "full_reversal";
  platform_fee_bps: number;
  loss_reserve_bps: number;
  starts_at: string;
  ends_at: string;
  funded_minor: AmountString;
  available_minor: AmountString;
  reserved_minor: AmountString;
  loss_reserve_minor: AmountString;
  promised_reward_minor: AmountString;
  paid_reward_minor: AmountString;
}

export interface RewardAccrual {
  id: string;
  customer_id: string;
  merchant_id: string;
  merchant_name: string;
  campaign_id: string;
  campaign_name: string;
  campaign_version_id: string;
  order_id: string;
  asset: string;
  decimals: number;
  original_amount_minor: AmountString;
  reversed_amount_minor: AmountString;
  net_amount_minor: AmountString;
  platform_fee_minor: AmountString;
  loss_reserve_minor: AmountString;
  state: "pending" | "eligible" | "payout_requested" | "paid" | "reversed";
  payment_settled: boolean;
  fulfilled: boolean;
  eligible_at?: string;
  payout_id?: string;
  payout_fee_base?: AmountString;
  post_payout_disputed?: boolean;
  created_at: string;
  updated_at: string;
}

export interface BeneficiaryVersion {
  id: string;
  customer_id: string;
  version: number;
  asset: string;
  network: string;
  decimals: number;
  address: string;
  verification_status: "pending" | "verified" | "revoked";
  recovery_disclosed: boolean;
  verified_at?: string;
  revoked_at?: string;
  created_at: string;
}

export interface CustomerRewardsSummary {
  customer_id: string;
  asset: string;
  decimals: number;
  pending_minor: AmountString;
  eligible_minor: AmountString;
  payout_requested_minor: AmountString;
  paid_minor: AmountString;
  reversed_minor: AmountString;
  min_payout_minor: AmountString;
  payout_asset: string;
  payout_network: string;
  payout_decimals: number;
  payout_fee_usdc_base: AmountString;
  active_beneficiary?: BeneficiaryVersion;
  rewards: RewardAccrual[];
}

export interface PayoutQuote {
  id: string;
  customer_id: string;
  beneficiary_version_id: string;
  destination_address: string;
  reward_ids: string[];
  source_asset: string;
  source_decimals: number;
  source_amount_base: AmountString;
  payout_asset: string;
  payout_network: string;
  payout_decimals: number;
  gross_payout_base: AmountString;
  fee_base: AmountString;
  net_payout_base: AmountString;
  fee_bearer: string;
  fx_rate_bps: number;
  min_threshold_minor: AmountString;
  disclosure: string;
  expires_at: string;
  created_at: string;
}

export interface PayoutAttempt {
  id: string;
  logical_payout_id: string;
  attempt_number: number;
  customer_id: string;
  quote_id: string;
  beneficiary_version_id: string;
  destination_address: string;
  reward_ids: string[];
  idempotency_key: string;
  source_asset: string;
  source_amount_minor: AmountString;
  payout_asset: string;
  payout_network: string;
  payout_decimals: number;
  gross_payout_base: AmountString;
  fee_base: AmountString;
  net_payout_base: AmountString;
  fee_bearer: string;
  status: "prepared" | "submitted" | "pending" | "succeeded" | "failed" | "outcome_unknown";
  provider_ref?: string;
  tx_hash?: string;
  confirmations: number;
  failure_reason?: string;
  created_at: string;
  updated_at: string;
}

export interface ReconciliationException {
  id: string;
  category: string;
  severity: "info" | "warning" | "critical";
  status: "open" | "acknowledged" | "resolved";
  entity_type: string;
  entity_id: string;
  asset: string;
  decimals: number;
  expected_base: AmountString;
  actual_base: AmountString;
  delta_base: AmountString;
  summary: string;
  resolution_note?: string;
  resolved_by?: string;
  created_at: string;
  resolved_at?: string;
}

export interface AssetTrialBalance {
  asset: string;
  network: string;
  decimals: number;
  total_debits: AmountString;
  total_credits: AmountString;
  balanced: boolean;
}

export interface LedgerAccount {
  id: string;
  code: string;
  category: string;
  scope_id: string;
  asset: string;
  network: string;
  decimals: number;
  balance: AmountString;
}

export interface LedgerSummary {
  balanced: boolean;
  journal_count: number;
  trial_balance: AssetTrialBalance[];
  provider_custody_usd: AmountString;
  campaign_available_usd: AmountString;
  campaign_reserved_usd: AmountString;
  reward_pending_usd: AmountString;
  reward_payable_usd: AmountString;
  payout_clearing_usd: AmountString;
  loss_reserve_usd: AmountString;
  platform_revenue_usd: AmountString;
  total_obligations_usd: AmountString;
  funding_covers_obligations: boolean;
  accounts: LedgerAccount[];
}

export interface JournalEntry {
  id: string;
  entry_type: string;
  reference_id: string;
  description: string;
  postings: {
    account_code: string;
    scope_id: string;
    asset: string;
    network: string;
    decimals: number;
    direction: "debit" | "credit";
    amount: AmountString;
  }[];
  created_at: string;
}

export interface PauseControl {
  issuance_paused: boolean;
  payouts_paused: boolean;
  reason?: string;
  updated_by?: string;
  updated_at?: string;
}

export interface SystemStatus {
  tenant_id: string;
  server_time: string;
  pause: PauseControl;
  merchant_count: number;
  campaign_count: number;
  reward_count: number;
  payout_attempt_count: number;
  open_exceptions: number;
  last_reconciled_at?: string;
}

export interface WorkerTickReport {
  tenant_id: string;
  run_at: string;
  rewards_made_eligible: number;
  payouts_reconciled: number;
  custody_drift_usd_minor: AmountString;
  ledger_balanced: boolean;
  funding_covers_obligations: boolean;
  new_exceptions_opened: number;
}

async function request<T>(
  path: string,
  opts: {
    method?: string;
    body?: unknown;
    customerId?: string;
    operatorToken?: string;
    idempotencyKey?: string;
  } = {},
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Tenant-ID": "default",
  };
  if (opts.customerId) headers["X-Customer-ID"] = opts.customerId;
  if (opts.operatorToken) headers["X-Operator-Token"] = opts.operatorToken;
  if (opts.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;

  const res = await fetch(path, {
    method: opts.method ?? "GET",
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((data as { error?: string }).error || `Request failed (${res.status})`);
  }
  return data as T;
}

export const api = {
  getSystemStatus: () => request<SystemStatus>("/v1/system/status"),
  listMerchants: () => request<{ merchants: Merchant[] }>("/v1/merchants"),
  listCampaigns: () => request<{ campaigns: CampaignView[] }>("/v1/campaigns"),
  createCampaign: (body: unknown) => request<CampaignView>("/v1/campaigns", { method: "POST", body }),
  fundCampaign: (id: string, body: unknown) =>
    request("/v1/campaigns/" + encodeURIComponent(id) + "/funding", { method: "POST", body }),
  setCampaignStatus: (id: string, status: string, operatorToken?: string) =>
    request<CampaignView>("/v1/campaigns/" + encodeURIComponent(id) + "/status", {
      method: "POST",
      body: { status },
      operatorToken,
    }),
  simulateMerchantEvent: (body: unknown) =>
    request<{ deduplicated: boolean; reward?: RewardAccrual; exception?: ReconciliationException }>(
      "/v1/sandbox/simulate-merchant-event",
      { method: "POST", body },
    ),

  getCustomerRewards: (customerId: string) =>
    request<CustomerRewardsSummary>("/v1/rewards?customer_id=" + encodeURIComponent(customerId), { customerId }),
  listBeneficiaries: (customerId: string) =>
    request<{ beneficiaries: BeneficiaryVersion[] }>(
      "/v1/beneficiaries?customer_id=" + encodeURIComponent(customerId),
      { customerId },
    ),
  registerBeneficiary: (customerId: string, body: unknown) =>
    request<BeneficiaryVersion>("/v1/beneficiaries", { method: "POST", body, customerId }),
  createPayoutQuote: (customerId: string, body: unknown) =>
    request<PayoutQuote>("/v1/payout-quotes", { method: "POST", body, customerId }),
  authorizePayout: (customerId: string, idempotencyKey: string, body: unknown) =>
    request<PayoutAttempt>("/v1/payouts", { method: "POST", body, customerId, idempotencyKey }),
  listPayouts: (customerId: string) =>
    request<{ payouts: PayoutAttempt[] }>("/v1/payouts?customer_id=" + encodeURIComponent(customerId), {
      customerId,
    }),
  retryPayout: (customerId: string, logicalId: string, idempotencyKey: string, sandboxScenario = "") =>
    request<PayoutAttempt>("/v1/payouts/" + encodeURIComponent(logicalId) + "/retry", {
      method: "POST",
      customerId,
      idempotencyKey,
      body: { sandbox_scenario: sandboxScenario },
    }),

  listExceptions: (operatorToken: string) =>
    request<{ exceptions: ReconciliationException[] }>("/v1/reconciliation-exceptions", { operatorToken }),
  resolveException: (operatorToken: string, id: string, resolution_note: string) =>
    request<ReconciliationException>(
      "/v1/reconciliation-exceptions/" + encodeURIComponent(id) + "/resolve",
      { method: "POST", operatorToken, body: { resolution_note, resolved_by: "operator" } },
    ),
  getLedgerSummary: (operatorToken: string) =>
    request<LedgerSummary>("/v1/ledger/summary", { operatorToken }),
  listLedgerEntries: (operatorToken: string) =>
    request<{ entries: JournalEntry[] }>("/v1/ledger/entries", { operatorToken }),
  setPause: (operatorToken: string, issuance_paused: boolean, payouts_paused: boolean, reason: string) =>
    request<PauseControl>("/v1/system/pause", {
      method: "POST",
      operatorToken,
      body: { issuance_paused, payouts_paused, reason },
    }),
  runReconciliation: (operatorToken: string) =>
    request<WorkerTickReport>("/v1/reconciliation/run", { method: "POST", operatorToken, body: {} }),
  advanceClock: (operatorToken: string, seconds: number) =>
    request<{ server_time: string; worker_report: WorkerTickReport }>("/v1/sandbox/advance-clock", {
      method: "POST",
      operatorToken,
      body: { seconds },
    }),
  setCustodyDrift: (operatorToken: string, drift_usd_minor: AmountString) =>
    request<WorkerTickReport>("/v1/sandbox/custody-drift", {
      method: "POST",
      operatorToken,
      body: { drift_usd_minor },
    }),
};
