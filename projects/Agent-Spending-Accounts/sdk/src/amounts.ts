/**
 * Exact base-unit amounts.
 *
 * Amounts cross the API as canonical decimal strings (e.g. "1000000" for
 * 1 USDC with 6 decimals). This module never converts them to `number`;
 * all arithmetic uses `bigint` and all formatting is string-based.
 */

/** A validated canonical base-unit amount string. */
export type Amount = string & { readonly __brand: "Amount" };

export const MAX_BASE_UNITS = (1n << 128n) - 1n;

const CANONICAL = /^(0|[1-9][0-9]{0,38})$/;

export class AmountError extends Error {
  override name = "AmountError";
}

/**
 * Validate and brand a base-unit amount. Accepts a canonical string or a
 * non-negative bigint. Numbers are rejected on purpose: a JavaScript number
 * cannot represent large base-unit values exactly.
 */
export function amount(value: string | bigint): Amount {
  if (typeof (value as unknown) === "number") {
    throw new AmountError("amounts must be strings or bigints, never numbers");
  }
  if (typeof value === "bigint") {
    if (value < 0n) throw new AmountError("amount cannot be negative");
    if (value > MAX_BASE_UNITS) throw new AmountError("amount exceeds maximum");
    return value.toString() as Amount;
  }
  if (!CANONICAL.test(value)) {
    throw new AmountError(`invalid amount ${JSON.stringify(value)}: expected canonical decimal digits`);
  }
  if (BigInt(value) > MAX_BASE_UNITS) throw new AmountError("amount exceeds maximum");
  return value as Amount;
}

export function isAmount(value: unknown): value is Amount {
  return typeof value === "string" && CANONICAL.test(value) && BigInt(value) <= MAX_BASE_UNITS;
}

export function toBigInt(a: Amount): bigint {
  return BigInt(a);
}

export function addAmounts(a: Amount, b: Amount): Amount {
  return amount(toBigInt(a) + toBigInt(b));
}

/** Returns a - b, throwing if the result would be negative. */
export function subAmounts(a: Amount, b: Amount): Amount {
  const r = toBigInt(a) - toBigInt(b);
  if (r < 0n) throw new AmountError("subtraction would be negative");
  return amount(r);
}

export function compareAmounts(a: Amount, b: Amount): -1 | 0 | 1 {
  const x = toBigInt(a);
  const y = toBigInt(b);
  return x < y ? -1 : x > y ? 1 : 0;
}

/**
 * Format base units as a decimal string with exactly `decimals` fraction
 * digits trimmed of trailing zeros (at least one digit remains after the
 * point when any fraction exists). Pure string arithmetic.
 */
export function formatUnits(a: Amount, decimals: number): string {
  if (!Number.isInteger(decimals) || decimals < 0 || decimals > 77) throw new AmountError("invalid decimals");
  if (decimals === 0) return a;
  const padded = a.padStart(decimals + 1, "0");
  const whole = padded.slice(0, padded.length - decimals);
  const frac = padded.slice(padded.length - decimals).replace(/0+$/, "");
  return frac.length ? `${whole}.${frac}` : whole;
}

/**
 * Parse a human decimal string ("1.25") into base units with the given
 * decimals. Rejects more fraction digits than `decimals` (no rounding) and
 * any non-decimal syntax.
 */
export function parseUnits(text: string, decimals: number): Amount {
  if (!Number.isInteger(decimals) || decimals < 0 || decimals > 77) throw new AmountError("invalid decimals");
  const m = /^(\d+)(?:\.(\d+))?$/.exec(text.trim());
  if (!m) throw new AmountError(`invalid decimal ${JSON.stringify(text)}`);
  const whole = m[1] ?? "0";
  const frac = m[2] ?? "";
  if (frac.length > decimals) throw new AmountError(`too many fraction digits for ${decimals} decimals`);
  const base = BigInt(whole) * 10n ** BigInt(decimals) + BigInt((frac + "0".repeat(decimals - frac.length)) || "0");
  return amount(base);
}
