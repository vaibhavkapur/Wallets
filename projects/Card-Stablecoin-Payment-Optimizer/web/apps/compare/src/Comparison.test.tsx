import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Evaluation } from "@unowallet/optimizer-sdk";
import { Comparison } from "./components/Comparison";
import { PreferencesForm, toBaseUnits } from "./components/PreferencesForm";
import planExample from "./test/fixtures/001-plan-example.json";
import belowThreshold from "./test/fixtures/003-saving-below-threshold-keeps-default.json";
import staleQuote from "./test/fixtures/010-stale-quote.json";
import fundingStep from "./test/fixtures/032-funding-step-quoted.json";

const asEvaluation = (fixture: unknown) => fixture as Evaluation;

describe("Comparison renders the engine's totals without recomputing them", () => {
  it("shows the plan example: card $97.00 beats stablecoin $97.50 because of estimated rewards", () => {
    render(<Comparison evaluation={asEvaluation(planExample)} />);
    expect(screen.getByRole("status")).toHaveTextContent("Recommended: Rewards credit card");
    expect(screen.getByTestId("net-card")).toHaveTextContent("$97.00");
    expect(screen.getByTestId("net-usdc")).toHaveTextContent("$97.50");
    expect(screen.getByTestId("immediate-card")).toHaveTextContent("$100.00");
    expect(screen.getByTestId("immediate-usdc")).toHaveTextContent("$97.50");
    expect(screen.getByTestId("due-usdc")).toHaveTextContent("97.50 USDC (base)");
    expect(screen.getByTestId("due-card")).toHaveTextContent("$100.00");
    const card = within(screen.getByTestId("option-card"));
    expect(card.getByText("3% cash back")).toBeInTheDocument();
    expect(card.getAllByText("estimated reward").length).toBeGreaterThan(0);
    expect(screen.getByTestId("sensitivities")).toHaveTextContent("depends on estimated rewards");
    expect(screen.getByTestId("sensitivities")).toHaveTextContent("leads the runner-up (usdc) by $0.50");
    expect(screen.queryByTestId("savings")).not.toBeInTheDocument();
    expect(screen.getByText(/Recommendation only/)).toBeInTheDocument();
  });

  it("separates immediate savings from estimated rewards and shows the threshold", () => {
    render(<Comparison evaluation={asEvaluation(belowThreshold)} />);
    expect(screen.getByRole("status")).toHaveTextContent("Recommended: Rewards credit card");
    expect(screen.getByRole("status")).toHaveTextContent("below your threshold");
    const savings = within(screen.getByTestId("savings"));
    expect(savings.getByTestId("savings-immediate")).toHaveTextContent("+$4.50");
    expect(savings.getByTestId("savings-total")).toHaveTextContent("+$1.50");
    expect(savings.getByText(/\$2\.00 \(not met\)/)).toBeInTheDocument();
  });

  it("explains an excluded stale quote and reports insufficient data", () => {
    render(<Comparison evaluation={asEvaluation(staleQuote)} />);
    expect(screen.getByRole("status")).toHaveTextContent("Not enough data to compare");
    const exclusions = screen.getByTestId("exclusions-usdc");
    expect(exclusions).toHaveTextContent("expired at 2026-10-02T12:00:00Z");
    expect(exclusions).toHaveTextContent("missing data");
    expect(screen.queryByTestId("net-usdc")).not.toBeInTheDocument();
  });

  it("shows a funding step as a separate quoted step and lets the customer approve the exact amount due", () => {
    const onApprove = vi.fn();
    render(<Comparison evaluation={asEvaluation(fundingStep)} onApprove={onApprove} />);
    expect(screen.getByTestId("option-usdc")).toHaveTextContent("Separate funding step: add 60.00 USDC (base) from bank transfer (fees $0.35");
    expect(screen.getByTestId("immediate-usdc")).toHaveTextContent("$97.85");
    expect(screen.getByTestId("due-usdc")).toHaveTextContent("97.50 USDC (base)");
    fireEvent.click(screen.getByTestId("approve-usdc"));
    expect(onApprove).toHaveBeenCalledTimes(1);
    expect(onApprove.mock.calls[0]![0].breakdown.amount_due.base_units).toBe("97500000");
  });

  it("does not offer approval once the evaluation has expired", () => {
    const expired = { ...asEvaluation(planExample), status: "expired" as const };
    render(<Comparison evaluation={expired} onApprove={vi.fn()} />);
    expect(screen.queryByTestId("approve-card")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("expired");
  });
});

describe("PreferencesForm", () => {
  it("converts dollars to minor units without floating point", () => {
    expect(toBaseUnits("1.00", 2)).toBe("100");
    expect(toBaseUnits("0.1", 2)).toBe("10");
    expect(toBaseUnits("0", 2)).toBe("0");
    expect(toBaseUnits("12.345678", 6)).toBe("12345678");
    expect(toBaseUnits("1.234", 2)).toBeNull();
    expect(toBaseUnits("abc", 2)).toBeNull();
  });

  it("saves consent, threshold and risk constraints as the API expects", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <PreferencesForm
        value={{
          default_option_id: "card",
          min_savings_threshold_minor: "100",
          consented_rails: ["card"],
          consent_funding_step: false,
          allowed_rails: null,
          require_purchase_protection: false,
          avoid_credit: false,
          liquidity_reserves: [],
          point_valuations: [],
        }}
        isDefault
        version={0}
        optionIds={["card", "usdc"]}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByLabelText(/Minimum estimated saving/), { target: { value: "2.50" } });
    fireEvent.click(screen.getByLabelText(/Stablecoin \(USDC on Base\)/));
    fireEvent.click(screen.getByLabelText(/Require purchase protection/));
    fireEvent.change(screen.getByLabelText(/liquidity reserve/), { target: { value: "25" } });
    fireEvent.click(screen.getByText("Save preferences"));
    await vi.waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    const saved = onSave.mock.calls[0]![0];
    expect(saved.min_savings_threshold_minor).toBe("250");
    expect(saved.consented_rails).toEqual(["card", "stablecoin"]);
    expect(saved.require_purchase_protection).toBe(true);
    expect(saved.liquidity_reserves).toEqual([{ asset: { code: "USDC", network: "base", decimals: 6 }, base_units: "25000000" }]);
    expect(saved.point_valuations[0]).toMatchObject({ program_id: "pts_demo", micro_usd_per_point: "10000", stability: "stable" });
  });
});
