/**
 * Exact decimal string / BigInt monetary utilities.
 *
 * Per plan.md, the backend serializes integer minor/base units as base-10 decimal strings.
 * TypeScript MUST NEVER silently convert base units to IEEE-754 floating-point numbers.
 */

export type AmountString = string;

const INT_RE = /^-?[0-9]+$/;

export function parseBaseUnits(value: AmountString | undefined | null): bigint {
  if (!value) return 0n;
  const trimmed = value.trim();
  if (!INT_RE.test(trimmed)) {
    throw new Error(`Invalid integer amount string: "${value}"`);
  }
  return BigInt(trimmed);
}

/**
 * Formats integer base units into a human-readable decimal string without float conversion.
 * Example: formatDecimal("14750000", 6, 2) => "14.75"
 * Example: formatDecimal("14750000", 6)    => "14.750000"
 */
export function formatDecimal(
  baseUnits: AmountString | undefined | null,
  decimals: number,
  displayDecimals?: number,
): string {
  const n = parseBaseUnits(baseUnits);
  const neg = n < 0n;
  const abs = neg ? -n : n;
  const raw = abs.toString();

  let intPart = "0";
  let fracPart = "";

  if (decimals <= 0) {
    intPart = raw;
  } else if (raw.length <= decimals) {
    intPart = "0";
    fracPart = "0".repeat(decimals - raw.length) + raw;
  } else {
    const split = raw.length - decimals;
    intPart = raw.slice(0, split);
    fracPart = raw.slice(split);
  }

  // Insert thousands separators in integer part
  const groupedInt = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");

  let out = decimals > 0 ? `${groupedInt}.${fracPart}` : groupedInt;
  if (displayDecimals !== undefined && decimals > 0) {
    if (displayDecimals <= 0) {
      out = groupedInt;
    } else if (displayDecimals < fracPart.length) {
      // Trim trailing precision only for compact display when requested; exact values still shown in detail tooltips.
      const trimmed = fracPart.slice(0, displayDecimals);
      out = `${groupedInt}.${trimmed}`;
    } else if (displayDecimals > fracPart.length) {
      out = `${groupedInt}.${fracPart}${"0".repeat(displayDecimals - fracPart.length)}`;
    }
  }
  return neg ? `-${out}` : out;
}

export function formatUSD(minorUnits: AmountString | undefined | null): string {
  const n = parseBaseUnits(minorUnits);
  const neg = n < 0n;
  const formatted = formatDecimal((neg ? -n : n).toString(), 2, 2);
  return neg ? `-$${formatted}` : `$${formatted}`;
}

export function formatUSDC(baseUnits: AmountString | undefined | null, exact = false): string {
  const formatted = formatDecimal(baseUnits, 6, exact ? 6 : 2);
  return `${formatted} USDC`;
}

/**
 * Converts a user-entered decimal string (e.g. "125.50") to exact integer base units
 * using pure string manipulation (zero floating-point math).
 */
export function parseDecimalToBaseUnits(input: string, decimals: number): AmountString {
  const cleaned = input.trim().replace(/,/g, "").replace(/^\$/, "");
  if (!/^[0-9]+(\.[0-9]+)?$/.test(cleaned)) {
    throw new Error(`Invalid decimal value: "${input}"`);
  }
  const [whole, frac = ""] = cleaned.split(".");
  if (frac.length > decimals) {
    throw new Error(`Too many decimal places (max ${decimals})`);
  }
  const paddedFrac = frac.padEnd(decimals, "0");
  const combined = `${whole}${paddedFrac}`.replace(/^0+(?=\d)/, "");
  return combined === "" ? "0" : combined;
}

/**
 * Computes integer percentage (0..100) using BigInt division for progress bars.
 */
export function ratioPercent(part: AmountString, whole: AmountString): number {
  const p = parseBaseUnits(part);
  const w = parseBaseUnits(whole);
  if (w <= 0n || p <= 0n) return 0;
  if (p >= w) return 100;
  return Number((p * 100n) / w);
}
