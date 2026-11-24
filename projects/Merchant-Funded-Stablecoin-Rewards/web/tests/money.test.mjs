import assert from "node:assert/strict";
import test from "node:test";
import {
  formatDecimal,
  formatUSD,
  formatUSDC,
  parseBaseUnits,
  parseDecimalToBaseUnits,
  ratioPercent,
} from "../.test-dist/money.js";

test("preserves exact precision beyond IEEE-754 MAX_SAFE_INTEGER", () => {
  const huge = "9007199254740993123456"; // > 2^53 - 1
  assert.equal(parseBaseUnits(huge), 9007199254740993123456n);
  assert.equal(formatDecimal(huge, 6), "9,007,199,254,740,993.123456");
});

test("formats USD and USDC amounts deterministically without floats", () => {
  assert.equal(formatUSD("1475"), "$14.75");
  assert.equal(formatUSD("-500"), "-$5.00");
  assert.equal(formatUSDC("14750000", true), "14.750000 USDC");
  assert.equal(formatUSDC("250000", true), "0.250000 USDC");
});

test("parses decimal strings to exact integer base units", () => {
  assert.equal(parseDecimalToBaseUnits("125.50", 2), "12550");
  assert.equal(parseDecimalToBaseUnits("$1,250.05", 2), "125005");
  assert.equal(parseDecimalToBaseUnits("0.250000", 6), "250000");
  assert.throws(() => parseDecimalToBaseUnits("1.234", 2));
});

test("computes BigInt ratio percentages safely", () => {
  assert.equal(ratioPercent("2500", "10000"), 25);
  assert.equal(ratioPercent("0", "10000"), 0);
  assert.equal(ratioPercent("15000", "10000"), 100);
});
