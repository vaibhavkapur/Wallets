import { describe, expect, it } from "vitest";
import { compareUnits, formatAmount, formatDecimal, formatMicroUsd, isZero } from "./money";

describe("formatMicroUsd", () => {
  it("shows cents and keeps sub-cent precision", () => {
    expect(formatMicroUsd("97500000")).toBe("$97.50");
    expect(formatMicroUsd("97000000")).toBe("$97.00");
    expect(formatMicroUsd("977625")).toBe("$0.977625");
    expect(formatMicroUsd("12375")).toBe("$0.012375");
    expect(formatMicroUsd("0")).toBe("$0.00");
    expect(formatMicroUsd("1")).toBe("$0.000001");
  });
  it("handles signs and thousands", () => {
    expect(formatMicroUsd("-500000")).toBe("-$0.50");
    expect(formatMicroUsd("1081200", { explicitSign: true })).toBe("+$1.0812");
    expect(formatMicroUsd("1234567890000")).toBe("$1,234,567.89");
  });
  it("never goes through a floating-point number", () => {
    // 2^53 + 1 micro-USD is not representable as a double.
    expect(formatMicroUsd("9007199254740993")).toBe("$9,007,199,254.740993");
    expect(formatMicroUsd("170141183460469231731687303715884105727")).toBe("$170,141,183,460,469,231,731,687,303,715,884.105727");
  });
  it("rejects non-canonical input", () => {
    for (const bad of ["", "01", "1.5", "1e3", "+1", "-0", " 1"]) {
      expect(() => formatMicroUsd(bad)).toThrow();
    }
  });
});

describe("formatDecimal / formatAmount", () => {
  it("formats base units of any asset", () => {
    expect(formatDecimal("134590000", 6)).toBe("134.59");
    expect(formatDecimal("97500000000000000000", 18)).toBe("97.50");
    expect(formatDecimal("5", 0)).toBe("5");
    expect(formatDecimal("13844", 2)).toBe("138.44");
    expect(formatAmount({ asset: { code: "USD", decimals: 2 }, base_units: "10250" })).toBe("$102.50");
    expect(formatAmount({ asset: { code: "USDC", network: "base", decimals: 6 }, base_units: "97264577" })).toBe("97.264577 USDC (base)");
  });
});

describe("compareUnits", () => {
  it("orders by value, not by string", () => {
    expect(compareUnits("9", "10")).toBe(-1);
    expect(compareUnits("10", "9")).toBe(1);
    expect(compareUnits("-10", "-9")).toBe(-1);
    expect(compareUnits("-1", "1")).toBe(-1);
    expect(compareUnits("5", "5")).toBe(0);
    expect(isZero("0")).toBe(true);
  });
});
