import type { Evaluation } from "@unowallet/optimizer-sdk";
import {
  explain,
  explainExclusion,
  formatAmount,
  formatMicroUsd,
  outcomeTitles,
  rationaleMessages,
} from "@unowallet/optimizer-sdk";
import type { EvaluationResponse, OptionResult } from "@unowallet/optimizer-sdk";

export interface ComparisonProps {
  evaluation: Evaluation;
  /** Present in assisted mode: lets the customer approve one eligible option. */
  onApprove?: (option: OptionResult) => void;
  approving?: string | null;
  onRefresh?: () => void;
  busy?: boolean;
}

function bannerClass(outcome: EvaluationResponse["outcome"]): string {
  switch (outcome) {
    case "preferred":
      return "ok";
    case "tie":
      return "info";
    case "insufficient_data":
      return "warn";
    default:
      return "bad";
  }
}

export function Comparison({ evaluation, onApprove, approving, onRefresh, busy }: ComparisonProps) {
  const r = evaluation.result;
  const expired = evaluation.status === "expired";
  const preferred = r.preferred_option_id;
  const labelOf = (id: string | null | undefined) => r.options.find((o) => o.option_id === id)?.label ?? id ?? "";

  return (
    <section aria-label="Comparison">
      <div className={`banner ${expired ? "warn" : bannerClass(r.outcome)}`} role="status">
        <strong>{outcomeTitles[r.outcome]}</strong>
        {preferred && (
          <>
            {": "}
            <span data-testid="preferred-label">{labelOf(preferred)}</span>
          </>
        )}
        {r.outcome === "tie" && `: ${r.tied_option_ids.map(labelOf).join(" and ")}`}
        <div className="subtle">{rationaleMessages[r.rationale]}</div>
        {expired && <div>These quotes have expired. Refresh to compare again before deciding.</div>}
      </div>

      {r.savings_vs_baseline && (
        <div className="panel" style={{ marginBottom: 12 }} data-testid="savings">
          <h2>
            {labelOf(r.savings_vs_baseline.option_id)} compared with your default ({labelOf(r.savings_vs_baseline.baseline_option_id)})
          </h2>
          <table>
            <tbody>
              <tr>
                <td>Immediate difference (what you pay now)</td>
                <td className="amt" data-testid="savings-immediate">
                  {formatMicroUsd(r.savings_vs_baseline.immediate_micro_usd, { explicitSign: true })}
                </td>
              </tr>
              <tr className="estimate">
                <td>
                  Estimated rewards difference <span className="tag estimate">estimate</span>
                </td>
                <td className="amt">{formatMicroUsd(r.savings_vs_baseline.estimated_rewards_micro_usd, { explicitSign: true })}</td>
              </tr>
              <tr className="total">
                <td>Estimated total saving</td>
                <td className="amt" data-testid="savings-total">
                  {formatMicroUsd(r.savings_vs_baseline.estimated_total_micro_usd, { explicitSign: true })}
                </td>
              </tr>
              <tr className="muted">
                <td>Your threshold for switching</td>
                <td className="amt">
                  {formatMicroUsd(r.savings_vs_baseline.threshold_micro_usd)} {r.savings_vs_baseline.meets_threshold ? "(met)" : "(not met)"}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      )}

      <div className="grid two">
        {r.options.map((o) => (
          <OptionCard
            key={o.option_id}
            option={o}
            isPreferred={o.option_id === preferred}
            isBaseline={o.option_id === r.baseline_option_id}
            onApprove={onApprove && !expired && o.status === "eligible" ? () => onApprove(o) : undefined}
            approving={approving === o.option_id}
          />
        ))}
      </div>

      <div className="grid two" style={{ marginTop: 16 }}>
        <div className="panel">
          <h2>What could change this result</h2>
          <ul data-testid="sensitivities">
            {r.sensitivities.map((s, i) => (
              <li key={i}>{explain(s)}</li>
            ))}
            {r.sensitivities.length === 0 && <li className="subtle">Nothing recorded.</li>}
          </ul>
        </div>
        <div className="panel">
          <h2>Assumptions used</h2>
          <ul data-testid="assumptions">
            {r.assumptions.map((a, i) => (
              <li key={i}>{explain(a)}</li>
            ))}
            {r.assumptions.length === 0 && <li className="subtle">None beyond the quotes themselves.</li>}
          </ul>
        </div>
      </div>

      <div className="row" style={{ marginTop: 12 }}>
        {onRefresh && (
          <button className="secondary" onClick={onRefresh} disabled={busy}>
            Refresh quotes
          </button>
        )}
        <span className="subtle">
          Valid until {evaluation.expires_at ?? "—"} · {evaluation.disclosure === "recommendation_only" && "Recommendation only: no payment has been made and no saving has been realized."}
        </span>
      </div>
      {evaluation.warnings.length > 0 && (
        <div className="banner warn" style={{ marginTop: 12 }}>
          {evaluation.warnings.map((w) => (
            <div key={w}>
              {w === "default_method_not_offered" ? "Your default payment method is not offered for this basket, so there is no baseline for savings." : w}
            </div>
          ))}
        </div>
      )}
      <p className="subtle" style={{ marginTop: 12 }}>
        Evaluation <code>{evaluation.id}</code> · contract {evaluation.versions.contract} · engine {evaluation.versions.engine} · formula{" "}
        {evaluation.versions.formula} · rounding {evaluation.versions.rounding} · policy {evaluation.versions.policy} · preferences{" "}
        {evaluation.versions.preference} · input <code>{evaluation.input_sha256.slice(0, 16)}…</code>
      </p>
    </section>
  );
}

