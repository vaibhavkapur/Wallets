import { describe, expect, it } from "vitest";
import { addAmounts, amount, compareAmounts, formatUnits, isAmount, parseUnits, subAmounts, MAX_BASE_UNITS } from "./amounts.js";

describe("amount", () => {
  it("accepts canonical strings and bigints", () => {
    expect(amount("0")).toBe("0");
    expect(amount("1000000")).toBe("1000000");
    expect(amount(123n)).toBe("123");
    expect(amount(MAX_BASE_UNITS)).toBe("340282366920938463463374607431768211455");
  });

  it("rejects numbers, floats, signs, leading zeros, overflow", () => {
    // @ts-expect-error numbers are not allowed by the type system either
    expect(() => amount(1)).toThrow();
    for (const bad of ["", "-1", "+1", "01", "1.5", "1e6", " 1", "0x10", "340282366920938463463374607431768211456"]) {
      expect(() => amount(bad), bad).toThrow();
      expect(isAmount(bad)).toBe(false);
    }
    expect(() => amount(-1n)).toThrow();
  });

  it("does exact arithmetic beyond Number precision", () => {
    const big = amount("9007199254740993"); // 2^53 + 1
    expect(addAmounts(big, amount("1"))).toBe("9007199254740994");
    expect(subAmounts(big, amount("1"))).toBe("9007199254740992");
    expect(() => subAmounts(amount("1"), amount("2"))).toThrow();
    expect(compareAmounts(amount("2"), amount("10"))).toBe(-1);
  });

  it("formats and parses units without floating point", () => {
    expect(formatUnits(amount("1000000"), 6)).toBe("1");
    expect(formatUnits(amount("1250000"), 6)).toBe("1.25");
    expect(formatUnits(amount("1"), 6)).toBe("0.000001");
    expect(formatUnits(amount("0"), 6)).toBe("0");
    expect(formatUnits(amount("123456789012345678901234567890"), 18)).toBe("123456789012.34567890123456789");
    expect(parseUnits("1.25", 6)).toBe("1250000");
    expect(parseUnits("0.000001", 6)).toBe("1");
    expect(parseUnits("5", 6)).toBe("5000000");
    expect(() => parseUnits("1.2345678", 6)).toThrow(); // no silent rounding
    expect(() => parseUnits("1e3", 6)).toThrow();
    expect(() => parseUnits("-1", 6)).toThrow();
  });
});
