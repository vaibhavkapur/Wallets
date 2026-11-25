export type MinorUnits = string; // Always decimal string in JSON; never IEEE-754 float

export interface Money {
  amount_minor: MinorUnits;
  asset: string;
  network: string;
  decimals: number;
}

export type RuleStatus = "active" | "paused" | "canceled";
export type ApprovalMode =
  | "C1_REMINDER_ONLY"
  | "C2_ASSISTED_REORDER"
  | "C3_LIMITED_DELEGATION";
export type ScheduleKind = "calendar" | "interval";
export type CalendarCadence = "weekly" | "monthly";
export type DSTPolicy = "shift_forward_first" | "shift_forward_second";
export type LeapDayPolicy = "clamp_month_end" | "carry_march_first";
export type SubstitutionPolicy =
  | "none"
  | "approval_required"
  | "preauthorized_exact";

export type OccurrenceStatus =
  | "scheduled"
  | "quoting"
  | "awaiting_approval"
  | "approved"
  | "executing"
  | "completed"
  | "skipped"
  | "expired"
  | "canceled"
  | "failed"
  | "needs_reconciliation";

export type PaymentStatus =
  | "uninitiated"
  | "pending"
  | "authorized"
  | "settled"
  | "timeout_unknown"
  | "failed"
  | "refunded"
  | "partially_refunded";

export type FulfillmentStatus =
  | "unfulfilled"
  | "confirmed"
  | "delayed"
  | "shipped"
  | "delivered"
  | "canceled";

export interface ScheduleSpec {
  kind: ScheduleKind;
  timezone: string;
  dst_policy: DSTPolicy;
  leap_day_policy: LeapDayPolicy;
  local_hour: number;
  local_minute: number;
  calendar_cadence?: CalendarCadence;
  day_of_week?: number;
  day_of_month?: number;
  interval_days?: number;
  min_interval_days: number;
  proposal_ttl_minutes: number;
}

export interface ProductConstraint {
  category: string;
  sku: string;
  title: string;
  pack_size_value: number;
  pack_size_unit: string;
  quantity: number;
  preferred_merchant_id: string;
  max_delivered_price: Money;
  monthly_budget: Money;
  substitution_policy: SubstitutionPolicy;
  preauthorized_substitute_skus?: string[];
  payment_method_id: string;
  shipping_postal_code: string;
}

export interface RecurringRuleVersion {
  rule_id: string;
  tenant_id: string;
  version: number;
  name: string;
  status: RuleStatus;
  approval_mode: ApprovalMode;
  schedule: ScheduleSpec;
  product: ProductConstraint;
  next_run_at_utc: string;
  last_completed_utc?: string;
  skipped_slot_keys?: string[];
  created_at: string;
  updated_at: string;
  pause_reason?: string;
}

export interface RecurringRule {
  rule_id: string;
  tenant_id: string;
  current_version: number;
  current: RecurringRuleVersion;
  versions: RecurringRuleVersion[];
}

export interface MerchantQuote {
  quote_id: string;
  occurrence_id: string;
  merchant_id: string;
  merchant_name: string;
  requested_sku: string;
  quoted_sku: string;
  title: string;
  pack_size_value: number;
  pack_size_unit: string;
  quantity: number;
  available_quantity: number;
  stock_status: "in_stock" | "partial_stock" | "out_of_stock";
  item_subtotal: Money;
  shipping_fee: Money;
  tax_amount: Money;
  delivered_total: Money;
  is_substitution: boolean;
  pack_size_changed: boolean;
  substitution_reason?: string;
  quoted_at: string;
  expires_at: string;
  digest: string;
}

export interface PurchaseProposal {
  proposal_id: string;
  tenant_id: string;
  rule_id: string;
  rule_version: number;
  occurrence_id: string;
  quote_id: string;
  quote_digest: string;
  merchant_id: string;
  merchant_name: string;
  sku: string;
  title: string;
  pack_size_label: string;
  quantity: number;
  delivered_total: Money;
  payment_method_id: string;
  payment_method_label: string;
  requires_substitution_approval: boolean;
  substitution_summary?: string;
  policy_warnings?: string[];
  policy_blocked: boolean;
  block_reason?: string;
  reminder_only: boolean;
  outage_recovery: boolean;
  missed_slots_count?: number;
  status: "pending" | "approved" | "rejected" | "expired" | "superseded";
  created_at: string;
  expires_at: string;
  decided_at?: string;
}

