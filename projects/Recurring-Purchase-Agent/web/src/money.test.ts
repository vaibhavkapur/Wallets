import { describe, expect, it } from "vitest";
import { budgetPercent, describeSchedule, formatMoney, parseUSDToMoney } from "./money";

describe("BigInt-safe money and schedule helpers", () => {
  it("formats USD cents and large >2^53 integers without float precision loss", () => {
    expect(
      formatMoney({ amount_minor: "2499", asset: "USD", network: "fiat", decimals: 2 }),
    ).toBe("$24.99");

    // 9007199254740995 > Number.MAX_SAFE_INTEGER (9007199254740991)
    expect(
      formatMoney({
        amount_minor: "9007199254740995",
        asset: "USDC",
        network: "base",
        decimals: 6,
      }),
    ).toBe("9007199254.740995 USDC");
  });

  it("parses dollar strings to exact decimal minor units", () => {
    expect(parseUSDToMoney("$34.95")).toEqual({
      amount_minor: "3495",
      asset: "USD",
      network: "fiat",
      decimals: 2,
    });
    expect(parseUSDToMoney("60")).toEqual({
      amount_minor: "6000",
      asset: "USD",
      network: "fiat",
      decimals: 2,
    });
    expect(parseUSDToMoney("12.345")).toBeNull();
    expect(parseUSDToMoney("-5")).toBeNull();
  });

  it("computes budget usage percentage accurately", () => {
    expect(
      budgetPercent(
        { amount_minor: "2500", asset: "USD", network: "fiat", decimals: 2 },
        { amount_minor: "10000", asset: "USD", network: "fiat", decimals: 2 },
      ),
    ).toBe(25);
  });

  it("describes calendar vs interval schedules distinctly", () => {
    expect(
      describeSchedule({
        kind: "interval",
        timezone: "America/New_York",
        dst_policy: "shift_forward_first",
        leap_day_policy: "clamp_month_end",
        local_hour: 9,
        local_minute: 30,
        interval_days: 14,
        min_interval_days: 10,
        proposal_ttl_minutes: 1440,
      }),
    ).toBe("Every 14 days at 09:30 (America/New_York)");

    expect(
      describeSchedule({
        kind: "calendar",
        calendar_cadence: "monthly",
        day_of_month: 29,
        timezone: "America/Los_Angeles",
        dst_policy: "shift_forward_second",
        leap_day_policy: "clamp_month_end",
        local_hour: 8,
        local_minute: 0,
        min_interval_days: 20,
        proposal_ttl_minutes: 1440,
      }),
    ).toBe("Calendar: Monthly on day 29 at 08:00 (America/Los_Angeles)");
  });
});
