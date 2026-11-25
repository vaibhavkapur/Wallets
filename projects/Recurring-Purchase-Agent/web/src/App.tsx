import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "./api";
import {
  budgetPercent,
  describeSchedule,
  formatMoney,
  formatTimeShort,
  parseUSDToMoney,
} from "./money";
import type {
  ApprovalMode,
  CalendarCadence,
  CatalogItem,
  DSTPolicy,
  LeapDayPolicy,
  OccurrenceDetail,
  PaymentMethod,
  RecurringRule,
  SandboxFaultConfig,
  ScheduleKind,
  SubstitutionPolicy,
  WorkspaceSnapshot,
} from "./types";

type TabKey = "approvals" | "rules" | "orders" | "ops";

const TIMEZONES = [
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "Europe/London",
  "UTC",
];

function statusTone(status: string): string {
  switch (status) {
    case "active":
    case "completed":
    case "settled":
    case "delivered":
    case "approved":
    case "dispatched":
      return "ok";
    case "paused":
    case "awaiting_approval":
    case "pending":
    case "delayed":
    case "needs_reconciliation":
    case "timeout_unknown":
    case "uncertain_held":
    case "partially_refunded":
      return "warn";
    case "canceled":
    case "failed":
    case "expired":
    case "rejected":
    case "out_of_stock":
    case "partial_stock":
      return "danger";
    default:
      return "";
  }
}

