import { useState } from "react";
import type { CustomerPreferences } from "@unowallet/optimizer-sdk";
import { formatDecimal } from "@unowallet/optimizer-sdk";

/** Parses "1.25" into minor units "125" by string manipulation only. */
export function toBaseUnits(text: string, decimals: number): string | null {
  const m = /^(\d+)(?:\.(\d*))?$/.exec(text.trim());
  if (!m) return null;
  const fraction = (m[2] ?? "").padEnd(decimals, "0");
  if (fraction.length > decimals) return null;
  const joined = (m[1] + fraction).replace(/^0+(?=\d)/, "");
  return joined === "" ? "0" : joined;
}

export interface PreferencesFormProps {
  value: CustomerPreferences;
  isDefault: boolean;
  version: number;
  optionIds: string[];
  onSave: (prefs: CustomerPreferences) => Promise<void>;
  busy?: boolean;
}

const USDC = { code: "USDC", network: "base", decimals: 6 };

export function PreferencesForm({ value, isDefault, version, optionIds, onSave, busy }: PreferencesFormProps) {
  const reserve = value.liquidity_reserves.find((r) => r.asset.code === "USDC");
  const valuation = value.point_valuations.find((v) => v.program_id === "pts_demo");
  const [form, setForm] = useState({
    defaultOption: value.default_option_id ?? "",
    threshold: formatDecimal(value.min_savings_threshold_minor, 2).replace(/,/g, ""),
    consentCard: value.consented_rails.includes("card"),
    consentStablecoin: value.consented_rails.includes("stablecoin"),
    consentFunding: value.consent_funding_step,
    requireProtection: value.require_purchase_protection,
    avoidCredit: value.avoid_credit,
    cardOnly: value.allowed_rails?.length === 1 && value.allowed_rails[0] === "card",
    reserve: reserve ? formatDecimal(reserve.base_units, 6).replace(/,/g, "") : "",
    pointCents: valuation ? formatDecimal(valuation.micro_usd_per_point, 4).replace(/,/g, "") : "1.00",
    pointsUnstable: valuation?.stability === "unstable",
  });
  const [error, setError] = useState<string | null>(null);

  const set = <K extends keyof typeof form>(key: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [key]: v }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const threshold = toBaseUnits(form.threshold, 2);
    if (threshold === null) return setError("Enter the threshold in dollars, e.g. 1.00");
    const reserveUnits = form.reserve ? toBaseUnits(form.reserve, 6) : null;
    if (form.reserve && reserveUnits === null) return setError("Enter the USDC reserve with at most 6 decimals");
    const pointMicro = toBaseUnits(form.pointCents, 4);
    if (pointMicro === null) return setError("Enter the point value in cents, e.g. 1.00");
    const consented: ("card" | "stablecoin")[] = [];
    if (form.consentCard) consented.push("card");
    if (form.consentStablecoin) consented.push("stablecoin");
    setError(null);
    await onSave({
      default_option_id: form.defaultOption || null,
      min_savings_threshold_minor: threshold,
      consented_rails: consented,
      consent_funding_step: form.consentFunding,
      allowed_rails: form.cardOnly ? ["card"] : null,
      require_purchase_protection: form.requireProtection,
      avoid_credit: form.avoidCredit,
      liquidity_reserves: reserveUnits ? [{ asset: USDC, base_units: reserveUnits }] : [],
      point_valuations: [
        { program_id: "pts_demo", valuation_version: "customer-1", micro_usd_per_point: pointMicro, stability: form.pointsUnstable ? "unstable" : "stable" },
      ],
    });
  }

  return (
    <form onSubmit={submit} aria-label="Preferences">
      <h2>
        Your preferences <span className="tag">{isDefault ? "defaults" : `version ${version}`}</span>
      </h2>
      <label htmlFor="default-option">Default payment method (the baseline for savings)</label>
      <select id="default-option" value={form.defaultOption} onChange={(e) => set("defaultOption", e.target.value)}>
        <option value="">No default</option>
        {optionIds.map((id) => (
          <option key={id} value={id}>
            {id}
          </option>
        ))}
      </select>
      <label htmlFor="threshold">Minimum estimated saving before switching away from the default ($)</label>
      <input id="threshold" type="text" inputMode="decimal" value={form.threshold} onChange={(e) => set("threshold", e.target.value)} />

      <label>Methods you consent to pay with</label>
      <label className="inline">
        <input type="checkbox" checked={form.consentCard} onChange={(e) => set("consentCard", e.target.checked)} /> Card
      </label>
      <label className="inline">
        <input type="checkbox" checked={form.consentStablecoin} onChange={(e) => set("consentStablecoin", e.target.checked)} /> Stablecoin (USDC on Base)
      </label>
      <label className="inline">
        <input type="checkbox" checked={form.consentFunding} onChange={(e) => set("consentFunding", e.target.checked)} /> Allow a separate, quoted funding or conversion step
      </label>

      <label>Hard constraints (never priced; applied as filters)</label>
      <label className="inline">
        <input type="checkbox" checked={form.requireProtection} onChange={(e) => set("requireProtection", e.target.checked)} /> Require purchase protection
      </label>
      <label className="inline">
        <input type="checkbox" checked={form.avoidCredit} onChange={(e) => set("avoidCredit", e.target.checked)} /> Do not use credit
      </label>
      <label className="inline">
        <input type="checkbox" checked={form.cardOnly} onChange={(e) => set("cardOnly", e.target.checked)} /> Only recommend card payments
      </label>

      <label htmlFor="reserve">Keep at least this much USDC untouched (liquidity reserve)</label>
      <input id="reserve" type="text" inputMode="decimal" placeholder="0" value={form.reserve} onChange={(e) => set("reserve", e.target.value)} />

      <label htmlFor="points">Value of one pts_demo point (cents)</label>
      <input id="points" type="text" inputMode="decimal" value={form.pointCents} onChange={(e) => set("pointCents", e.target.value)} />
      <label className="inline">
        <input type="checkbox" checked={form.pointsUnstable} onChange={(e) => set("pointsUnstable", e.target.checked)} /> This valuation is unstable (count points at zero)
      </label>

      {error && <div className="banner bad">{error}</div>}
      <div className="row" style={{ marginTop: 12 }}>
        <button type="submit" disabled={busy}>
          Save preferences
        </button>
      </div>
    </form>
  );
}
