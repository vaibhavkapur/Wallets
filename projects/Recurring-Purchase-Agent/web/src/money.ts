import type { Money, ScheduleSpec } from "./types";

/**
 * Formats a Money object strictly using BigInt integer math so large
 * decimal-string minor units are never converted to IEEE-754 floats.
 */
export function formatMoney(m: Money | undefined | null): string {
  if (!m) return "$0.00";
  const raw = BigInt(m.amount_minor || "0");
  const neg = raw < 0n;
  const abs = neg ? -raw : raw;
  const sign = neg ? "-" : "";

  if (m.decimals === 0) {
    return `${sign}${abs.toString()} ${m.asset}`;
  }
  const scale = 10n ** BigInt(m.decimals);
  const whole = abs / scale;
  const frac = (abs % scale).toString().padStart(m.decimals, "0");

  if (m.asset === "USD" && m.decimals === 2) {
    return `${sign}$${whole.toString()}.${frac}`;
  }
  return `${sign}${whole.toString()}.${frac} ${m.asset}`;
}

/**
 * Parses a human dollar input string (e.g. "29.50" or "$30") into a USD Money
 * object using exact string/BigInt manipulation with zero floating-point math.
 */
export function parseUSDToMoney(input: string): Money | null {
  const cleaned = input.trim().replace(/^\$/, "").replace(/,/g, "");
  if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) {
    return null;
  }
  const [wholePart, fracPart = ""] = cleaned.split(".");
  const centsStr = wholePart + fracPart.padEnd(2, "0");
  const minor = BigInt(centsStr);
  return {
    amount_minor: minor.toString(),
    asset: "USD",
    network: "fiat",
    decimals: 2,
  };
}

/**
 * Computes integer percentage (0..100) using BigInt arithmetic.
 */
export function budgetPercent(used: Money | undefined, cap: Money | undefined): number {
  if (!used || !cap) return 0;
  const u = BigInt(used.amount_minor || "0");
  const c = BigInt(cap.amount_minor || "0");
  if (c <= 0n) return 0;
  const pct = (u * 100n) / c;
  if (pct < 0n) return 0;
  if (pct > 100n) return 100;
  return Number(pct);
}

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function describeSchedule(spec: ScheduleSpec): string {
  const hh = String(spec.local_hour).padStart(2, "0");
  const mm = String(spec.local_minute).padStart(2, "0");
  if (spec.kind === "interval") {
    return `Every ${spec.interval_days} days at ${hh}:${mm} (${spec.timezone})`;
  }
  if (spec.calendar_cadence === "weekly") {
    const dow = WEEKDAYS[spec.day_of_week ?? 1] ?? "Mon";
    return `Calendar: Weekly on ${dow} at ${hh}:${mm} (${spec.timezone})`;
  }
  return `Calendar: Monthly on day ${spec.day_of_month ?? 1} at ${hh}:${mm} (${spec.timezone})`;
}

export function formatTimeShort(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().replace("T", " ").slice(0, 16) + " UTC";
}