export function App() {
  const [tab, setTab] = useState<TabKey>("approvals");
  const [catalog, setCatalog] = useState<CatalogItem[]>([]);
  const [paymentMethods, setPaymentMethods] = useState<PaymentMethod[]>([]);
  const [workspace, setWorkspace] = useState<WorkspaceSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: "ok" | "danger" | "warn"; text: string } | null>(null);

  // Rule creation / edit state
  const [editingRuleId, setEditingRuleId] = useState<string | null>(null);
  const [ruleName, setRuleName] = useState("Morning Ethiopia Coffee");
  const [sku, setSku] = useState("COFFEE-ETHIOPIA-12OZ");
  const [merchantId, setMerchantId] = useState("roaster_direct");
  const [quantity, setQuantity] = useState(1);
  const [maxPriceStr, setMaxPriceStr] = useState("30.00");
  const [monthlyBudgetStr, setMonthlyBudgetStr] = useState("75.00");
  const [scheduleKind, setScheduleKind] = useState<ScheduleKind>("interval");
  const [intervalDays, setIntervalDays] = useState(14);
  const [calendarCadence, setCalendarCadence] = useState<CalendarCadence>("monthly");
  const [dayOfWeek, setDayOfWeek] = useState(1);
  const [dayOfMonth, setDayOfMonth] = useState(15);
  const [localHour, setLocalHour] = useState(9);
  const [localMinute, setLocalMinute] = useState(0);
  const [timezone, setTimezone] = useState("America/New_York");
  const [dstPolicy, setDstPolicy] = useState<DSTPolicy>("shift_forward_first");
  const [leapPolicy, setLeapPolicy] = useState<LeapDayPolicy>("clamp_month_end");
  const [minIntervalDays, setMinIntervalDays] = useState(10);
  const [proposalTTLMinutes, setProposalTTLMinutes] = useState(1440);
  const [substitutionPolicy, setSubstitutionPolicy] = useState<SubstitutionPolicy>("approval_required");
  const [preauthSubSKU, setPreauthSubSKU] = useState("COFFEE-COLOMBIA-12OZ");
  const [paymentMethodId, setPaymentMethodId] = useState("pm_card_everyday");
  const [approvalMode, setApprovalMode] = useState<ApprovalMode>("C2_ASSISTED_REORDER");

  // Explicit substitution checkbox per proposal
  const [acceptedSubs, setAcceptedSubs] = useState<Record<string, boolean>>({});

  // Operator controls
  const [opToken, setOpToken] = useState("local-operator");
  const [globalPauseReason, setGlobalPauseReason] = useState("Investigating merchant pricing feed");

  const refresh = useCallback(async () => {
    const ws = await api.getWorkspace();
    setWorkspace(ws);
  }, []);

  useEffect(() => {
    (async () => {
      try {
        await api.openSession("household-demo", "customer");
        const [cat, ws] = await Promise.all([api.getCatalog(), api.getWorkspace()]);
        setCatalog(cat.catalog);
        setPaymentMethods(cat.payment_methods);
        setWorkspace(ws);
      } catch (err) {
        setNotice({ tone: "danger", text: err instanceof Error ? err.message : "Failed to initialize." });
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  const selectedCatalogItem = useMemo(
    () => catalog.find((c) => c.sku === sku) ?? catalog[0],
    [catalog, sku],
  );

  const runAction = async (fn: () => Promise<void>, successMsg?: string) => {
    setBusy(true);
    setNotice(null);
    try {
      await fn();
      await refresh();
      if (successMsg) setNotice({ tone: "ok", text: successMsg });
    } catch (err) {
      setNotice({ tone: "danger", text: err instanceof Error ? err.message : "Action failed." });
    } finally {
      setBusy(false);
    }
  };

  const handleSelectSKU = (nextSKU: string) => {
    setSku(nextSKU);
    const found = catalog.find((c) => c.sku === nextSKU);
    if (found) {
      setRuleName(`${found.title} (${found.pack_size_value} ${found.pack_size_unit})`);
      if (found.merchants.length > 0) {
        setMerchantId(found.merchants[0].merchant_id);
      }
      if (found.suggested_substitutes && found.suggested_substitutes.length > 0) {
        setPreauthSubSKU(found.suggested_substitutes[0]);
      }
    }
  };

  const loadRuleIntoEditor = (rule: RecurringRule) => {
    const c = rule.current;
    setEditingRuleId(rule.rule_id);
    setRuleName(c.name);
    setSku(c.product.sku);
    setMerchantId(c.product.preferred_merchant_id);
    setQuantity(c.product.quantity);
    setMaxPriceStr((Number(c.product.max_delivered_price.amount_minor) / 100).toFixed(2));
    setMonthlyBudgetStr((Number(c.product.monthly_budget.amount_minor) / 100).toFixed(2));
    setScheduleKind(c.schedule.kind);
    setIntervalDays(c.schedule.interval_days ?? 14);
    setCalendarCadence(c.schedule.calendar_cadence ?? "monthly");
    setDayOfWeek(c.schedule.day_of_week ?? 1);
    setDayOfMonth(c.schedule.day_of_month ?? 15);
    setLocalHour(c.schedule.local_hour);
    setLocalMinute(c.schedule.local_minute);
    setTimezone(c.schedule.timezone);
    setDstPolicy(c.schedule.dst_policy);
    setLeapPolicy(c.schedule.leap_day_policy);
    setMinIntervalDays(c.schedule.min_interval_days);
    setProposalTTLMinutes(c.schedule.proposal_ttl_minutes);
    setSubstitutionPolicy(c.product.substitution_policy);
    if (c.product.preauthorized_substitute_skus?.[0]) {
      setPreauthSubSKU(c.product.preauthorized_substitute_skus[0]);
    }
    setPaymentMethodId(c.product.payment_method_id);
    setApprovalMode(c.approval_mode);
    setTab("rules");
  };

  const handleSaveRule = async (e: React.FormEvent) => {
    e.preventDefault();
    const maxPrice = parseUSDToMoney(maxPriceStr);
    const monthlyBudget = parseUSDToMoney(monthlyBudgetStr);
    if (!maxPrice || !monthlyBudget) {
      setNotice({ tone: "danger", text: "Enter valid USD dollar amounts (e.g., 30.00)." });
      return;
    }
    const item = selectedCatalogItem;
    if (!item) return;

    const schedule =
      scheduleKind === "interval"
        ? {
            kind: "interval" as const,
            timezone,
            dst_policy: dstPolicy,
            leap_day_policy: leapPolicy,
            local_hour: localHour,
            local_minute: localMinute,
            interval_days: intervalDays,
            min_interval_days: minIntervalDays,
            proposal_ttl_minutes: proposalTTLMinutes,
          }
        : {
            kind: "calendar" as const,
            timezone,
            dst_policy: dstPolicy,
            leap_day_policy: leapPolicy,
            local_hour: localHour,
            local_minute: localMinute,
            calendar_cadence: calendarCadence,
            ...(calendarCadence === "weekly"
              ? { day_of_week: dayOfWeek }
              : { day_of_month: dayOfMonth }),
            min_interval_days: minIntervalDays,
            proposal_ttl_minutes: proposalTTLMinutes,
          };

    const product = {
      category: item.category,
      sku: item.sku,
      title: item.title,
      pack_size_value: item.pack_size_value,
      pack_size_unit: item.pack_size_unit,
      quantity,
      preferred_merchant_id: merchantId,
      max_delivered_price: maxPrice,
      monthly_budget: monthlyBudget,
      substitution_policy: substitutionPolicy,
      preauthorized_substitute_skus:
        substitutionPolicy === "preauthorized_exact" ? [preauthSubSKU] : undefined,
      payment_method_id: paymentMethodId,
      shipping_postal_code: "94103",
    };

    if (editingRuleId) {
      await runAction(async () => {
        await api.updateRule(editingRuleId, {
          name: ruleName,
          approval_mode: approvalMode,
          schedule,
          product,
        });
        setEditingRuleId(null);
      }, "Created new immutable rule version; any older pending proposals were superseded.");
    } else {
      await runAction(async () => {
        await api.createRule({
          name: ruleName,
          approval_mode: approvalMode,
          schedule,
          product,
        });
      }, "Created recurring reorder rule (v1). Use Sandbox Tick in the top bar or Operations tab to simulate the next due window.");
    }
  };

  const pendingProposals = useMemo(
    () =>
      (workspace?.occurrences ?? []).filter(
        (d) => d.proposal && d.proposal.status === "pending",
      ),
    [workspace],
  );

  const reconciliationQueue = useMemo(
    () =>
      (workspace?.occurrences ?? []).filter(
        (d) => d.occurrence.status === "needs_reconciliation",
      ),
    [workspace],
  );

  const activeRuleCount = useMemo(
    () => (workspace?.rules ?? []).filter((r) => r.current.status === "active").length,
    [workspace],
  );

  const completedOrderCount = useMemo(
    () =>
      (workspace?.occurrences ?? []).filter(
        (d) => d.occurrence.status === "completed",
      ).length,
    [workspace],
  );

  if (loading) {
    return (
      <div className="app-shell">
        <div className="card">Initializing sandbox session...</div>
      </div>
    );
  }

  const updateFault = (patch: Partial<SandboxFaultConfig>) => {
    if (!workspace) return;
    runAction(
      () => api.sandboxFaults({ ...workspace.faults, ...patch }).then(() => {}),
      "Updated sandbox scenario settings.",
    );
  };

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark">R</div>
          <div>
            <h1>Recurring Purchase Agent</h1>
            <div className="brand-sub">
              Exact-item essentials reorders · Timezone-safe scheduling · Deterministic policy &amp; reconciliation
            </div>
          </div>
        </div>
        <div className="topbar-meta">
          <div className="clock-pill">
            <span>Sandbox Clock:</span>
            <strong>{formatTimeShort(workspace?.now_utc)}</strong>
          </div>
          {workspace?.demo_mode && (
            <button
              className="small primary"
              disabled={busy}
              onClick={() =>
                runAction(
                  () => api.sandboxTick(24 * 60).then(() => {}),
                  "Advanced clock +1 day and ran worker recovery scan.",
                )
              }
            >
              +1 Day &amp; Scan
            </button>
          )}
          <span className={`pill ${workspace?.role === "operator" ? "brand" : ""}`}>
            {workspace?.role === "operator" ? "Operator" : "Customer"} · {workspace?.tenant}
          </span>
        </div>
      </header>

      {workspace?.global_pause.paused && (
        <div className="banner danger">
          <div>
            <strong>Product-level emergency pause is active.</strong>{" "}
            {workspace.global_pause.reason || "New executions and approvals are temporarily halted."}
          </div>
          <span className="pill danger">GLOBAL PAUSE</span>
        </div>
      )}

      {notice && (
        <div className={`banner ${notice.tone}`}>
          <span>{notice.text}</span>
          <button className="small" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </div>
      )}

      <div className="stat-strip">
        <div className="stat-card">
          <div className="stat-label">Active Rules</div>
          <div className="stat-value">{activeRuleCount}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Pending Proposals / Reminders</div>
          <div className="stat-value">{pendingProposals.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Needs Reconciliation</div>
          <div className="stat-value">{reconciliationQueue.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Completed Orders</div>
          <div className="stat-value">{completedOrderCount}</div>
        </div>
      </div>

      <nav className="nav-tabs">
        <button
          className={`nav-tab ${tab === "approvals" ? "active" : ""}`}
          onClick={() => setTab("approvals")}
        >
          Approvals &amp; Reminders
          {pendingProposals.length > 0 && <span className="badge">{pendingProposals.length}</span>}
        </button>
        <button
          className={`nav-tab ${tab === "rules" ? "active" : ""}`}
          onClick={() => setTab("rules")}
        >
          Reorder Rules ({workspace?.rules.length ?? 0})
        </button>
        <button
          className={`nav-tab ${tab === "orders" ? "active" : ""}`}
          onClick={() => setTab("orders")}
        >
          Orders &amp; Occurrences ({workspace?.occurrences.length ?? 0})
        </button>
        <button
          className={`nav-tab ${tab === "ops" ? "active" : ""}`}
          onClick={() => setTab("ops")}
        >
          Operations &amp; Sandbox Lab
        </button>
      </nav>

      {/* TAB 1: APPROVALS & REMINDERS */}
      {tab === "approvals" && (
        <div>
          {reconciliationQueue.length > 0 && (
            <div className="card">
              <div className="card-header">
                <div>
                  <h2 className="card-title">Uncertain Payment Outcomes (Reconciliation Required)</h2>
                  <p className="muted">
                    Provider timed out. Budget stays reserved and no second charge will be submitted until reconciled.
                  </p>
                </div>
                <span className="pill warn">timeout_unknown</span>
              </div>
              {reconciliationQueue.map((d) => (
                <div key={d.occurrence.occurrence_id} className="banner warn" style={{ marginBottom: 8 }}>
                  <div>
                    <strong>{d.proposal?.title ?? d.occurrence.occurrence_id}</strong> ·{" "}
                    <span className="mono">{d.occurrence.idempotency_key}</span>
                    <div className="muted">{d.occurrence.status_reason}</div>
                  </div>
                  <button
                    className="primary small"
                    disabled={busy}
                    onClick={() =>
                      runAction(
                        () => api.reconcileOccurrence(d.occurrence.occurrence_id).then(() => {}),
                        "Reconciled provider payment status using original idempotency key.",
                      )
                    }
                  >
                    Reconcile Outcome
                  </button>
                </div>
              ))}
            </div>
          )}

          {pendingProposals.length === 0 ? (
            <div className="empty-state">
              <h3 style={{ marginBottom: 6 }}>No pending proposals or reminders right now</h3>
              <p className="muted" style={{ marginBottom: 14 }}>
                Create a recurring coffee or household reorder rule, then advance the sandbox clock to generate a fresh merchant quote and approval request.
              </p>
              <button className="primary" onClick={() => setTab("rules")}>
                Create or Manage Rules
              </button>
            </div>
          ) : (
            pendingProposals.map((d) => {
              const prop = d.proposal!;
              const quote = d.quote;
              const subChecked = Boolean(acceptedSubs[prop.proposal_id]);
              return (
                <div key={prop.proposal_id} className="card">
                  <div className="card-header">
                    <div>
                      <div className="pill-row" style={{ marginBottom: 6 }}>
                        <span className="pill">Rule v{prop.rule_version}</span>
                        <span className="pill">{d.occurrence.approval_mode}</span>
                        {prop.reminder_only && <span className="pill brand">C1 Reminder Only</span>}
                        {prop.outage_recovery && (
                          <span className="pill warn">
                            Outage Recovery ({prop.missed_slots_count} missed slots coalesced into 1)
                          </span>
                        )}
                        {prop.policy_blocked && <span className="pill danger">Policy Blocked</span>}
                        {prop.requires_substitution_approval && (
                          <span className="pill warn">Substitution / Pack Change</span>
                        )}
                      </div>
                      <h2 className="card-title">
                        {prop.title} · {prop.quantity} × {prop.pack_size_label}
                      </h2>
                      <p className="muted">
                        Merchant: <strong>{prop.merchant_name}</strong> · Payment:{" "}
                        <strong>{prop.payment_method_label}</strong> · Slot:{" "}
                        {d.occurrence.scheduled_local_display} · Expires: {formatTimeShort(prop.expires_at)}
                      </p>
                    </div>
                    <div style={{ textAlign: "right" }}>
                      <div className="muted">Delivered Total</div>
                      <div style={{ fontSize: 22, fontWeight: 700 }}>
                        {formatMoney(prop.delivered_total)}
                      </div>
                    </div>
                  </div>

                  {quote && (
                    <div className="price-breakdown">
                      <div>
                        <div className="label">Items ({quote.quantity})</div>
                        <div className="val">{formatMoney(quote.item_subtotal)}</div>
                      </div>
                      <div>
                        <div className="label">Shipping</div>
                        <div className="val">{formatMoney(quote.shipping_fee)}</div>
                      </div>
                      <div>
                        <div className="label">Estimated Tax</div>
                        <div className="val">{formatMoney(quote.tax_amount)}</div>
                      </div>
                      <div>
                        <div className="label">Stock &amp; Quote Digest</div>
                        <div className="val">
                          <span className={`pill ${statusTone(quote.stock_status)}`}>
                            {quote.stock_status} ({quote.available_quantity} avail)
                          </span>
                        </div>
                      </div>
                    </div>
                  )}

                  {prop.policy_warnings && prop.policy_warnings.length > 0 && (
                    <div className={`banner ${prop.policy_blocked ? "danger" : "warn"}`} style={{ marginBottom: 10 }}>
                      <div>
                        {prop.policy_warnings.map((w, idx) => (
                          <div key={idx}>{w}</div>
                        ))}
                      </div>
                    </div>
                  )}

                  {prop.requires_substitution_approval && !prop.policy_blocked && (
                    <label className="checkbox-row">
                      <input
                        type="checkbox"
                        checked={subChecked}
                        onChange={(e) =>
                          setAcceptedSubs((prev) => ({
                            ...prev,
                            [prop.proposal_id]: e.target.checked,
                          }))
                        }
                      />
                      <span>
                        <strong>Explicit substitution confirmation required:</strong>{" "}
                        {prop.substitution_summary || "Quoted SKU or pack size differs from rule constraint."} I explicitly approve this change for this occurrence only.
                      </span>
                    </label>
                  )}

                  <div className="actions">
                    {!prop.reminder_only ? (
                      <button
                        className="primary"
                        disabled={
                          busy ||
                          prop.policy_blocked ||
                          (prop.requires_substitution_approval && !subChecked)
                        }
                        onClick={() =>
                          runAction(async () => {
                            const res = await api.approveProposal(prop.proposal_id, {
                              expected_quote_digest: prop.quote_digest,
                              expected_total_minor: prop.delivered_total.amount_minor,
                              accept_substitution: subChecked,
                            });
                            if (res.occurrence.status === "needs_reconciliation") {
                              setNotice({
                                tone: "warn",
                                text: "Checkout timed out with unknown outcome. Budget is held pending reconciliation — no second charge was attempted.",
                              });
                            }
                          }, "Proposal approved and submitted through checkout.")
                        }
                      >
                        Approve Exact Order ({formatMoney(prop.delivered_total)})
                      </button>
                    ) : (
                      <span className="pill brand">
                        Reminder only — edit rule to C2 Assisted Reorders to enable payment
                      </span>
                    )}
                    <button
                      disabled={busy}
                      onClick={() =>
                        runAction(
                          () => api.rejectProposal(prop.proposal_id, "Declined in review").then(() => {}),
                          "Proposal declined without purchasing.",
                        )
                      }
                    >
                      Decline This Occurrence
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>
      )}

      {/* TAB 2: REORDER RULES */}
      {tab === "rules" && (
        <div className="grid-2">
          <div className="card">
            <div className="card-header">
              <div>
                <h2 className="card-title">
                  {editingRuleId ? "Edit Rule (Creates New Version)" : "New Recurring Reorder Rule"}
                </h2>
                <p className="muted">
                  Exact SKU &amp; pack-size matching with timezone-aware calendar or minimum-interval schedules.
                </p>
              </div>
              {editingRuleId && (
                <button className="small" onClick={() => setEditingRuleId(null)}>
                  Cancel Edit
                </button>
              )}
            </div>

            <form onSubmit={handleSaveRule} className="form-grid">
              <div className="field full">
                <label>Product (Exact SKU &amp; Pack Size)</label>
                <select value={sku} onChange={(e) => handleSelectSKU(e.target.value)}>
                  {catalog.map((item) => (
                    <option key={item.sku} value={item.sku}>
                      [{item.category.toUpperCase()}] {item.title} — {item.pack_size_value}{" "}
                      {item.pack_size_unit} ({formatMoney(item.default_unit_price)})
                    </option>
                  ))}
                </select>
              </div>

              <div className="field full">
                <label>Rule Label</label>
                <input value={ruleName} onChange={(e) => setRuleName(e.target.value)} required />
              </div>

              <div className="field">
                <label>Preferred Merchant</label>
                <select value={merchantId} onChange={(e) => setMerchantId(e.target.value)}>
                  {(selectedCatalogItem?.merchants ?? []).map((m) => (
                    <option key={m.merchant_id} value={m.merchant_id}>
                      {m.merchant_name} ({m.protocol} · {formatMoney(m.unit_price)} +{" "}
                      {formatMoney(m.shipping_fee)} ship)
                    </option>
                  ))}
                </select>
              </div>

              <div className="field">
                <label>Quantity</label>
                <input
                  type="number"
                  min={1}
                  max={20}
                  value={quantity}
                  onChange={(e) => setQuantity(Number(e.target.value))}
                />
              </div>

              <div className="field">
                <label>Max Delivered Price (USD)</label>
                <input
                  value={maxPriceStr}
                  onChange={(e) => setMaxPriceStr(e.target.value)}
                  placeholder="30.00"
                />
              </div>

              <div className="field">
                <label>Monthly Budget Cap (USD)</label>
                <input
                  value={monthlyBudgetStr}
                  onChange={(e) => setMonthlyBudgetStr(e.target.value)}
                  placeholder="75.00"
                />
              </div>

              <div className="field">
                <label>Schedule Semantics</label>
                <select
                  value={scheduleKind}
                  onChange={(e) => setScheduleKind(e.target.value as ScheduleKind)}
                >
                  <option value="interval">Minimum Elapsed Interval (every N days)</option>
                  <option value="calendar">Fixed Calendar Schedule (weekly / monthly)</option>
                </select>
              </div>

              {scheduleKind === "interval" ? (
                <div className="field">
                  <label>Interval (Days)</label>
                  <input
                    type="number"
                    min={1}
                    max={365}
                    value={intervalDays}
                    onChange={(e) => setIntervalDays(Number(e.target.value))}
                  />
                </div>
              ) : (
                <div className="field">
                  <label>Calendar Cadence</label>
                  <select
                    value={calendarCadence}
                    onChange={(e) => setCalendarCadence(e.target.value as CalendarCadence)}
                  >
                    <option value="monthly">Monthly (Day of Month)</option>
                    <option value="weekly">Weekly (Day of Week)</option>
                  </select>
                </div>
              )}

              {scheduleKind === "calendar" && calendarCadence === "monthly" && (
                <div className="field">
                  <label>Day of Month (1–31)</label>
                  <input
                    type="number"
                    min={1}
                    max={31}
                    value={dayOfMonth}
                    onChange={(e) => setDayOfMonth(Number(e.target.value))}
                  />
                </div>
              )}

              {scheduleKind === "calendar" && calendarCadence === "weekly" && (
                <div className="field">
                  <label>Weekday</label>
                  <select value={dayOfWeek} onChange={(e) => setDayOfWeek(Number(e.target.value))}>
                    <option value={0}>Sunday</option>
                    <option value={1}>Monday</option>
                    <option value={2}>Tuesday</option>
                    <option value={3}>Wednesday</option>
                    <option value={4}>Thursday</option>
                    <option value={5}>Friday</option>
                    <option value={6}>Saturday</option>
                  </select>
                </div>
              )}

              <div className="field">
                <label>IANA Timezone</label>
                <select value={timezone} onChange={(e) => setTimezone(e.target.value)}>
                  {TIMEZONES.map((tz) => (
                    <option key={tz} value={tz}>
                      {tz}
                    </option>
                  ))}
                </select>
              </div>

              <div className="field">
                <label>Local Run Time (HH:MM)</label>
                <div style={{ display: "flex", gap: 6 }}>
                  <input
                    type="number"
                    min={0}
                    max={23}
                    value={localHour}
                    onChange={(e) => setLocalHour(Number(e.target.value))}
                  />
                  <input
                    type="number"
                    min={0}
                    max={59}
                    value={localMinute}
                    onChange={(e) => setLocalMinute(Number(e.target.value))}
                  />
                </div>
              </div>

              <div className="field">
                <label>Daylight-Saving Policy</label>
                <select value={dstPolicy} onChange={(e) => setDstPolicy(e.target.value as DSTPolicy)}>
                  <option value="shift_forward_first">Gap → post-gap · Fall-back → 1st hour</option>
                  <option value="shift_forward_second">Gap → post-gap · Fall-back → 2nd hour</option>
                </select>
              </div>

              <div className="field">
                <label>Leap Day / Short Month Policy</label>
                <select
                  value={leapPolicy}
                  onChange={(e) => setLeapPolicy(e.target.value as LeapDayPolicy)}
                >
                  <option value="clamp_month_end">Clamp to month end (Feb 28)</option>
                  <option value="carry_march_first">Carry Feb 29 → March 1</option>
                </select>
              </div>

              <div className="field">
                <label>Min Days Between Orders</label>
                <input
                  type="number"
                  min={0}
                  max={365}
                  value={minIntervalDays}
                  onChange={(e) => setMinIntervalDays(Number(e.target.value))}
                />
              </div>

              <div className="field">
                <label>Proposal Expiry (Minutes)</label>
                <input
                  type="number"
                  min={5}
                  max={10080}
                  value={proposalTTLMinutes}
                  onChange={(e) => setProposalTTLMinutes(Number(e.target.value))}
                />
              </div>

              <div className="field">
                <label>Substitution Policy</label>
                <select
                  value={substitutionPolicy}
                  onChange={(e) => setSubstitutionPolicy(e.target.value as SubstitutionPolicy)}
                >
                  <option value="approval_required">Require approval on substitution / pack change</option>
                  <option value="none">No substitutions permitted (strict exact match)</option>
                  <option value="preauthorized_exact">Allow preauthorized exact substitute SKU</option>
                </select>
              </div>

              {substitutionPolicy === "preauthorized_exact" && (
                <div className="field">
                  <label>Preauthorized Exact Substitute SKU</label>
                  <select value={preauthSubSKU} onChange={(e) => setPreauthSubSKU(e.target.value)}>
                    {catalog
                      .filter((c) => c.sku !== sku)
                      .map((c) => (
                        <option key={c.sku} value={c.sku}>
                          {c.title} ({c.pack_size_value} {c.pack_size_unit})
                        </option>
                      ))}
                  </select>
                </div>
              )}

              <div className="field">
                <label>Scoped Payment Method</label>
                <select value={paymentMethodId} onChange={(e) => setPaymentMethodId(e.target.value)}>
                  {paymentMethods.map((pm) => (
                    <option key={pm.id} value={pm.id}>
                      {pm.label} {pm.supports_delegation ? "(C2/C3)" : "(C2 only)"}
                    </option>
                  ))}
                </select>
              </div>

              <div className="field">
                <label>Approval Mode (Milestone)</label>
                <select
                  value={approvalMode}
                  onChange={(e) => setApprovalMode(e.target.value as ApprovalMode)}
                >
                  <option value="C1_REMINDER_ONLY">C1 — Reminders Only (no automatic charge)</option>
                  <option value="C2_ASSISTED_REORDER">C2 — Assisted Reorder (approve every order)</option>
                  <option value="C3_LIMITED_DELEGATION">
                    C3 — Limited Delegation (requires pilot flag + mandate)
                  </option>
                </select>
              </div>

              <div className="field full">
                <button className="primary" type="submit" disabled={busy}>
                  {editingRuleId ? "Save New Rule Version" : "Create Recurring Rule"}
                </button>
              </div>
            </form>
          </div>

          <div>
            {(workspace?.rules ?? []).length === 0 ? (
              <div className="empty-state">
                No rules yet. Configure your first recurring essential on the left.
              </div>
            ) : (
              (workspace?.rules ?? []).map((rule) => {
                const c = rule.current;
                const b = workspace?.budget_usage[rule.rule_id];
                const pct = budgetPercent(b?.used, b?.cap);
                const activeMandates = (workspace?.consents ?? []).filter(
                  (cn) =>
                    cn.rule_id === rule.rule_id &&
                    cn.kind === "delegated_mandate" &&
                    !cn.revoked_at,
                );
                return (
                  <div key={rule.rule_id} className="card">
                    <div className="card-header">
                      <div>
                        <div className="pill-row" style={{ marginBottom: 6 }}>
                          <span className={`pill ${statusTone(c.status)}`}>{c.status}</span>
                          <span className="pill">v{rule.current_version}</span>
                          <span className="pill">{c.approval_mode}</span>
                        </div>
                        <h3 className="card-title">{c.name}</h3>
                        <p className="muted">
                          {c.product.quantity} × {c.product.sku} ({c.product.pack_size_value}{" "}
                          {c.product.pack_size_unit}) · Max {formatMoney(c.product.max_delivered_price)}
                        </p>
                      </div>
                    </div>

                    <div className="muted" style={{ marginBottom: 8 }}>
                      <div>{describeSchedule(c.schedule)}</div>
                      <div>
                        Next run: <strong>{formatTimeShort(c.next_run_at_utc)}</strong> · Sub policy:{" "}
                        <strong>{c.product.substitution_policy}</strong>
                      </div>
                      {c.pause_reason && (
                        <div style={{ color: "var(--warn-ink)" }}>
                          Paused reason: {c.pause_reason} (blocks future runs only; already-submitted orders are tracked separately)
                        </div>
                      )}
                    </div>

                    {b && (
                      <div style={{ marginBottom: 10 }}>
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            fontSize: 12,
                          }}
                        >
                          <span className="muted">Monthly Budget ({b.period})</span>
                          <strong>
                            {formatMoney(b.used)} / {formatMoney(b.cap)}
                          </strong>
                        </div>
                        <div className="budget-bar">
                          <div style={{ width: `${pct}%` }} />
                        </div>
                      </div>
                    )}

                    {c.approval_mode === "C3_LIMITED_DELEGATION" && (
                      <div className="banner warn" style={{ padding: "8px 10px", marginBottom: 10 }}>
                        <div style={{ fontSize: 12.5 }}>
                          <strong>C3 Mandates:</strong> {activeMandates.length} active
                          {activeMandates.map((m) => (
                            <span key={m.consent_id} style={{ marginLeft: 8 }}>
                              <span className="mono">{m.provider_mandate_ref}</span>{" "}
                              <button
                                className="small danger"
                                disabled={busy}
                                onClick={() =>
                                  runAction(
                                    () => api.revokeConsent(m.consent_id).then(() => {}),
                                    "Revoked delegated mandate immediately.",
                                  )
                                }
                              >
                                Revoke
                              </button>
                            </span>
                          ))}
                        </div>
                        {activeMandates.length === 0 && (
                          <button
                            className="small primary"
                            disabled={busy}
                            onClick={() =>
                              runAction(
                                () =>
                                  api
                                    .grantMandate(rule.rule_id, {
                                      max_amount: c.product.max_delivered_price,
                                      payment_method_id: c.product.payment_method_id,
                                      provider_mandate_ref: `ap2_mandate_v${rule.current_version}`,
                                      ttl_hours: 720,
                                    })
                                    .then(() => {}),
                                "Granted scoped C3 delegated mandate bound to current rule version.",
                              )
                            }
                          >
                            Grant Mandate
                          </button>
                        )}
                      </div>
                    )}

                    <div className="actions">
                      {c.status === "active" && (
                        <>
                          <button
                            className="small"
                            disabled={busy}
                            onClick={() =>
                              runAction(
                                () => api.skipNext(rule.rule_id).then(() => {}),
                                "Skipped next scheduled occurrence and advanced schedule.",
                              )
                            }
                          >
                            Skip Next
                          </button>
                          <button
                            className="small"
                            disabled={busy}
                            onClick={() =>
                              runAction(async () => {
                                const res = await api.pauseRule(rule.rule_id, "Paused by customer");
                                if (res.already_submitted_orders.length > 0) {
                                  setNotice({
                                    tone: "warn",
                                    text: `Rule paused (future orders stopped). ${res.already_submitted_orders.length} already-submitted order(s) remain active and can be canceled individually in Orders.`,
                                  });
                                }
                              }, "Rule paused. Future scheduled occurrences are blocked.")
                            }
                          >
                            Pause Rule
                          </button>
                        </>
                      )}
                      {c.status === "paused" && (
                        <button
                          className="small primary"
                          disabled={busy}
                          onClick={() =>
                            runAction(
                              () => api.resumeRule(rule.rule_id).then(() => {}),
                              "Rule resumed with next run scheduled strictly in the future (no catch-up backlog).",
                            )
                          }
                        >
                          Resume Rule
                        </button>
                      )}
                      {c.status !== "canceled" && (
                        <>
                          <button className="small" onClick={() => loadRuleIntoEditor(rule)}>
                            Edit (New Version)
                          </button>
                          <button
                            className="small danger"
                            disabled={busy}
                            onClick={() =>
                              runAction(
                                () => api.cancelRule(rule.rule_id).then(() => {}),
                                "Rule permanently canceled.",
                              )
                            }
                          >
                            Cancel Rule
                          </button>
                        </>
                      )}
                    </div>
                  </div>
                );
              })
            )}
          </div>
        </div>
      )}

      {/* TAB 3: ORDERS & OCCURRENCES */}
      {tab === "orders" && (
        <div className="card">
          <div className="card-header">
            <div>
              <h2 className="card-title">Schedule Occurrences, Payments &amp; Fulfillment</h2>
              <p className="muted">
                Payment settlement and merchant fulfillment are tracked independently. Outstanding or delayed orders block duplicate reorders.
              </p>
            </div>
          </div>

          {(workspace?.occurrences ?? []).length === 0 ? (
            <div className="empty-state">No occurrences recorded yet.</div>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Slot &amp; Rule</th>
                    <th>Schedule State</th>
                    <th>Payment</th>
                    <th>Fulfillment</th>
                    <th>Budget &amp; Amount</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {(workspace?.occurrences ?? []).map((d: OccurrenceDetail) => {
                    const o = d.occurrence;
                    const chk = d.checkout;
                    return (
                      <tr key={o.occurrence_id}>
                        <td>
                          <strong>{d.proposal?.title ?? o.rule_id}</strong>
                          <div className="muted">{o.scheduled_local_display}</div>
                          <div className="mono muted">
                            v{o.rule_version} · {o.occurrence_id}
                          </div>
                          {o.pause_raced_submission && (
                            <span className="pill warn">Pause raced in-flight order</span>
                          )}
                          {o.status_reason && (
                            <div className="muted" style={{ marginTop: 4, maxWidth: 280 }}>
                              {o.status_reason}
                            </div>
                          )}
                        </td>
                        <td>
                          <span className={`pill ${statusTone(o.status)}`}>{o.status}</span>
                        </td>
                        <td>
                          <span className={`pill ${statusTone(o.payment_status)}`}>
                            {o.payment_status}
                          </span>
                          {chk?.merchant_order_id && (
                            <div className="mono muted" style={{ marginTop: 4 }}>
                              {chk.merchant_order_id}
                            </div>
                          )}
                        </td>
                        <td>
                          <span className={`pill ${statusTone(o.fulfillment_status)}`}>
                            {o.fulfillment_status}
                          </span>
                        </td>
                        <td>
                          <div>
                            <strong>
                              {d.proposal ? formatMoney(d.proposal.delivered_total) : "—"}
                            </strong>
                          </div>
                          {d.reservation && (
                            <div className="muted" style={{ fontSize: 12 }}>
                              Res: {d.reservation.status} ({d.reservation.budget_period})
                            </div>
                          )}
                          {chk && BigInt(chk.refunded_amount.amount_minor) > 0n && (
                            <div className="muted" style={{ fontSize: 12 }}>
                              Refunded: {formatMoney(chk.refunded_amount)}
                            </div>
                          )}
                        </td>
                        <td>
                          <div className="pill-row">
                            {o.status === "needs_reconciliation" && (
                              <button
                                className="small primary"
                                disabled={busy}
                                onClick={() =>
                                  runAction(
                                    () => api.reconcileOccurrence(o.occurrence_id).then(() => {}),
                                    "Reconciled payment with provider.",
                                  )
                                }
                              >
                                Reconcile
                              </button>
                            )}
                            {chk?.merchant_order_id &&
                              o.fulfillment_status !== "delivered" &&
                              o.fulfillment_status !== "canceled" && (
                                <>
                                  <button
                                    className="small danger"
                                    disabled={busy}
                                    onClick={() =>
                                      runAction(
                                        () => api.cancelSubmittedOrder(o.occurrence_id).then(() => {}),
                                        "Requested merchant order cancellation.",
                                      )
                                    }
                                  >
                                    Cancel Order
                                  </button>
                                  {workspace?.demo_mode && (
                                    <>
                                      <button
                                        className="small"
                                        disabled={busy}
                                        onClick={() =>
                                          runAction(
                                            () =>
                                              api
                                                .sandboxFulfill(o.occurrence_id, {
                                                  fulfillment_status: "delivered",
                                                  note: "Carrier confirmed porch delivery.",
                                                })
                                                .then(() => {}),
                                            "Marked order delivered (unblocks next cycle).",
                                          )
                                        }
                                      >
                                        Simulate Delivered
                                      </button>
                                      <button
                                        className="small"
                                        disabled={busy}
                                        onClick={() =>
                                          runAction(
                                            () =>
                                              api
                                                .sandboxFulfill(o.occurrence_id, {
                                                  fulfillment_status: "shipped",
                                                  partial_refund: {
                                                    amount_minor: "499",
                                                    asset: "USD",
                                                    network: "fiat",
                                                    decimals: 2,
                                                  },
                                                  note: "Merchant refunded $4.99 shipping fee.",
                                                })
                                                .then(() => {}),
                                            "Applied partial shipping refund ($4.99).",
                                          )
                                        }
                                      >
                                        Partial Refund
                                      </button>
                                    </>
                                  )}
                                </>
                              )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* TAB 4: OPERATIONS & SANDBOX LAB */}
      {tab === "ops" && workspace && (
        <div className="grid-2">
          <div>
            <div className="card">
              <h2 className="card-title" style={{ marginBottom: 8 }}>
                Sandbox Clock &amp; Recovery Scan Controls
              </h2>
              <p className="muted" style={{ marginBottom: 12 }}>
                Advance the deterministic clock to test proposal expiry, monthly budget rollover, and multi-slot outage coalescing.
              </p>
              <div className="actions">
                <button
                  className="small"
                  disabled={busy}
                  onClick={() =>
                    runAction(() => api.sandboxTick(180).then(() => {}), "Advanced +3 hours.")
                  }
                >
                  +3 Hours
                </button>
                <button
                  className="small"
                  disabled={busy}
                  onClick={() =>
                    runAction(() => api.sandboxTick(24 * 60).then(() => {}), "Advanced +1 day.")
                  }
                >
                  +1 Day
                </button>
                <button
                  className="small"
                  disabled={busy}
                  onClick={() =>
                    runAction(
                      () => api.sandboxTick(14 * 24 * 60).then(() => {}),
                      "Advanced +14 days.",
                    )
                  }
                >
                  +14 Days
                </button>
                <button
                  className="small primary"
                  disabled={busy}
                  onClick={() =>
                    runAction(
                      () => api.sandboxTick(35 * 24 * 60).then(() => {}),
                      "Simulated 35-day outage: missed slots coalesced into at most 1 recovery proposal.",
                    )
                  }
                >
                  Simulate 35-Day Outage
                </button>
              </div>
            </div>

            <div className="card">
              <h2 className="card-title" style={{ marginBottom: 8 }}>
                Merchant &amp; Payment Scenario Injection
              </h2>
              <div className="form-grid">
                <div className="field">
                  <label>Merchant Price Multiplier</label>
                  <select
                    value={workspace.faults.price_multiplier_bps}
                    onChange={(e) => updateFault({ price_multiplier_bps: Number(e.target.value) })}
                  >
                    <option value={10000}>Normal (1.00×)</option>
                    <option value={11500}>+15% Moderate Increase</option>
                    <option value={14500}>+45% Price Spike (triggers cap block)</option>
                  </select>
                </div>

                <div className="field">
                  <label>Stock &amp; Shrinkflation</label>
                  <select
                    value={
                      workspace.faults.force_pack_size_value
                        ? "pack_shrink"
                        : workspace.faults.force_stock_status || "in_stock"
                    }
                    onChange={(e) => {
                      const v = e.target.value;
                      if (v === "pack_shrink") {
                        updateFault({ force_pack_size_value: 10, force_stock_status: "" });
                      } else if (v === "in_stock") {
                        updateFault({ force_pack_size_value: 0, force_stock_status: "" });
                      } else {
                        updateFault({ force_pack_size_value: 0, force_stock_status: v });
                      }
                    }}
                  >
                    <option value="in_stock">In Stock (Exact Pack)</option>
                    <option value="pack_shrink">Pack Size Shrunk to 10</option>
                    <option value="partial_stock">Partial Stock</option>
                    <option value="out_of_stock">Out of Stock</option>
                  </select>
                </div>

                <div className="field">
                  <label>Force Substitute SKU</label>
                  <select
                    value={workspace.faults.force_substitute_sku || ""}
                    onChange={(e) => updateFault({ force_substitute_sku: e.target.value })}
                  >
                    <option value="">None (Exact SKU)</option>
                    {catalog.map((c) => (
                      <option key={c.sku} value={c.sku}>
                        Substitute with {c.sku}
                      </option>
                    ))}
                  </select>
                </div>

                <div className="field">
                  <label>Payment &amp; Fulfillment Outcome</label>
                  <select
                    value={workspace.faults.force_payment_behavior || ""}
                    onChange={(e) => updateFault({ force_payment_behavior: e.target.value })}
                  >
                    <option value="">Normal Settlement</option>
                    <option value="timeout_settled">Timeout (Actually Settled → Reconcile)</option>
                    <option value="timeout_failed">Timeout (Failed → Release Budget)</option>
                    <option value="delayed_fulfillment">Delayed Fulfillment (Blocks Next Cycle)</option>
                    <option value="decline">Card Declined</option>
                  </select>
                </div>

                <div className="field full">
                  <label>C3 Limited Delegation Pilot Gate</label>
                  <button
                    type="button"
                    className="small"
                    disabled={busy}
                    onClick={() =>
                      updateFault({ enable_c3_delegation: !workspace.faults.enable_c3_delegation })
                    }
                  >
                    {workspace.faults.enable_c3_delegation
                      ? "C3 Delegation Enabled (Click to Disable)"
                      : "Enable C3 Delegation Pilot Gate"}
                  </button>
                </div>
              </div>
            </div>

            <div className="card">
              <h2 className="card-title" style={{ marginBottom: 8 }}>
                Operator RBAC &amp; Product-Level Pause
              </h2>
              {workspace.role !== "operator" ? (
                <div className="form-grid">
                  <div className="field">
                    <label>Operator Token (local default: local-operator)</label>
                    <input value={opToken} onChange={(e) => setOpToken(e.target.value)} />
                  </div>
                  <div className="field" style={{ justifyContent: "flex-end" }}>
                    <button
                      className="primary"
                      disabled={busy}
                      onClick={() =>
                        runAction(
                          () => api.openSession(workspace.tenant, "operator", opToken).then(() => {}),
                          "Elevated session to Operator role.",
                        )
                      }
                    >
                      Elevate to Operator
                    </button>
                  </div>
                </div>
              ) : (
                <div>
                  <div className="field" style={{ marginBottom: 10 }}>
                    <label>Pause Reason</label>
                    <input
                      value={globalPauseReason}
                      onChange={(e) => setGlobalPauseReason(e.target.value)}
                    />
                  </div>
                  <div className="actions">
                    <button
                      className={workspace.global_pause.paused ? "primary" : "danger"}
                      disabled={busy}
                      onClick={() =>
                        runAction(
                          () =>
                            api
                              .setOperatorPause(!workspace.global_pause.paused, globalPauseReason)
                              .then(() => {}),
                          workspace.global_pause.paused
                            ? "Lifted product-level pause."
                            : "Activated product-level emergency pause.",
                        )
                      }
                    >
                      {workspace.global_pause.paused
                        ? "Lift Global Product Pause"
                        : "Activate Global Product Pause"}
                    </button>
                    <button
                      className="small"
                      disabled={busy}
                      onClick={() =>
                        runAction(
                          () => api.openSession(workspace.tenant, "customer").then(() => {}),
                          "Switched back to Customer role.",
                        )
                      }
                    >
                      Switch to Customer Role
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>

          <div>
            <div className="card">
              <h2 className="card-title" style={{ marginBottom: 8 }}>
                Notification Outbox ({workspace.notifications.length})
              </h2>
              {workspace.notifications.length === 0 ? (
                <div className="muted">No notifications emitted yet.</div>
              ) : (
                [...workspace.notifications].reverse().slice(0, 12).map((n) => (
                  <div
                    key={n.event_id}
                    style={{
                      padding: "8px 0",
                      borderBottom: "1px solid var(--border)",
                      fontSize: 12.5,
                    }}
                  >
                    <div className="pill-row" style={{ marginBottom: 3 }}>
                      <span className="pill">{n.kind}</span>
                      <span className={`pill ${statusTone(n.status)}`}>{n.status}</span>
                      <span className="muted">{formatTimeShort(n.created_at)}</span>
                    </div>
                    <div>{n.message}</div>
                  </div>
                ))
              )}
            </div>

            <div className="card">
              <h2 className="card-title" style={{ marginBottom: 8 }}>
                Immutable Audit Trail ({workspace.audits.length})
              </h2>
              {workspace.audits.length === 0 ? (
                <div className="muted">No audit events recorded yet.</div>
              ) : (
                [...workspace.audits].reverse().slice(0, 15).map((a) => (
                  <div
                    key={a.audit_id}
                    style={{
                      padding: "7px 0",
                      borderBottom: "1px solid var(--border)",
                      fontSize: 12,
                    }}
                  >
                    <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
                      <strong>{a.action}</strong>
                      <span className="muted">{formatTimeShort(a.created_at)}</span>
                    </div>
                    <div className="mono muted">
                      {a.actor} · {a.resource_type}/{a.resource_id}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
