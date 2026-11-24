import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  Building2,
  CheckCircle2,
  Clock,
  Coins,
  PauseCircle,
  PlayCircle,
  RefreshCw,
  ShieldCheck,
  Wallet,
} from "lucide-react";
import {
  api,
  type BeneficiaryVersion,
  type CampaignView,
  type CustomerRewardsSummary,
  type JournalEntry,
  type LedgerSummary,
  type Merchant,
  type PayoutAttempt,
  type PayoutQuote,
  type ReconciliationException,
  type SystemStatus,
} from "./api";
import {
  formatDecimal,
  formatUSD,
  formatUSDC,
  parseBaseUnits,
  parseDecimalToBaseUnits,
  ratioPercent,
} from "./money";

type Tab = "customer" | "merchant" | "operator";

function statusPillClass(status: string): string {
  switch (status) {
    case "eligible":
    case "succeeded":
    case "paid":
    case "active":
    case "verified":
    case "resolved":
      return "pill pill-green";
    case "pending":
    case "submitted":
    case "prepared":
    case "payout_requested":
    case "warning":
      return "pill pill-amber";
    case "failed":
    case "outcome_unknown":
    case "critical":
    case "paused":
    case "revoked":
      return "pill pill-red";
    default:
      return "pill pill-muted";
  }
}