export interface ConsentRecord {
  consent_id: string;
  tenant_id: string;
  kind: "proposal_approval" | "delegated_mandate";
  rule_id: string;
  rule_version: number;
  proposal_id?: string;
  merchant_id: string;
  sku: string;
  max_amount: Money;
  payment_method_id: string;
  provider_mandate_ref?: string;
  granted_by: string;
  granted_at: string;
  expires_at: string;
  revoked_at?: string;
  signature_digest: string;
}

export interface BudgetReservation {
  reservation_id: string;
  rule_id: string;
  occurrence_id: string;
  budget_period: string;
  reserved: Money;
  settled: Money;
  refunded: Money;
  status:
    | "reserved"
    | "settled"
    | "released"
    | "partially_refunded"
    | "uncertain_held";
}

export interface CheckoutReference {
  checkout_id: string;
  occurrence_id: string;
  idempotency_key: string;
  provider: string;
  merchant_id: string;
  merchant_order_id?: string;
  payment_intent_id?: string;
  payment_method_id: string;
  payment_status: PaymentStatus;
  fulfillment_status: FulfillmentStatus;
  charged_amount: Money;
  refunded_amount: Money;
  cancel_requested: boolean;
  cancel_outcome?: string;
  last_error?: string;
  last_reconciled_at?: string;
}

export interface ScheduleOccurrence {
  occurrence_id: string;
  tenant_id: string;
  rule_id: string;
  rule_version: number;
  slot_key: string;
  idempotency_key: string;
  scheduled_for_utc: string;
  scheduled_local_display: string;
  status: OccurrenceStatus;
  payment_status: PaymentStatus;
  fulfillment_status: FulfillmentStatus;
  approval_mode: ApprovalMode;
  outage_recovery: boolean;
  missed_slots_count?: number;
  pause_raced_submission?: boolean;
  quote_id?: string;
  proposal_id?: string;
  consent_id?: string;
  reservation_id?: string;
  checkout_id?: string;
  status_reason?: string;
  created_at: string;
  updated_at: string;
}

export interface OccurrenceDetail {
  occurrence: ScheduleOccurrence;
  quote?: MerchantQuote;
  proposal?: PurchaseProposal;
  consent?: ConsentRecord;
  reservation?: BudgetReservation;
  checkout?: CheckoutReference;
}

export interface NotificationEvent {
  event_id: string;
  rule_id: string;
  occurrence_id?: string;
  kind: string;
  channel: string;
  status: string;
  message: string;
  attempts: number;
  created_at: string;
  dispatched_at?: string;
}

export interface AuditEvent {
  audit_id: string;
  actor: string;
  action: string;
  resource_type: string;
  resource_id: string;
  detail?: Record<string, string>;
  created_at: string;
}

export interface GlobalPauseState {
  paused: boolean;
  reason?: string;
  updated_by?: string;
  updated_at?: string;
}

export interface SandboxFaultConfig {
  price_multiplier_bps: number;
  force_pack_size_value?: number;
  force_stock_status?: string;
  force_available_qty?: number;
  force_substitute_sku?: string;
  force_payment_behavior?: string;
  enable_c3_delegation: boolean;
}

export interface MerchantListing {
  merchant_id: string;
  merchant_name: string;
  unit_price: Money;
  shipping_fee: Money;
  tax_rate_bps: number;
  protocol: string;
}

export interface CatalogItem {
  sku: string;
  category: string;
  brand: string;
  title: string;
  pack_size_value: number;
  pack_size_unit: string;
  default_unit_price: Money;
  merchants: MerchantListing[];
  suggested_substitutes?: string[];
}

export interface PaymentMethod {
  id: string;
  label: string;
  network: string;
  last4: string;
  rail: string;
  scoped_credential: boolean;
  supports_delegation: boolean;
}

export interface BudgetSummary {
  period: string;
  used: Money;
  remaining: Money;
  cap: Money;
}

export interface WorkspaceSnapshot {
  tenant: string;
  role: "customer" | "operator";
  now_utc: string;
  demo_mode: boolean;
  global_pause: GlobalPauseState;
  faults: SandboxFaultConfig;
  rules: RecurringRule[];
  occurrences: OccurrenceDetail[];
  consents: ConsentRecord[];
  budget_usage: Record<string, BudgetSummary>;
  notifications: NotificationEvent[];
  audits: AuditEvent[];
}
