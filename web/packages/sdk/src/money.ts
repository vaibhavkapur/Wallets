/**
 * Exact display formatting for decimal-string amounts.
 *
 * These functions only move a decimal point and insert separators. They never
 * parse an amount into a floating-point number and never round: a value with
 * sub-cent precision is shown with all of its digits. Every financial
 * calculation lives in the engine.
 */

const UNITS_PATTERN = /^(0|-?[1-9][0-9]{0,38})$/;

/** Throws when a string is not a canonical signed integer. */
export function assertUnits(value: string): void {
  if (!UNITS_PATTERN.test(value)) {
    throw new Error(`not a canonical integer amount: ${JSON.stringify(value)}`);
  }
}

/** Splits an integer string into sign, integer part and fraction part at `decimals`. */
export function splitDecimal(value: string, decimals: number): { negative: boolean; integer: string; fraction: string } {
  assertUnits(value);
  const negative = value.startsWith("-");
  const digits = negative ? value.slice(1) : value;
  const padded = digits.padStart(decimals + 1, "0");
  const integer = padded.slice(0, padded.length - decimals);
  const fraction = decimals === 0 ? "" : padded.slice(padded.length - decimals);
  return { negative, integer, fraction };
}

function groupThousands(integer: string): string {
  return integer.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

export interface FormatOptions {
  /** Minimum fraction digits to show (default 2). */
  minFraction?: number;
  /** Prefix a "+" to positive values (default false). */
  explicitSign?: boolean;
}

/**
 * Formats an integer amount with `decimals` as a plain decimal number,
 * keeping every significant fraction digit beyond `minFraction`.
 */
export function formatDecimal(value: string, decimals: number, options: FormatOptions = {}): string {
  const minFraction = options.minFraction ?? 2;
  const { negative, integer, fraction } = splitDecimal(value, decimals);
  let trimmed = fraction.replace(/0+$/, "");
  if (trimmed.length < minFraction) {
    trimmed = trimmed.padEnd(Math.min(minFraction, decimals), "0");
  }
  const body = trimmed ? `${groupThousands(integer)}.${trimmed}` : groupThousands(integer);
  if (negative) return `-${body}`;
  return options.explicitSign ? `+${body}` : body;
}

/** Formats a signed micro-USD string as a dollar amount, e.g. "97500000" -> "$97.50". */
export function formatMicroUsd(value: string, options: FormatOptions = {}): string {
  const text = formatDecimal(value, 6, options);
  if (text.startsWith("-")) return `-$${text.slice(1)}`;
  if (text.startsWith("+")) return `+$${text.slice(1)}`;
  return `$${text}`;
}

/** Formats an engine `Amount` ({asset, base_units}) in its own asset. */
export function formatAmount(amount: { asset: { code: string; network?: string | null; decimals: number }; base_units: string }): string {
  const text = formatDecimal(amount.base_units, amount.asset.decimals);
  if (amount.asset.code === "USD" && !amount.asset.network) return `$${text}`;
  return `${text} ${amount.asset.code}${amount.asset.network ? ` (${amount.asset.network})` : ""}`;
}

/** Compares two signed integer strings without converting them to numbers. */
export function compareUnits(a: string, b: string): -1 | 0 | 1 {
  assertUnits(a);
  assertUnits(b);
  const na = a.startsWith("-");
  const nb = b.startsWith("-");
  if (na !== nb) return na ? -1 : 1;
  const da = na ? a.slice(1) : a;
  const db = nb ? b.slice(1) : b;
  let magnitude: -1 | 0 | 1 = 0;
  if (da.length !== db.length) magnitude = da.length < db.length ? -1 : 1;
  else if (da !== db) magnitude = da < db ? -1 : 1;
  if (na) return magnitude === 0 ? 0 : magnitude === -1 ? 1 : -1;
  return magnitude;
}

/** True when the signed integer string is zero. */
export function isZero(value: string): boolean {
  assertUnits(value);
  return value === "0";
}
