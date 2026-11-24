import { amount, formatUnits, isAmount, type Amount } from "@asa/sdk";

/** Format a base-unit amount for display. Never goes through Number. */
export function fmtAmount(value: string | undefined, decimals = 6, symbol = "USDC"): string {
  if (value === undefined || !isAmount(value)) return "—";
  return `${formatUnits(value as Amount, decimals)} ${symbol}`;
}

export function fmtTime(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function shortId(id: string | undefined, n = 10): string {
  if (!id) return "—";
  return id.length > n + 3 ? `${id.slice(0, n)}…` : id;
}

/** Validate a human decimal into base units; returns an error string instead of throwing. */
export function toBaseUnits(text: string, decimals: number): { amount?: Amount; error?: string } {
  const m = /^(\d+)(?:\.(\d+))?$/.exec(text.trim());
  if (!m) return { error: "enter a decimal number like 12.50" };
  const whole = m[1] ?? "0";
  const frac = m[2] ?? "";
  if (frac.length > decimals) return { error: `at most ${decimals} decimal places` };
  const base = BigInt(whole) * 10n ** BigInt(decimals) + BigInt((frac + "0".repeat(decimals - frac.length)) || "0");
  try {
    return { amount: amount(base) };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}