interface OptionCardProps {
  option: OptionResult;
  isPreferred: boolean;
  isBaseline: boolean;
  onApprove?: () => void;
  approving: boolean;
}

export function OptionCard({ option: o, isPreferred, isBaseline, onApprove, approving }: OptionCardProps) {
  const b = o.breakdown;
  const excluded = o.status === "excluded";
  return (
    <article className={`option ${isPreferred ? "preferred" : ""} ${excluded ? "excluded" : ""}`} data-testid={`option-${o.option_id}`}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h3>
          {o.label}
          <span className="tag">{o.rail === "card" ? "card" : "stablecoin"}</span>
          {isPreferred && <span className="tag ok">recommended</span>}
          {isBaseline && <span className="tag">your default</span>}
          {excluded && <span className="tag bad">excluded</span>}
        </h3>
        {b && !excluded && (
          <span className="big" data-testid={`net-${o.option_id}`}>
            {formatMicroUsd(b.estimated_net_cost_micro_usd)}
          </span>
        )}
      </div>

      {excluded && (
        <ul data-testid={`exclusions-${o.option_id}`}>
          {o.exclusions.map((x, i) => (
            <li key={i}>
              {explainExclusion(x.code, x.params)} <span className="tag">{x.kind === "missing_data" ? "missing data" : "not eligible"}</span>
            </li>
          ))}
        </ul>
      )}

      {b && (
        <table style={{ marginTop: 8 }}>
          <tbody>
            <tr>
              <td>Delivered price</td>
              <td className="amt">{formatMicroUsd(b.delivered_price_micro_usd)}</td>
            </tr>
            {b.fees.map((f) => (
              <tr key={f.fee_id}>
                <td>
                  {f.description || f.kind.replace(/_/g, " ")} <span className="tag">{f.kind.replace(/_/g, " ")} fee</span>
                  <div className="subtle">{formatAmount(f.source_amount)}</div>
                </td>
                <td className="amt">+{formatMicroUsd(f.micro_usd)}</td>
              </tr>
            ))}
            {b.discounts.map((d) => (
              <tr key={d.discount_id} className={d.applied ? "" : "muted"}>
                <td>
                  {d.description || d.discount_id}
                  {!d.applied && d.not_applied_reason && (
                    <div className="subtle">{explain({ code: d.not_applied_reason, option_id: o.option_id, params: { discount_id: d.discount_id } })}</div>
                  )}
                </td>
                <td className="amt">{d.applied ? `-${formatMicroUsd(d.micro_usd)}` : "not applied"}</td>
              </tr>
            ))}
            <tr className="total">
              <td>Immediate cost (what you pay now)</td>
              <td className="amt" data-testid={`immediate-${o.option_id}`}>
                {formatMicroUsd(b.immediate_cost_micro_usd)}
              </td>
            </tr>
            {b.rewards.map((rw) => (
              <tr key={rw.rule_id} className={rw.counted ? "estimate" : "muted"}>
                <td>
                  {rw.description || rw.rule_id} <span className="tag estimate">estimated reward</span>
                  {rw.points !== null && <div className="subtle">{rw.points} points</div>}
                  {rw.cap_applied && <div className="subtle">cap applied</div>}
                  {!rw.counted && rw.not_counted_reason && (
                    <div className="subtle">{explain({ code: rw.not_counted_reason, option_id: o.option_id, params: { rule_id: rw.rule_id } })}</div>
                  )}
                </td>
                <td className="amt">{rw.counted ? `-${formatMicroUsd(rw.micro_usd)}` : "not counted"}</td>
              </tr>
            ))}
            <tr className="total estimate">
              <td>
                Estimated net cost <span className="tag estimate">includes estimated rewards</span>
              </td>
              <td className="amt">{formatMicroUsd(b.estimated_net_cost_micro_usd)}</td>
            </tr>
            <tr className="muted">
              <td>Amount charged to this method</td>
              <td className="amt" data-testid={`due-${o.option_id}`}>
                {formatAmount(b.amount_due)}
              </td>
            </tr>
          </tbody>
        </table>
      )}

      {o.funding_step && (
        <div className="banner info" style={{ marginTop: 8 }}>
          Separate funding step: add {formatAmount(o.funding_step.amount)} from {o.funding_step.source.replace(/_/g, " ")} (fees{" "}
          {formatMicroUsd(o.funding_step.fees_micro_usd)}, quote {o.funding_step.quote_id} valid until {o.funding_step.valid_until}).
        </div>
      )}

      {o.notes.length > 0 && (
        <ul>
          {o.notes.map((n, i) => (
            <li key={i}>{explain(n)}</li>
          ))}
        </ul>
      )}

      <details>
        <summary>Trade-offs (not priced)</summary>
        <ul>
          <li>Purchase protection: {o.tradeoffs.purchase_protection}</li>
          <li>Uses credit: {o.tradeoffs.uses_credit ? "yes" : "no"}</li>
          <li>Refunds: {o.tradeoffs.refund_asset}; {o.tradeoffs.refund_timing}</li>
          <li>Settlement: {o.tradeoffs.settlement}</li>
          {(o.tradeoffs.notes ?? []).map((n, i) => (
            <li key={i}>{n}</li>
          ))}
        </ul>
      </details>

      {onApprove && b && (
        <div className="row" style={{ marginTop: 10 }}>
          <button onClick={onApprove} disabled={approving} data-testid={`approve-${o.option_id}`}>
            {approving ? "Handing off…" : `Approve ${formatAmount(b.amount_due)} with ${o.label}`}
          </button>
        </div>
      )}
    </article>
  );
}