export default function App() {
  const [tab, setTab] = useState<Tab>("customer");
  const [customerId, setCustomerId] = useState("cust_alex");
  const [operatorToken, setOperatorToken] = useState("local-operator");

  const [sysStatus, setSysStatus] = useState<SystemStatus | null>(null);
  const [merchants, setMerchants] = useState<Merchant[]>([]);
  const [campaigns, setCampaigns] = useState<CampaignView[]>([]);
  const [rewards, setRewards] = useState<CustomerRewardsSummary | null>(null);
  const [beneficiaries, setBeneficiaries] = useState<BeneficiaryVersion[]>([]);
  const [payouts, setPayouts] = useState<PayoutAttempt[]>([]);
  const [exceptions, setExceptions] = useState<ReconciliationException[]>([]);
  const [ledger, setLedger] = useState<LedgerSummary | null>(null);
  const [journals, setJournals] = useState<JournalEntry[]>([]);

  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Customer beneficiary form
  const [benAddress, setBenAddress] = useState("0x71C7656EC7ab88b098defB751B7401B5f6d8976F");
  const [benRecoveryAck, setBenRecoveryAck] = useState(true);

  // Customer quote & payout state
  const [activeQuote, setActiveQuote] = useState<PayoutQuote | null>(null);
  const [termsApproved, setTermsApproved] = useState(false);
  const [payoutScenario, setPayoutScenario] = useState("");

  // Merchant campaign form
  const [cmpMerchant, setCmpMerchant] = useState("mer_roast");
  const [cmpName, setCmpName] = useState("Holiday 6% Coffee Cashback");
  const [cmpType, setCmpType] = useState<"percentage" | "fixed">("percentage");
  const [cmpRateBps, setCmpRateBps] = useState("600");
  const [cmpFixedUSD, setCmpFixedUSD] = useState("10.00");
  const [cmpBudgetUSD, setCmpBudgetUSD] = useState("1000.00");
  const [cmpCustCapUSD, setCmpCustCapUSD] = useState("50.00");
  const [cmpRetDays, setCmpRetDays] = useState("7");
  const [cmpFeeBps, setCmpFeeBps] = useState("500");
  const [cmpLossBps, setCmpLossBps] = useState("200");

  // Merchant event simulator form
  const [evtType, setEvtType] = useState("order.paid");
  const [evtMerchant, setEvtMerchant] = useState("mer_roast");
  const [evtCampaign, setEvtCampaign] = useState("cmp_roast_spring");
  const [evtOrderId, setEvtOrderId] = useState("ORD-9100");
  const [evtCustomer, setEvtCustomer] = useState("cust_alex");
  const [evtSubtotalUSD, setEvtSubtotalUSD] = useState("120.00");
  const [evtFulfilled, setEvtFulfilled] = useState(true);

  // Operator controls
  const [pauseReason, setPauseReason] = useState("Scheduled maintenance check");
  const [resolveNotes, setResolveNotes] = useState<Record<string, string>>({});

  const refreshAll = useCallback(async () => {
    try {
      const [statusRes, merchRes, campRes, rewRes, benRes, payRes] = await Promise.all([
        api.getSystemStatus(),
        api.listMerchants(),
        api.listCampaigns(),
        api.getCustomerRewards(customerId),
        api.listBeneficiaries(customerId),
        api.listPayouts(customerId),
      ]);
      setSysStatus(statusRes);
      setMerchants(merchRes.merchants);
      setCampaigns(campRes.campaigns);
      setRewards(rewRes);
      setBeneficiaries(benRes.beneficiaries);
      setPayouts(payRes.payouts);

      if (operatorToken.trim()) {
        try {
          const [excRes, ledRes, jouRes] = await Promise.all([
            api.listExceptions(operatorToken),
            api.getLedgerSummary(operatorToken),
            api.listLedgerEntries(operatorToken),
          ]);
          setExceptions(excRes.exceptions);
          setLedger(ledRes);
          setJournals(jouRes.entries);
        } catch {
          // Operator token may not be valid; leave operator panes unloaded
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [customerId, operatorToken]);

  useEffect(() => {
    void refreshAll();
  }, [refreshAll]);

  async function runAction(label: string, fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await fn();
      setNotice(label);
      await refreshAll();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const openExceptions = exceptions.filter((e) => e.status !== "resolved");

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-icon">
            <Coins size={20} />
          </div>
          <div>
            <h1>Merchant-Funded Stablecoin Rewards</h1>
            <p>
              Double-entry USD ledger · Prefunded campaign caps · Base USDC payouts (Sandbox)
            </p>
          </div>
        </div>

        <nav className="nav-tabs" aria-label="Workspace roles">
          <button
            className={`nav-tab ${tab === "customer" ? "active" : ""}`}
            onClick={() => setTab("customer")}
          >
            <Wallet size={15} /> Customer Rewards
          </button>
          <button
            className={`nav-tab ${tab === "merchant" ? "active" : ""}`}
            onClick={() => setTab("merchant")}
          >
            <Building2 size={15} /> Merchant Campaigns
          </button>
          <button
            className={`nav-tab ${tab === "operator" ? "active" : ""}`}
            onClick={() => setTab("operator")}
          >
            <ShieldCheck size={15} /> Operator Queue
            {openExceptions.length > 0 && (
              <span className="badge-count">{openExceptions.length}</span>
            )}
          </button>
        </nav>

        <button className="btn btn-sm" disabled={busy} onClick={() => void refreshAll()}>
          <RefreshCw size={14} /> Refresh
        </button>
      </header>

      {sysStatus && (sysStatus.pause.issuance_paused || sysStatus.pause.payouts_paused) && (
        <div className="banner banner-warn">
          <span>
            <strong>Product pause active:</strong>{" "}
            {sysStatus.pause.issuance_paused ? "Issuance PAUSED " : ""}
            {sysStatus.pause.payouts_paused ? "Payouts PAUSED " : ""}
            {sysStatus.pause.reason ? `— ${sysStatus.pause.reason}` : ""}
          </span>
        </div>
      )}

      {error && (
        <div className="banner banner-error">
          <span>{error}</span>
          <button className="btn btn-sm" onClick={() => setError(null)}>
            Dismiss
          </button>
        </div>
      )}
      {notice && (
        <div className="banner banner-ok">
          <span>{notice}</span>
          <button className="btn btn-sm" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </div>
      )}

      {tab === "customer" && (
        <section>
          <div className="card-header">
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <label className="field" style={{ flexDirection: "row", alignItems: "center" }}>
                <span>Customer:</span>
                <input
                  type="text"
                  value={customerId}
                  onChange={(e) => {
                    setCustomerId(e.target.value);
                    setActiveQuote(null);
                  }}
                  style={{ width: 150 }}
                />
              </label>
            </div>
            {rewards && (
              <span className="mono" style={{ color: "var(--muted)" }}>
                Rail: {rewards.payout_asset} on {rewards.payout_network} · Min payout:{" "}
                {formatUSD(rewards.min_payout_minor)} · Fee:{" "}
                {formatUSDC(rewards.payout_fee_usdc_base)}
              </span>
            )}
          </div>

          {rewards && (
            <div className="stats-grid">
              <div className="stat-card">
                <div className="stat-label">Eligible to Claim</div>
                <div className="stat-value" style={{ color: "var(--green)" }}>
                  {formatUSD(rewards.eligible_minor)}
                </div>
                <div className="stat-sub">Ready for stablecoin payout</div>
              </div>
              <div className="stat-card">
                <div className="stat-label">Pending Return Window</div>
                <div className="stat-value" style={{ color: "var(--amber)" }}>
                  {formatUSD(rewards.pending_minor)}
                </div>
                <div className="stat-sub">Awaiting fulfillment & return expiry</div>
              </div>
              <div className="stat-card">
                <div className="stat-label">Payout In-Flight</div>
                <div className="stat-value" style={{ color: "var(--accent)" }}>
                  {formatUSD(rewards.payout_requested_minor)}
                </div>
                <div className="stat-sub">Locked in payout clearing</div>
              </div>
              <div className="stat-card">
                <div className="stat-label">Total Paid</div>
                <div className="stat-value">{formatUSD(rewards.paid_minor)}</div>
                <div className="stat-sub">
                  Reversed prior to payout: {formatUSD(rewards.reversed_minor)}
                </div>
              </div>
            </div>
          )}

          <div className="grid-2">
            {/* Beneficiary Enrollment & Address Verification */}
            <div className="card">
              <div className="card-header">
                <h2>
                  <Wallet size={16} /> Beneficiary Enrollment & Address Verification
                </h2>
                {rewards?.active_beneficiary && (
                  <span className={statusPillClass(rewards.active_beneficiary.verification_status)}>
                    v{rewards.active_beneficiary.version} ·{" "}
                    {rewards.active_beneficiary.verification_status}
                  </span>
                )}
              </div>

              {rewards?.active_beneficiary ? (
                <div style={{ marginBottom: 12, fontSize: "0.86rem" }}>
                  <div>
                    Active Destination ({rewards.active_beneficiary.asset} on{" "}
                    {rewards.active_beneficiary.network}):
                  </div>
                  <div className="mono" style={{ color: "var(--accent)", wordBreak: "break-all" }}>
                    {rewards.active_beneficiary.address}
                  </div>
                  <div style={{ fontSize: "0.76rem", color: "var(--muted)", marginTop: 3 }}>
                    Version ID: <span className="mono">{rewards.active_beneficiary.id}</span> ·
                    Updating destination creates a new immutable version and invalidates prior quotes.
                  </div>
                </div>
              ) : (
                <p style={{ color: "var(--muted)", fontSize: "0.86rem" }}>
                  No verified payout destination enrolled yet.
                </p>
              )}

              <label className="field">
                Base Network EVM Wallet Address (USDC)
                <input
                  type="text"
                  value={benAddress}
                  onChange={(e) => setBenAddress(e.target.value)}
                  placeholder="0x..."
                />
              </label>

              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={benRecoveryAck}
                  onChange={(e) => setBenRecoveryAck(e.target.checked)}
                />
                <span>
                  I understand stablecoin transfers on Base are irreversible and wallet access /
                  recovery depends on my wallet or off-ramp provider.
                </span>
              </label>

              <div className="btn-row">
                <button
                  className="btn btn-primary"
                  disabled={busy}
                  onClick={() =>
                    void runAction("Enrolled & verified new beneficiary version", async () => {
                      await api.registerBeneficiary(customerId, {
                        customer_id: customerId,
                        asset: "USDC",
                        network: "base",
                        address: benAddress,
                        recovery_disclosed: benRecoveryAck,
                        auto_verify: true,
                      });
                      setActiveQuote(null);
                    })
                  }
                >
                  Save & Verify Destination
                </button>
              </div>

              {beneficiaries.length > 1 && (
                <div style={{ marginTop: 12, fontSize: "0.78rem", color: "var(--muted)" }}>
                  Prior versions:{" "}
                  {beneficiaries
                    .slice(1)
                    .map((b) => `v${b.version} (${b.verification_status})`)
                    .join(", ")}
                </div>
              )}
            </div>

            {/* Payout Quote & Authorization */}
            <div className="card">
              <div className="card-header">
                <h2>
                  <Coins size={16} /> Claim Eligible Balance (USDC on Base)
                </h2>
              </div>

              <p style={{ marginTop: 0, fontSize: "0.85rem", color: "var(--muted)" }}>
                Request a locked conversion quote showing exact USDC base units, fees, destination
                version, and expiry before authorizing an idempotent payout.
              </p>

              <div className="btn-row">
                <button
                  className="btn btn-primary"
                  disabled={
                    busy ||
                    !rewards ||
                    parseBaseUnits(rewards.eligible_minor) <
                      parseBaseUnits(rewards.min_payout_minor)
                  }
                  onClick={() =>
                    void runAction("Generated locked payout quote", async () => {
                      const q = await api.createPayoutQuote(customerId, {
                        customer_id: customerId,
                      });
                      setActiveQuote(q);
                      setTermsApproved(false);
                    })
                  }
                >
                  Get Payout Quote ({rewards ? formatUSD(rewards.eligible_minor) : "$0.00"})
                </button>
              </div>

              {activeQuote && (
                <div className="quote-box">
                  <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 6 }}>
                    <strong>Quote {activeQuote.id}</strong>
                    <span className="mono" style={{ color: "var(--amber)" }}>
                      Expires: {new Date(activeQuote.expires_at).toLocaleTimeString()}
                    </span>
                  </div>
                  <div className="table-wrap">
                    <table>
                      <tbody>
                        <tr>
                          <td>Eligible Cashback (USD)</td>
                          <td className="mono">{formatUSD(activeQuote.source_amount_base)}</td>
                        </tr>
                        <tr>
                          <td>Gross Conversion ({activeQuote.payout_asset})</td>
                          <td className="mono">{formatUSDC(activeQuote.gross_payout_base, true)}</td>
                        </tr>
                        <tr>
                          <td>Payout & Network Fee ({activeQuote.fee_bearer})</td>
                          <td className="mono">-{formatUSDC(activeQuote.fee_base, true)}</td>
                        </tr>
                        <tr>
                          <td>
                            <strong>Net Payout ({activeQuote.payout_network})</strong>
                          </td>
                          <td className="mono" style={{ color: "var(--green)", fontWeight: 700 }}>
                            {formatUSDC(activeQuote.net_payout_base, true)}
                          </td>
                        </tr>
                        <tr>
                          <td>Locked Beneficiary</td>
                          <td className="mono">
                            {activeQuote.beneficiary_version_id} ({activeQuote.destination_address.slice(0, 10)}...)
                          </td>
                        </tr>
                      </tbody>
                    </table>
                  </div>

                  <div className="disclosure">{activeQuote.disclosure}</div>

                  <label className="field" style={{ marginTop: 10 }}>
                    Sandbox Settlement Scenario
                    <select
                      value={payoutScenario}
                      onChange={(e) => setPayoutScenario(e.target.value)}
                    >
                      <option value="">Normal immediate settlement (12 confirmations)</option>
                      <option value="delayed">Delayed pending confirmation (worker settles)</option>
                      <option value="timeout_succeeded">
                        Provider timeout after broadcast (outcome_unknown → reconcile)
                      </option>
                      <option value="timeout_failed">
                        Provider timeout before broadcast (outcome_unknown → failed → retry)
                      </option>
                      <option value="reject">Provider compliance rejection (clean failure)</option>
                    </select>
                  </label>

                  <label className="checkbox-row">
                    <input
                      type="checkbox"
                      checked={termsApproved}
                      onChange={(e) => setTermsApproved(e.target.checked)}
                    />
                    <span>
                      I approve this one-time payout to {activeQuote.destination_address} bound to{" "}
                      {activeQuote.beneficiary_version_id}.
                    </span>
                  </label>

                  <div className="btn-row">
                    <button
                      className="btn btn-primary"
                      disabled={busy || !termsApproved}
                      onClick={() =>
                        void runAction("Submitted idempotent payout authorization", async () => {
                          await api.authorizePayout(
                            customerId,
                            `ui_pay_${activeQuote.id}_${Date.now()}`,
                            {
                              customer_id: customerId,
                              quote_id: activeQuote.id,
                              beneficiary_version_id: activeQuote.beneficiary_version_id,
                              terms_approved: true,
                              sandbox_scenario: payoutScenario,
                            },
                          );
                          setActiveQuote(null);
                          setTermsApproved(false);
                        })
                      }
                    >
                      Authorize Payout
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Customer Reward Statement */}
          <div className="card">
            <div className="card-header">
              <h2>Reward Statement (Linked to Source Orders & Fees)</h2>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Reward ID</th>
                    <th>Merchant / Campaign</th>
                    <th>Source Order</th>
                    <th>Payment / Fulfillment</th>
                    <th>Original</th>
                    <th>Reversed</th>
                    <th>Net Reward</th>
                    <th>State</th>
                    <th>Payout / Fee</th>
                  </tr>
                </thead>
                <tbody>
                  {rewards?.rewards.map((r) => (
                    <tr key={r.id}>
                      <td className="mono">{r.id}</td>
                      <td>
                        <div>{r.merchant_name}</div>
                        <div style={{ fontSize: "0.75rem", color: "var(--muted)" }}>
                          {r.campaign_name} (<span className="mono">{r.campaign_version_id}</span>)
                        </div>
                      </td>
                      <td className="mono">{r.order_id}</td>
                      <td>
                        {r.payment_settled ? "Paid" : "Unpaid"} ·{" "}
                        {r.fulfilled ? "Fulfilled" : "Awaiting delivery"}
                      </td>
                      <td className="mono">{formatUSD(r.original_amount_minor)}</td>
                      <td className="mono">{formatUSD(r.reversed_amount_minor)}</td>
                      <td className="mono" style={{ fontWeight: 700 }}>
                        {formatUSD(r.net_amount_minor)}
                      </td>
                      <td>
                        <span className={statusPillClass(r.state)}>
                          {r.state.replace("_", " ")}
                        </span>
                        {r.post_payout_disputed && (
                          <div style={{ fontSize: "0.72rem", color: "var(--red)", marginTop: 2 }}>
                            Post-payout dispute
                          </div>
                        )}
                      </td>
                      <td className="mono">
                        {r.payout_id ? (
                          <div>
                            {r.payout_id}
                            {r.payout_fee_base && parseBaseUnits(r.payout_fee_base) > 0n && (
                              <div style={{ fontSize: "0.72rem", color: "var(--muted)" }}>
                                Fee: {formatUSDC(r.payout_fee_base)}
                              </div>
                            )}
                          </div>
                        ) : (
                          "—"
                        )}
                      </td>
                    </tr>
                  ))}
                  {(!rewards || rewards.rewards.length === 0) && (
                    <tr>
                      <td colSpan={9} style={{ color: "var(--muted)", textAlign: "center" }}>
                        No rewards recorded for this customer yet.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          {/* Payout Attempts History */}
          <div className="card">
            <div className="card-header">
              <h2>Payout Attempts & Settlement Status</h2>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Logical Payout / Attempt</th>
                    <th>Destination</th>
                    <th>Net USDC</th>
                    <th>Fee</th>
                    <th>Status</th>
                    <th>Tx Hash / Reason</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {payouts.map((p) => (
                    <tr key={p.id}>
                      <td className="mono">
                        {p.logical_payout_id} #{p.attempt_number}
                      </td>
                      <td className="mono">
                        {p.beneficiary_version_id} ({p.destination_address.slice(0, 10)}...)
                      </td>
                      <td className="mono">{formatUSDC(p.net_payout_base, true)}</td>
                      <td className="mono">{formatUSDC(p.fee_base)}</td>
                      <td>
                        <span className={statusPillClass(p.status)}>
                          {p.status.replace("_", " ")}
                        </span>
                      </td>
                      <td className="mono" style={{ maxWidth: 260, wordBreak: "break-all" }}>
                        {p.tx_hash
                          ? `${p.tx_hash.slice(0, 18)}... (${p.confirmations} confs)`
                          : p.failure_reason || "—"}
                      </td>
                      <td>
                        {p.status === "failed" && (
                          <button
                            className="btn btn-sm btn-primary"
                            disabled={busy}
                            onClick={() =>
                              void runAction(`Retried logical payout ${p.logical_payout_id}`, async () => {
                                await api.retryPayout(
                                  customerId,
                                  p.logical_payout_id,
                                  `retry_${p.logical_payout_id}_${Date.now()}`,
                                );
                              })
                            }
                          >
                            Safe Retry
                          </button>
                        )}
                        {p.status === "outcome_unknown" && (
                          <span style={{ fontSize: "0.75rem", color: "var(--amber)" }}>
                            Reconcile in Operator tab before retry
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                  {payouts.length === 0 && (
                    <tr>
                      <td colSpan={7} style={{ color: "var(--muted)", textAlign: "center" }}>
                        No payouts submitted yet.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </section>
      )}

      {tab === "merchant" && (
        <section>
          <div className="card">
            <div className="card-header">
              <h2>
                <Building2 size={16} /> Active Pilot Merchants & Prefunded Campaigns
              </h2>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Campaign / Version</th>
                    <th>Rules & Caps</th>
                    <th>Prefunded vs Available</th>
                    <th>Reserved / Loss Res.</th>
                    <th>Status</th>
                    <th>Controls</th>
                  </tr>
                </thead>
                <tbody>
                  {campaigns.map((c) => {
                    const spent =
                      parseBaseUnits(c.funded_minor) - parseBaseUnits(c.available_minor);
                    const pct = ratioPercent(spent.toString(), c.budget_cap_minor);
                    return (
                      <tr key={c.id}>
                        <td>
                          <strong>{c.name}</strong>
                          <div className="mono" style={{ fontSize: "0.75rem", color: "var(--muted)" }}>
                            {c.merchant_name} · {c.campaign_id} (v{c.version})
                          </div>
                        </td>
                        <td style={{ fontSize: "0.8rem" }}>
                          <div>
                            {c.reward_type === "fixed"
                              ? `${formatUSD(c.fixed_reward_minor)} flat`
                              : `${(c.percentage_bps / 100).toFixed(2)}% cashback`}{" "}
                            · Customer cap {formatUSD(c.customer_cap_minor)}
                          </div>
                          <div style={{ color: "var(--muted)" }}>
                            Fee {(c.platform_fee_bps / 100).toFixed(1)}% · Loss reserve{" "}
                            {(c.loss_reserve_bps / 100).toFixed(1)}% · Return window{" "}
                            {Math.round(c.return_window_seconds / 86400)}d ({c.return_treatment})
                          </div>
                        </td>
                        <td style={{ minWidth: 190 }}>
                          <div className="mono">
                            Avail: <strong>{formatUSD(c.available_minor)}</strong> / Funded:{" "}
                            {formatUSD(c.funded_minor)}
                          </div>
                          <div className="progress-track">
                            <div className="progress-fill" style={{ width: `${pct}%` }} />
                          </div>
                          <div style={{ fontSize: "0.72rem", color: "var(--muted)", marginTop: 2 }}>
                            Cap: {formatUSD(c.budget_cap_minor)} · Promised:{" "}
                            {formatUSD(c.promised_reward_minor)}
                          </div>
                        </td>
                        <td className="mono">
                          <div>Fee/Res: {formatUSD(c.reserved_minor)}</div>
                          <div style={{ color: "var(--muted)" }}>
                            Loss Res: {formatUSD(c.loss_reserve_minor)}
                          </div>
                        </td>
                        <td>
                          <span className={statusPillClass(c.status)}>{c.status}</span>
                        </td>
                        <td>
                          <div className="btn-row" style={{ marginTop: 0 }}>
                            <button
                              className="btn btn-sm"
                              disabled={busy}
                              onClick={() =>
                                void runAction(`Added $500 reconciled funding to ${c.campaign_id}`, async () => {
                                  await api.fundCampaign(c.campaign_id, {
                                    provider_ref: `ach_topup_${Date.now()}`,
                                    amount_minor: "50000",
                                    reconciled: true,
                                  });
                                })
                              }
                            >
                              +$500 Fund
                            </button>
                            <button
                              className="btn btn-sm"
                              disabled={busy}
                              onClick={() =>
                                void runAction(
                                  `Updated ${c.campaign_id} status`,
                                  async () => {
                                    await api.setCampaignStatus(
                                      c.campaign_id,
                                      c.status === "active" ? "paused" : "active",
                                      operatorToken,
                                    );
                                  },
                                )
                              }
                            >
                              {c.status === "active" ? "Pause" : "Resume"}
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>

          <div className="grid-2">
            {/* Create Versioned Campaign */}
            <div className="card">
              <h2>Create Versioned Campaign</h2>
              <div className="form-grid" style={{ marginTop: 12 }}>
                <label className="field">
                  Merchant
                  <select value={cmpMerchant} onChange={(e) => setCmpMerchant(e.target.value)}>
                    {merchants.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.name} ({m.id})
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field">
                  Campaign Name
                  <input type="text" value={cmpName} onChange={(e) => setCmpName(e.target.value)} />
                </label>
                <label className="field">
                  Reward Type
                  <select
                    value={cmpType}
                    onChange={(e) => setCmpType(e.target.value as "percentage" | "fixed")}
                  >
                    <option value="percentage">Percentage (bps)</option>
                    <option value="fixed">Fixed USD</option>
                  </select>
                </label>
                {cmpType === "percentage" ? (
                  <label className="field">
                    Rate (bps, 600 = 6%)
                    <input
                      type="number"
                      value={cmpRateBps}
                      onChange={(e) => setCmpRateBps(e.target.value)}
                    />
                  </label>
                ) : (
                  <label className="field">
                    Fixed Reward (USD)
                    <input
                      type="text"
                      value={cmpFixedUSD}
                      onChange={(e) => setCmpFixedUSD(e.target.value)}
                    />
                  </label>
                )}
                <label className="field">
                  Budget Cap (USD)
                  <input
                    type="text"
                    value={cmpBudgetUSD}
                    onChange={(e) => setCmpBudgetUSD(e.target.value)}
                  />
                </label>
                <label className="field">
                  Per-Customer Cap (USD)
                  <input
                    type="text"
                    value={cmpCustCapUSD}
                    onChange={(e) => setCmpCustCapUSD(e.target.value)}
                  />
                </label>
                <label className="field">
                  Return Window (days)
                  <input
                    type="number"
                    value={cmpRetDays}
                    onChange={(e) => setCmpRetDays(e.target.value)}
                  />
                </label>
                <label className="field">
                  Platform Fee (bps)
                  <input
                    type="number"
                    value={cmpFeeBps}
                    onChange={(e) => setCmpFeeBps(e.target.value)}
                  />
                </label>
                <label className="field">
                  Loss Reserve (bps)
                  <input
                    type="number"
                    value={cmpLossBps}
                    onChange={(e) => setCmpLossBps(e.target.value)}
                  />
                </label>
              </div>
              <div className="btn-row">
                <button
                  className="btn btn-primary"
                  disabled={busy}
                  onClick={() =>
                    void runAction("Created & prefunded new campaign version", async () => {
                      const budgetMinor = parseDecimalToBaseUnits(cmpBudgetUSD, 2);
                      const created = await api.createCampaign({
                        merchant_id: cmpMerchant,
                        name: cmpName,
                        reward_type: cmpType,
                        percentage_bps: Number(cmpRateBps),
                        fixed_reward_minor: parseDecimalToBaseUnits(cmpFixedUSD, 2),
                        budget_cap_minor: budgetMinor,
                        customer_cap_minor: parseDecimalToBaseUnits(cmpCustCapUSD, 2),
                        return_window_seconds: Number(cmpRetDays) * 86400,
                        return_treatment: "pro_rata",
                        platform_fee_bps: Number(cmpFeeBps),
                        loss_reserve_bps: Number(cmpLossBps),
                      });
                      await api.fundCampaign(created.campaign_id, {
                        provider_ref: `ach_init_${created.campaign_id}_${Date.now()}`,
                        amount_minor: budgetMinor,
                        reconciled: true,
                      });
                    })
                  }
                >
                  Create & Prefund Campaign
                </button>
              </div>
            </div>

            {/* Signed Merchant Webhook Simulator */}
            <div className="card">
              <h2>Simulate Signed Merchant Event (HMAC-SHA256)</h2>
              <div className="form-grid" style={{ marginTop: 12 }}>
                <label className="field">
                  Event Type
                  <select value={evtType} onChange={(e) => setEvtType(e.target.value)}>
                    <option value="order.paid">order.paid (Reserve & accrue pending)</option>
                    <option value="order.fulfilled">order.fulfilled (Start return clock)</option>
                    <option value="order.refunded">order.refunded (Partial/full reversal)</option>
                    <option value="order.chargeback">order.chargeback (Post-payout reserve/exception)</option>
                  </select>
                </label>
                <label className="field">
                  Merchant
                  <select
                    value={evtMerchant}
                    onChange={(e) => {
                      setEvtMerchant(e.target.value);
                      const first = campaigns.find((c) => c.merchant_id === e.target.value);
                      if (first) setEvtCampaign(first.campaign_id);
                    }}
                  >
                    {merchants.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field">
                  Campaign
                  <select value={evtCampaign} onChange={(e) => setEvtCampaign(e.target.value)}>
                    {campaigns
                      .filter((c) => c.merchant_id === evtMerchant)
                      .map((c) => (
                        <option key={c.campaign_id} value={c.campaign_id}>
                          {c.name} ({c.campaign_id})
                        </option>
                      ))}
                  </select>
                </label>
                <label className="field">
                  Order ID
                  <input
                    type="text"
                    value={evtOrderId}
                    onChange={(e) => setEvtOrderId(e.target.value)}
                  />
                </label>
                <label className="field">
                  Customer ID
                  <input
                    type="text"
                    value={evtCustomer}
                    onChange={(e) => setEvtCustomer(e.target.value)}
                  />
                </label>
                <label className="field">
                  {evtType === "order.paid" ? "Order Subtotal (USD)" : "Refund/Dispute Amount (USD)"}
                  <input
                    type="text"
                    value={evtSubtotalUSD}
                    onChange={(e) => setEvtSubtotalUSD(e.target.value)}
                  />
                </label>
              </div>
              {evtType === "order.paid" && (
                <label className="checkbox-row">
                  <input
                    type="checkbox"
                    checked={evtFulfilled}
                    onChange={(e) => setEvtFulfilled(e.target.checked)}
                  />
                  <span>Mark order immediately fulfilled (otherwise requires separate order.fulfilled event)</span>
                </label>
              )}
              <div className="btn-row">
                <button
                  className="btn btn-primary"
                  disabled={busy}
                  onClick={() =>
                    void runAction(`Processed signed ${evtType} for ${evtOrderId}`, async () => {
                      const amtMinor = parseDecimalToBaseUnits(evtSubtotalUSD, 2);
                      const camp = campaigns.find((c) => c.campaign_id === evtCampaign);
                      const items =
                        camp && camp.eligible_skus.length > 0
                          ? [{ sku: camp.eligible_skus[0], quantity: 1, subtotal_minor: amtMinor }]
                          : [];
                      await api.simulateMerchantEvent({
                        event_id: `evt_ui_${Date.now()}`,
                        event_type: evtType,
                        merchant_id: evtMerchant,
                        campaign_id: evtCampaign,
                        order_id: evtOrderId,
                        customer_id: evtCustomer,
                        order_subtotal_minor: evtType === "order.paid" ? amtMinor : "0",
                        refund_amount_minor:
                          evtType === "order.refunded" || evtType === "order.chargeback"
                            ? amtMinor
                            : "0",
                        currency: "USD",
                        fulfilled: evtFulfilled,
                        items,
                      });
                    })
                  }
                >
                  Send Signed Event
                </button>
              </div>
            </div>
          </div>
        </section>
      )}

      {tab === "operator" && (
        <section>
          <div className="card">
            <div className="card-header">
              <h2>
                <ShieldCheck size={16} /> Operator Controls, Pause & Reconciliation Workers
              </h2>
              <label className="field" style={{ flexDirection: "row", alignItems: "center" }}>
                <span>Operator Token:</span>
                <input
                  type="text"
                  value={operatorToken}
                  onChange={(e) => setOperatorToken(e.target.value)}
                  style={{ width: 150 }}
                />
              </label>
            </div>

            <div className="form-grid">
              <label className="field">
                Audit Reason for Pause Toggle
                <input
                  type="text"
                  value={pauseReason}
                  onChange={(e) => setPauseReason(e.target.value)}
                />
              </label>
            </div>

            <div className="btn-row">
              <button
                className="btn btn-primary"
                disabled={busy}
                onClick={() =>
                  void runAction("Ran worker reconciliation tick", async () => {
                    await api.runReconciliation(operatorToken);
                  })
                }
              >
                <RefreshCw size={14} /> Run Worker & Reconciliation Tick
              </button>
              <button
                className="btn"
                disabled={busy}
                onClick={() =>
                  void runAction("Advanced sandbox clock +15 days and ran eligibility worker", async () => {
                    await api.advanceClock(operatorToken, 86400 * 15);
                  })
                }
              >
                <Clock size={14} /> Advance Clock +15d (Clear Return Windows)
              </button>
              <button
                className="btn btn-danger"
                disabled={busy}
                onClick={() =>
                  void runAction("Injected -$5.00 provider custody drift and reconciled", async () => {
                    await api.setCustodyDrift(operatorToken, "-500");
                  })
                }
              >
                <AlertTriangle size={14} /> Simulate -$5 Custody Drift
              </button>
              <button
                className="btn"
                disabled={busy}
                onClick={() =>
                  void runAction("Cleared sandbox custody drift", async () => {
                    await api.setCustodyDrift(operatorToken, "0");
                  })
                }
              >
                Clear Drift
              </button>
              <button
                className="btn btn-danger"
                disabled={busy}
                onClick={() =>
                  void runAction("Updated product pause state", async () => {
                    const paused = !(
                      sysStatus?.pause.issuance_paused || sysStatus?.pause.payouts_paused
                    );
                    await api.setPause(operatorToken, paused, paused, pauseReason);
                  })
                }
              >
                {sysStatus?.pause.issuance_paused || sysStatus?.pause.payouts_paused ? (
                  <>
                    <PlayCircle size={14} /> Resume All Flows
                  </>
                ) : (
                  <>
                    <PauseCircle size={14} /> Global Emergency Pause
                  </>
                )}
              </button>
            </div>
          </div>

          {/* Double-Entry Ledger Summary */}
          {ledger && (
            <div className="card">
              <div className="card-header">
                <h2>Double-Entry Ledger Trial Balance & Funding Coverage</h2>
                <div style={{ display: "flex", gap: 8 }}>
                  <span className={ledger.balanced ? "pill pill-green" : "pill pill-red"}>
                    {ledger.balanced ? "All Journals Balanced" : "IMBALANCE DETECTED"}
                  </span>
                  <span
                    className={
                      ledger.funding_covers_obligations ? "pill pill-green" : "pill pill-red"
                    }
                  >
                    {ledger.funding_covers_obligations
                      ? "Funding Covers Obligations"
                      : "COVERAGE DEFICIT"}
                  </span>
                </div>
              </div>

              <div className="stats-grid">
                <div className="stat-card">
                  <div className="stat-label">Provider Custody (USD)</div>
                  <div className="stat-value">{formatUSD(ledger.provider_custody_usd)}</div>
                </div>
                <div className="stat-card">
                  <div className="stat-label">Campaign Available</div>
                  <div className="stat-value">{formatUSD(ledger.campaign_available_usd)}</div>
                </div>
                <div className="stat-card">
                  <div className="stat-label">Pending + Payable + Clearing</div>
                  <div className="stat-value">{formatUSD(ledger.total_obligations_usd)}</div>
                  <div className="stat-sub">
                    Pend {formatUSD(ledger.reward_pending_usd)} · Pay{" "}
                    {formatUSD(ledger.reward_payable_usd)} · Clr{" "}
                    {formatUSD(ledger.payout_clearing_usd)}
                  </div>
                </div>
                <div className="stat-card">
                  <div className="stat-label">Loss Reserve & Revenue</div>
                  <div className="stat-value">{formatUSD(ledger.platform_revenue_usd)}</div>
                  <div className="stat-sub">Loss Reserve: {formatUSD(ledger.loss_reserve_usd)}</div>
                </div>
              </div>

              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Asset / Network</th>
                      <th>Total Debits</th>
                      <th>Total Credits</th>
                      <th>Invariant</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ledger.trial_balance.map((tb) => (
                      <tr key={`${tb.asset}-${tb.network}`}>
                        <td className="mono">
                          {tb.asset} / {tb.network}
                        </td>
                        <td className="mono">{formatDecimal(tb.total_debits, tb.decimals)}</td>
                        <td className="mono">{formatDecimal(tb.total_credits, tb.decimals)}</td>
                        <td>
                          <span className={tb.balanced ? "pill pill-green" : "pill pill-red"}>
                            {tb.balanced ? "Balanced" : "Mismatch"}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Operator Reconciliation Exception Queue */}
          <div className="card">
            <div className="card-header">
              <h2>
                <AlertTriangle size={16} /> Reconciliation Exception Queue ({openExceptions.length}{" "}
                open)
              </h2>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>ID / Category</th>
                    <th>Severity</th>
                    <th>Entity</th>
                    <th>Expected / Actual / Delta</th>
                    <th>Summary</th>
                    <th>Status / Resolution</th>
                  </tr>
                </thead>
                <tbody>
                  {exceptions.map((ex) => (
                    <tr key={ex.id}>
                      <td>
                        <div className="mono">{ex.id}</div>
                        <div style={{ fontSize: "0.78rem", color: "var(--muted)" }}>
                          {ex.category}
                        </div>
                      </td>
                      <td>
                        <span className={statusPillClass(ex.severity)}>{ex.severity}</span>
                      </td>
                      <td className="mono">
                        {ex.entity_type}:{ex.entity_id}
                      </td>
                      <td className="mono">
                        <div>Exp: {formatDecimal(ex.expected_base, ex.decimals)} {ex.asset}</div>
                        <div>Act: {formatDecimal(ex.actual_base, ex.decimals)} {ex.asset}</div>
                        <div>Δ: {formatDecimal(ex.delta_base, ex.decimals)} {ex.asset}</div>
                      </td>
                      <td style={{ maxWidth: 320 }}>{ex.summary}</td>
                      <td>
                        <span className={statusPillClass(ex.status)}>{ex.status}</span>
                        {ex.status !== "resolved" ? (
                          <div style={{ marginTop: 6, display: "flex", gap: 6 }}>
                            <input
                              type="text"
                              placeholder="Resolution note"
                              value={resolveNotes[ex.id] || ""}
                              onChange={(e) =>
                                setResolveNotes({ ...resolveNotes, [ex.id]: e.target.value })
                              }
                            />
                            <button
                              className="btn btn-sm btn-primary"
                              disabled={busy}
                              onClick={() =>
                                void runAction(`Resolved exception ${ex.id}`, async () => {
                                  await api.resolveException(
                                    operatorToken,
                                    ex.id,
                                    resolveNotes[ex.id] || "Verified and reconciled by operator",
                                  );
                                })
                              }
                            >
                              <CheckCircle2 size={13} /> Resolve
                            </button>
                          </div>
                        ) : (
                          <div style={{ fontSize: "0.75rem", color: "var(--muted)", marginTop: 4 }}>
                            {ex.resolution_note} ({ex.resolved_by})
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                  {exceptions.length === 0 && (
                    <tr>
                      <td colSpan={6} style={{ color: "var(--muted)", textAlign: "center" }}>
                        No reconciliation exceptions. All accounts and provider balances match.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          {/* Immutable Journal Entries */}
          <div className="card">
            <div className="card-header">
              <h2>Recent Immutable Journal Entries ({journals.length})</h2>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Entry</th>
                    <th>Type</th>
                    <th>Description</th>
                    <th>Postings (Balanced by Asset)</th>
                  </tr>
                </thead>
                <tbody>
                  {journals.slice(0, 15).map((je) => (
                    <tr key={je.id}>
                      <td className="mono">{je.id}</td>
                      <td className="mono">{je.entry_type}</td>
                      <td>{je.description}</td>
                      <td className="mono" style={{ fontSize: "0.76rem" }}>
                        {je.postings.map((p, idx) => (
                          <div key={idx}>
                            {p.direction === "debit" ? "DR" : "CR"} {p.account_code}[{p.scope_id}]{" "}
                            {formatDecimal(p.amount, p.decimals)} {p.asset}
                          </div>
                        ))}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
