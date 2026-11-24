/**
 * Message catalog for the engine's machine-readable explanation components.
 * The interface renders these; it never recomputes what they describe.
 */
import type { ExclusionCode, Explanation, ExplanationCode, Rationale, Outcome } from "./generated/engine-contract";
import { formatMicroUsd } from "./money";

type Params = Record<string, string | undefined>;

const micro = (p: Params, key: string): string => (p[key] !== undefined ? formatMicroUsd(p[key]!) : "an unknown amount");
const text = (p: Params, key: string, fallback = "unknown"): string => p[key] ?? fallback;

export const exclusionMessages: Record<ExclusionCode, (p: Params) => string> = {
  method_unavailable: () => "This payment method is not available right now.",
  unsupported_jurisdiction: () => "This payment method is not supported where you are.",
  jurisdiction_unverified: () => "We could not verify that this method is supported where you are.",
  merchant_does_not_accept: () => "The merchant does not accept this payment method for this basket.",
  merchant_acceptance_unverified: () => "We could not verify that the merchant accepts this method.",
  not_consented: () => "You have not enabled this payment method.",
  rail_not_allowed: (p) => `Your settings do not allow ${text(p, "rail")} payments.`,
  purchase_protection_required: () => "You require purchase protection, and this method does not offer it.",
  credit_not_allowed: () => "You asked not to use credit.",
  stale_quote: (p) => `The quote ${text(p, "quote_id")} expired at ${text(p, "valid_until")}. Refresh to compare again.`,
  basket_mismatch: () => "The quote was issued for a different basket, so it cannot be compared.",
  incompatible_asset: (p) => `Your balance is ${text(p, "payment_asset")} but the merchant accepts ${text(p, "accepted_asset")}.`,
  rate_unavailable: (p) => `No USD value was available for ${text(p, "asset")}.`,
  funding_source_not_permitted: (p) => `Funding from ${text(p, "source").replace(/_/g, " ")} is not offered.`,
  funding_not_consented: () => "You have not enabled funding or conversion steps.",
  unknown_required_fee: (p) =>
    p.reason === "missing"
      ? `The ${text(p, "kind").replace(/_/g, " ")} fee was not quoted. An unknown fee is never treated as zero.`
      : `The ${text(p, "kind").replace(/_/g, " ")} fee amount is unknown. An unknown fee is never treated as zero.`,
  amount_out_of_range: () => "An amount was too large to compare safely.",
  funds_unverified: () => "Your available balance could not be read, so it was not assumed.",
  insufficient_funds: (p) => `Not enough spendable balance: ${text(p, "amount_due_base_units")} base units due, ${text(p, "spendable_base_units")} spendable.`,
};

export const explanationMessages: Record<ExplanationCode, (p: Params) => string> = {
  discount_already_in_quote: () => "This discount is already reflected in the quoted price, so it is not subtracted again.",
  discount_not_immediately_usable: () => "This benefit cannot reduce this payment (for example, credit on a future order), so it is not counted.",
  discount_unverified: () => "Eligibility for this discount is unverified, so it is not counted.",
  discount_unvaluable: () => "This discount's asset could not be valued in USD, so it is not counted.",
  duplicate_discount_ignored: () => "The same discount was listed twice; it is counted once.",
  discount_capped_at_price: () => "The discount exceeded the price and was capped at the price.",
  reward_unverified: () => "Eligibility for this reward is unverified, so it is valued at zero.",
  reward_cap_applied: () => "Only the remaining room under this reward's cap is counted.",
  reward_cap_exhausted: () => "This reward's cap is fully used; nothing more can be earned.",
  point_valuation_missing: (p) => `No point valuation is set for ${text(p, "program_id")}, so the points are shown but valued at zero.`,
  point_valuation_unstable: (p) => `Your valuation for ${text(p, "program_id")} is flagged unstable, so the points are valued at zero.`,
  usd_rate_used: (p) => `${text(p, "asset")} valued at ${text(p, "usd_num")}/${text(p, "usd_den")} USD per unit (quote ${text(p, "quote_id")}).`,
  point_valuation_used: (p) => `Points in ${text(p, "program_id")} valued at ${micro(p, "micro_usd_per_point")} each (valuation ${text(p, "valuation_version")}).`,
  reward_base_excludes_fees: () => "Rewards are estimated on the discounted price, not on fees.",
  amount_due_is_upper_bound: () => "The amount due in the payment asset is an upper bound; the provider sets the exact charge.",
  quote_expiry: (p) => `This comparison is valid until ${text(p, "expires_at")}.`,
  margin: (p) => `The preferred option leads the runner-up (${text(p, "runner_up_option_id")}) by ${micro(p, "margin_micro_usd")}.`,
  depends_on_estimated_rewards: (p) =>
    `This result depends on estimated rewards. On immediate costs alone the outcome would be "${text(p, "immediate_only_outcome")}"` +
    (p.immediate_only_preferred_option_id ? ` preferring ${p.immediate_only_preferred_option_id}.` : "."),
  unstable_point_valuation_excluded: (p) =>
    `${text(p, "points")} points in ${text(p, "program_id")} were not valued` +
    (p.hypothetical_micro_usd ? `; at your stated valuation they would be worth ${micro(p, "hypothetical_micro_usd")}.` : "."),
  savings_below_threshold: (p) => `The estimated saving of ${micro(p, "estimated_total_micro_usd")} is below your ${micro(p, "threshold_micro_usd")} threshold, so your default is kept.`,
  excluded_option_could_change_result: (p) => `An option was excluded for missing data (${text(p, "exclusion_codes").replace(/,/g, ", ")}); better data could change the result.`,
};

export const outcomeTitles: Record<Outcome, string> = {
  preferred: "Recommended",
  tie: "No price difference",
  insufficient_data: "Not enough data to compare",
  no_eligible_option: "No eligible payment method",
};

export const rationaleMessages: Record<Rationale, string> = {
  default_is_lowest_cost: "Your default method has the lowest estimated net cost.",
  savings_meet_threshold: "Another method saves at least your threshold against your default.",
  savings_below_threshold: "A cheaper method exists, but the saving is below your threshold, so your default is kept.",
  lowest_cost_no_default: "You have no default method; the lowest estimated net cost is preferred.",
  only_eligible_option: "Only one method is eligible for this basket.",
  equal_net_cost: "The eligible methods have exactly the same estimated net cost.",
  options_missing_data: "At least one method could not be compared because required data is missing or stale.",
  no_eligible_option: "No method is available, consented and fundable for this basket.",
};

/** Renders one explanation component. Unknown codes fall back to the code itself. */
export function explain(e: Explanation): string {
  const render = explanationMessages[e.code];
  return render ? render(e.params) : e.code;
}

/** Renders an exclusion. Unknown codes fall back to the code itself. */
export function explainExclusion(code: ExclusionCode, params: Params): string {
  const render = exclusionMessages[code];
  return render ? render(params) : code;
}
