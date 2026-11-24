import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { exclusionMessages, explain, explainExclusion, explanationMessages, outcomeTitles, rationaleMessages } from "./explain";

const schema = JSON.parse(readFileSync(new URL("../../../../contracts/engine-v1.schema.json", import.meta.url), "utf8"));
const enumOf = (name: string): string[] => schema.$defs[name].enum;

describe("explanation catalog", () => {
  it("covers every code the engine can emit", () => {
    for (const code of enumOf("ExclusionCode")) expect(exclusionMessages).toHaveProperty(code);
    for (const code of enumOf("ExplanationCode")) expect(explanationMessages).toHaveProperty(code);
    for (const code of enumOf("Outcome")) expect(outcomeTitles).toHaveProperty(code);
    for (const code of enumOf("Rationale")) expect(rationaleMessages).toHaveProperty(code);
  });
  it("renders parameters exactly as the engine supplied them", () => {
    expect(
      explain({ code: "margin", option_id: "card", params: { runner_up_option_id: "usdc", margin_micro_usd: "500000" } }),
    ).toBe("The preferred option leads the runner-up (usdc) by $0.50.");
    expect(
      explain({ code: "savings_below_threshold", option_id: "usdc", params: { estimated_total_micro_usd: "710000", threshold_micro_usd: "1000000" } }),
    ).toContain("$0.71 is below your $1.00 threshold");
    expect(explainExclusion("unknown_required_fee", { kind: "network", reason: "missing" })).toContain("network fee was not quoted");
    expect(explainExclusion("stale_quote", { quote_id: "q1", valid_until: "2026-10-02T12:05:00Z" })).toContain("q1");
  });
});
