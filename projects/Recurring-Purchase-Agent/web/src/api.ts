import type {
  ApprovalMode,
  CatalogItem,
  ConsentRecord,
  FulfillmentStatus,
  GlobalPauseState,
  Money,
  OccurrenceDetail,
  PaymentMethod,
  ProductConstraint,
  RecurringRule,
  SandboxFaultConfig,
  ScheduleSpec,
  WorkspaceSnapshot,
} from "./types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  const text = await resp.text();
  const data = text ? JSON.parse(text) : {};
  if (!resp.ok) {
    throw new Error(data.message || data.error || `Request failed (${resp.status})`);
  }
  return data as T;
}

export const api = {
  openSession: (tenant = "household-demo", role: "customer" | "operator" = "customer", operatorToken = "") =>
    request<{ tenant: string; role: "customer" | "operator"; actor: string }>("/v1/session", {
      method: "POST",
      body: JSON.stringify({ tenant, role, operator_token: operatorToken }),
    }),

  getCatalog: () =>
    request<{ catalog: CatalogItem[]; payment_methods: PaymentMethod[] }>("/v1/catalog"),

  getWorkspace: () => request<WorkspaceSnapshot>("/v1/workspace"),

  createRule: (payload: {
    name: string;
    approval_mode: ApprovalMode;
    schedule: ScheduleSpec;
    product: ProductConstraint;
  }) =>
    request<RecurringRule>("/v1/recurring-rules", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  updateRule: (
    ruleId: string,
    payload: Partial<{
      name: string;
      approval_mode: ApprovalMode;
      schedule: ScheduleSpec;
      product: ProductConstraint;
    }>,
  ) =>
    request<RecurringRule>(`/v1/recurring-rules/${ruleId}`, {
      method: "PATCH",
      body: JSON.stringify(payload),
    }),

  pauseRule: (ruleId: string, reason: string) =>
    request<{
      rule: RecurringRule;
      already_submitted_orders: string[];
      cancels_submitted_orders: boolean;
      stops_future_occurrences: boolean;
    }>(`/v1/recurring-rules/${ruleId}/pause`, {
      method: "POST",
      body: JSON.stringify({ reason }),
    }),

  resumeRule: (ruleId: string) =>
    request<RecurringRule>(`/v1/recurring-rules/${ruleId}/resume`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  cancelRule: (ruleId: string) =>
    request<RecurringRule>(`/v1/recurring-rules/${ruleId}/cancel`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  skipNext: (ruleId: string) =>
    request<{ rule: RecurringRule }>(`/v1/recurring-rules/${ruleId}/skip-next`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  grantMandate: (
    ruleId: string,
    payload: {
      max_amount: Money;
      payment_method_id: string;
      provider_mandate_ref: string;
      ttl_hours: number;
    },
  ) =>
    request<ConsentRecord>(`/v1/recurring-rules/${ruleId}/mandates`, {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  revokeConsent: (consentId: string) =>
    request<ConsentRecord>(`/v1/consents/${consentId}/revoke`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  approveProposal: (
    proposalId: string,
    payload: {
      expected_quote_digest?: string;
      expected_total_minor?: string;
      accept_substitution?: boolean;
    },
  ) =>
    request<OccurrenceDetail>(`/v1/proposals/${proposalId}/approve`, {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  rejectProposal: (proposalId: string, reason: string) =>
    request(`/v1/proposals/${proposalId}/reject`, {
      method: "POST",
      body: JSON.stringify({ reason }),
    }),

  reconcileOccurrence: (occurrenceId: string) =>
    request<OccurrenceDetail>(`/v1/occurrences/${occurrenceId}/reconcile`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  cancelSubmittedOrder: (occurrenceId: string) =>
    request<OccurrenceDetail>(`/v1/occurrences/${occurrenceId}/cancel-order`, {
      method: "POST",
      body: JSON.stringify({}),
    }),

  setOperatorPause: (paused: boolean, reason: string) =>
    request<GlobalPauseState>("/v1/operator/pause", {
      method: "POST",
      body: JSON.stringify({ paused, reason }),
    }),

  sandboxTick: (advanceMinutes: number, setTimeUTC?: string) =>
    request<{ now_utc: string; jobs_processed: number }>("/v1/sandbox/tick", {
      method: "POST",
      body: JSON.stringify({
        advance_minutes: advanceMinutes,
        set_time_utc: setTimeUTC ?? "",
      }),
    }),

  sandboxFaults: (faults: SandboxFaultConfig) =>
    request<SandboxFaultConfig>("/v1/sandbox/faults", {
      method: "PUT",
      body: JSON.stringify(faults),
    }),

  sandboxFulfill: (
    occurrenceId: string,
    payload: {
      fulfillment_status?: FulfillmentStatus;
      partial_refund?: Money;
      full_refund?: boolean;
      note?: string;
    },
  ) =>
    request<OccurrenceDetail>(`/v1/sandbox/fulfill/${occurrenceId}`, {
      method: "POST",
      body: JSON.stringify(payload),
    }),
};
