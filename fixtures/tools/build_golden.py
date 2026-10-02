#!/usr/bin/env python3
"""Writes the golden evaluation vectors in fixtures/golden/v1.

Each vector is a full contract-v1 request plus the expected result. Requests are
assembled from one base scenario so that every case differs from it in exactly
the way its description says. The expected values below were worked out by hand
from docs/calculation.md; they are not produced by the Rust engine.
`independent_check.py` recomputes them with exact rational arithmetic, and the
engine's own tests must reproduce them.

All scenarios are synthetic and illustrative. None is a provider quote.
"""

import copy
import json
import pathlib

OUT = pathlib.Path(__file__).resolve().parent.parent / "golden" / "v1"

USD = {"code": "USD", "decimals": 2}
USDC = {"code": "USDC", "network": "base", "decimals": 6}
NOW = "2026-10-02T12:00:00Z"
HASH = "sha256:2f1c0f6f6f3a4f0f8a9f1c7f2f0b1f7a3d5c9e1b4a6d8f0c2e4a6b8d0f1e3c5a"


def usd(cents):
    return {"asset": USD, "base_units": str(cents)}


def usdc(units):
    return {"asset": USDC, "base_units": str(units)}


def base():
    return {
        "contract_version": "1",
        "evaluated_at": NOW,
        "basket": {"basket_id": "bsk_plan_example", "merchant_id": "mer_demo_outfitter", "basket_hash": HASH},
        "policy": {
            "policy_version": "policy-2026-10-02.1",
            "comparison_currency": "USD",
            "supported_stablecoins": [USDC],
            "required_fee_kinds": {
                "card": ["payment_surcharge", "fx"],
                "stablecoin": ["network"],
                "with_funding_step": ["funding", "conversion"],
            },
            "permitted_funding_sources": ["bank_transfer", "debit_card", "fiat_balance"],
        },
        "preferences": {
            "preference_version": "pref-v1",
            "default_option_id": "card",
            "min_savings_threshold": usd(0),
            "hard_constraints": {"allowed_rails": None, "require_purchase_protection": False, "avoid_credit": False},
            "point_valuations": [],
        },
        "options": [
            {
                "option_id": "card",
                "rail": "card",
                "label": "Rewards credit card",
                "payment_asset": USD,
                "quote": {
                    "quote_id": "q_card_1",
                    "provider": "recorded",
                    "basket_hash": HASH,
                    "delivered_price": usd(10000),
                    "accepted_asset": USD,
                    "usd_rate": None,
                    "obtained_at": "2026-10-02T11:59:00Z",
                    "valid_until": "2026-10-02T12:10:00Z",
                },
                "availability": {
                    "method_available": True,
                    "jurisdiction": "verified",
                    "merchant_acceptance": "verified",
                    "customer_consent": True,
                },
                "fees": [
                    {"fee_id": "card_surcharge", "kind": "payment_surcharge", "status": "not_applicable", "amount": None, "description": "No card surcharge"},
                    {"fee_id": "card_fx", "kind": "fx", "status": "not_applicable", "amount": None, "description": "Charged in USD"},
                ],
                "discounts": [],
                "rewards": [
                    {
                        "rule_id": "card_cashback",
                        "rule_version": "2026-09",
                        "eligibility": "verified",
                        "earn": {"type": "cashback", "rate_bps": "300"},
                        "cap": None,
                        "description": "3% cash back",
                    }
                ],
                "funds": {"status": "known", "available": usd(500000), "reserve": None},
                "funding_step": None,
                "tradeoffs": {
                    "purchase_protection": "yes",
                    "uses_credit": True,
                    "refund_asset": "USD to the card",
                    "refund_timing": "5-10 business days",
                    "settlement": "Chargeback rights apply",
                    "notes": [],
                },
            },
            {
                "option_id": "usdc",
                "rail": "stablecoin",
                "label": "USDC on Base",
                "payment_asset": USDC,
                "quote": {
                    "quote_id": "q_usdc_1",
                    "provider": "recorded",
                    "basket_hash": HASH,
                    "delivered_price": usd(10000),
                    "accepted_asset": USDC,
                    "usd_rate": {"usd_num": "1", "usd_den": "1"},
                    "obtained_at": "2026-10-02T11:59:30Z",
                    "valid_until": "2026-10-02T12:05:00Z",
                },
                "availability": {
                    "method_available": True,
                    "jurisdiction": "verified",
                    "merchant_acceptance": "verified",
                    "customer_consent": True,
                },
                "fees": [
                    {"fee_id": "usdc_network", "kind": "network", "status": "known", "amount": usdc(1500000), "description": "Network and processing"},
                ],
                "discounts": [
                    {
                        "discount_id": "usdc_pay_discount",
                        "amount": usd(400),
                        "included_in_quote": False,
                        "immediately_usable": True,
                        "eligibility": "verified",
                        "description": "Stablecoin payment discount",
                    }
                ],
                "rewards": [],
                "funds": {"status": "known", "available": usdc(250000000), "reserve": None},
                "funding_step": None,
                "tradeoffs": {
                    "purchase_protection": "no",
                    "uses_credit": False,
                    "refund_asset": "USDC on Base",
                    "refund_timing": "Merchant-initiated; timing varies",
                    "settlement": "Final once confirmed",
                    "notes": [],
                },
            },
        ],
    }


def elig(immediate, rewards, net, due):
    return {
        "status": "eligible",
        "exclusion_codes": [],
        "immediate_cost_micro_usd": str(immediate),
        "estimated_rewards_micro_usd": str(rewards),
        "estimated_net_cost_micro_usd": str(net),
        "amount_due_base_units": str(due),
    }


def excl(*codes, amounts=None):
    out = {"status": "excluded", "exclusion_codes": sorted(codes)}
    if amounts:
        immediate, rewards, net, due = amounts
        out.update(
            immediate_cost_micro_usd=str(immediate),
            estimated_rewards_micro_usd=str(rewards),
            estimated_net_cost_micro_usd=str(net),
            amount_due_base_units=str(due),
        )
    return out


CARD = elig(100_000_000, 3_000_000, 97_000_000, 10000)
USDC_OPT = elig(97_500_000, 0, 97_500_000, 97_500_000)
USDC_AMOUNTS = (97_500_000, 0, 97_500_000, 97_500_000)

CASES = []


def case(name, description, expected=None, expected_error=None):
    def register(mutate):
        req = base()
        mutate(req)
        entry = {"name": name, "description": description, "synthetic": True, "request": req}
        if expected_error:
            entry["expected_error"] = {"code": expected_error}
        else:
            entry["expected"] = expected
        CASES.append(entry)
        return mutate

    return register


def result(outcome, rationale, preferred, lowest, card, usdc_opt, tied=(), sensitivities=(), savings=None, expires="2026-10-02T12:05:00Z"):
    return {
        "outcome": outcome,
        "rationale": rationale,
        "preferred_option_id": preferred,
        "tied_option_ids": list(tied),
        "lowest_cost_option_ids": list(lowest),
        "savings_estimated_total_micro_usd": None if savings is None else str(savings),
        "expires_at": expires,
        "sensitivity_codes": sorted(sensitivities),
        "options": {"card": card, "usdc": usdc_opt},
    }


CARD_ONLY_EXPIRY = "2026-10-02T12:10:00Z"


def card_opt(req):
    return req["options"][0]


def usdc_opt(req):
    return req["options"][1]


# --- Decision rule -----------------------------------------------------------


@case(
    "001-plan-example",
    "The plan's illustration: $100 card purchase with $3 rewards ($97.00) against a $4 stablecoin "
    "discount with $1.50 of costs ($97.50). The card wins by $0.50, but only because of estimated rewards.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD, USDC_OPT,
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    pass


@case(
    "002-stablecoin-saving-meets-threshold",
    "A $6 stablecoin discount gives $95.50 against the card's $97.00. The $1.50 saving exceeds the $1.00 threshold.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"], CARD,
           elig(95_500_000, 0, 95_500_000, 95_500_000), savings=1_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(600)
    req["preferences"]["min_savings_threshold"] = usd(100)


@case(
    "003-saving-below-threshold-keeps-default",
    "Same $1.50 saving, but the customer asks for at least $2.00 before switching. The default card is kept.",
    result("preferred", "savings_below_threshold", "card", ["usdc"], CARD,
           elig(95_500_000, 0, 95_500_000, 95_500_000), savings=1_500_000,
           sensitivities=["quote_expiry", "margin", "savings_below_threshold", "depends_on_estimated_rewards"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(600)
    req["preferences"]["min_savings_threshold"] = usd(200)


@case(
    "004-saving-equal-to-threshold-switches",
    "The $1.50 saving equals the $1.50 threshold exactly. The threshold is inclusive.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"], CARD,
           elig(95_500_000, 0, 95_500_000, 95_500_000), savings=1_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(600)
    req["preferences"]["min_savings_threshold"] = usd(150)


@case(
    "005-exact-tie",
    "A $4.50 discount makes both methods cost exactly $97.00. The result is a tie, not an arbitrary pick.",
    result("tie", "equal_net_cost", None, ["card", "usdc"], CARD,
           elig(97_000_000, 0, 97_000_000, 97_000_000), tied=["card", "usdc"],
           sensitivities=["quote_expiry", "depends_on_estimated_rewards"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(450)


@case(
    "006-no-default-picks-lowest",
    "With no stated default there is no baseline and no threshold test; the unique lowest cost is preferred.",
    result("preferred", "lowest_cost_no_default", "card", ["card"], CARD, USDC_OPT,
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    req["preferences"]["default_option_id"] = None
    req["preferences"]["min_savings_threshold"] = usd(5000)


# --- Quotes, eligibility and consent ----------------------------------------


@case(
    "010-stale-quote",
    "The stablecoin quote expired exactly at the evaluation time. Expiry is exclusive, so the option is "
    "excluded and the evaluation reports insufficient data instead of recommending the card by default.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("stale_quote", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["quote"]["valid_until"] = NOW


@case(
    "011-basket-mismatch",
    "The stablecoin quote was issued for a different basket, so the two prices are not comparable.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("basket_mismatch", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["quote"]["basket_hash"] = "sha256:" + "0" * 64


@case(
    "012-merchant-acceptance-unverified",
    "Stablecoin acceptance at the merchant has not been verified. Unverified eligibility is not assumed.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("merchant_acceptance_unverified", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["availability"]["merchant_acceptance"] = "unverified"


@case(
    "013-unsupported-jurisdiction",
    "Stablecoin payment is not supported in the customer's jurisdiction. That is a definite exclusion, "
    "so the card is the only eligible option.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("unsupported_jurisdiction", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["availability"]["jurisdiction"] = "rejected"


@case(
    "014-not-consented",
    "The customer has not consented to stablecoin payments.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("not_consented", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["availability"]["customer_consent"] = False


@case(
    "015-incompatible-asset",
    "The customer's USDC is on Ethereum; the merchant accepts USDC on Base only.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("incompatible_asset", "unknown_required_fee"),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    eth_usdc = {"code": "USDC", "network": "ethereum", "decimals": 6}
    opt = usdc_opt(req)
    opt["payment_asset"] = eth_usdc
    opt["funds"]["available"] = {"asset": eth_usdc, "base_units": "250000000"}


@case(
    "016-no-eligible-option",
    "The card is unavailable and stablecoin is outside the customer's consent. Nothing can be recommended.",
    result("no_eligible_option", "no_eligible_option", None, [],
           excl("method_unavailable", amounts=(100_000_000, 3_000_000, 97_000_000, 10000)),
           excl("not_consented", amounts=USDC_AMOUNTS), expires=None),
)
def _(req):
    card_opt(req)["availability"]["method_available"] = False
    usdc_opt(req)["availability"]["customer_consent"] = False


# --- Customer hard constraints ----------------------------------------------


@case(
    "020-purchase-protection-required",
    "The customer requires purchase protection. The stablecoin option offers none and is excluded "
    "as a constraint, not priced as a penalty.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("purchase_protection_required", amounts=(95_500_000, 0, 95_500_000, 95_500_000)),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(600)
    req["preferences"]["hard_constraints"]["require_purchase_protection"] = True


@case(
    "021-avoid-credit",
    "The customer does not want to use credit. The default credit card is excluded, leaving stablecoin.",
    result("preferred", "only_eligible_option", "usdc", ["usdc"],
           excl("credit_not_allowed", amounts=(100_000_000, 3_000_000, 97_000_000, 10000)), USDC_OPT,
           sensitivities=["quote_expiry"]),
)
def _(req):
    req["preferences"]["hard_constraints"]["avoid_credit"] = True


@case(
    "022-rail-not-allowed",
    "The customer allows card payments only.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("rail_not_allowed", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    req["preferences"]["hard_constraints"]["allowed_rails"] = ["card"]


# --- Fees --------------------------------------------------------------------


@case(
    "030-unknown-network-fee",
    "The network fee is listed but its amount is unknown. It is not treated as zero.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("unknown_required_fee"),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    fee = usdc_opt(req)["fees"][0]
    fee["status"] = "unknown"
    fee["amount"] = None


@case(
    "031-missing-conversion-fee",
    "The stablecoin option needs a funding step. The funding fee is quoted but no conversion fee was "
    "supplied, so the option cannot be costed.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("unknown_required_fee"),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    opt = usdc_opt(req)
    opt["funds"]["available"] = usdc(50_000_000)
    opt["funding_step"] = {
        "source": "bank_transfer",
        "amount": usdc(60_000_000),
        "quote_id": "q_fund_1",
        "valid_until": "2026-10-02T12:04:00Z",
        "customer_consent": True,
    }
    opt["fees"].append({"fee_id": "usdc_funding", "kind": "funding", "status": "known", "amount": usd(25), "description": "Bank transfer fee"})


@case(
    "032-funding-step-quoted",
    "50 USDC on hand plus a consented, quoted 60 USDC top-up. Funding ($0.25) and conversion ($0.10) fees "
    "are added: 100 - 4 + 1.50 + 0.25 + 0.10 = $97.85. The amount charged to the wallet stays 97.50 USDC. "
    "The funding quote expires first.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD,
           elig(97_850_000, 0, 97_850_000, 97_500_000),
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"], expires="2026-10-02T12:04:00Z"),
)
def _(req):
    opt = usdc_opt(req)
    opt["funds"]["available"] = usdc(50_000_000)
    opt["funding_step"] = {
        "source": "bank_transfer",
        "amount": usdc(60_000_000),
        "quote_id": "q_fund_1",
        "valid_until": "2026-10-02T12:04:00Z",
        "customer_consent": True,
    }
    opt["fees"].append({"fee_id": "usdc_funding", "kind": "funding", "status": "known", "amount": usd(25), "description": "Bank transfer fee"})
    opt["fees"].append({"fee_id": "usdc_conversion", "kind": "conversion", "status": "known", "amount": usd(10), "description": "USD to USDC conversion"})


@case(
    "033-credit-card-funding-not-permitted",
    "Funding the stablecoin purchase with a credit card is not offered in the MVP.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("funding_source_not_permitted", amounts=(97_850_000, 0, 97_850_000, 97_500_000)),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    opt = usdc_opt(req)
    opt["funds"]["available"] = usdc(50_000_000)
    opt["funding_step"] = {
        "source": "credit_card",
        "amount": usdc(60_000_000),
        "quote_id": "q_fund_cc",
        "valid_until": "2026-10-02T12:04:00Z",
        "customer_consent": True,
    }
    opt["fees"].append({"fee_id": "usdc_funding", "kind": "funding", "status": "known", "amount": usd(25), "description": "Card funding fee"})
    opt["fees"].append({"fee_id": "usdc_conversion", "kind": "conversion", "status": "known", "amount": usd(10), "description": "USD to USDC conversion"})


@case(
    "034-card-fx-fee",
    "The card charges a $2.50 foreign transaction fee. Rewards are earned on the $100 price, not the fee: "
    "100 + 2.50 - 3 = $99.50, so stablecoin at $97.50 saves $2.00.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(102_500_000, 3_000_000, 99_500_000, 10250), USDC_OPT, savings=2_000_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    fee = card_opt(req)["fees"][1]
    fee["status"] = "known"
    fee["amount"] = usd(250)


@case(
    "035-fee-in-unvaluable-asset",
    "The network fee is quoted in ETH with no USD value supplied. A fee that cannot be valued is unknown.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("unknown_required_fee"),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["fees"][0]["amount"] = {"asset": {"code": "ETH", "network": "base", "decimals": 18}, "base_units": "400000000000000"}


# --- Funds -------------------------------------------------------------------


@case(
    "040-insufficient-funds",
    "Only 50 USDC is spendable against 97.50 USDC due and no funding step was requested.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("insufficient_funds", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["funds"]["available"] = usdc(50_000_000)


@case(
    "041-liquidity-reserve",
    "100 USDC is available but the customer keeps 10 USDC in reserve, leaving 90 USDC for a 97.50 USDC payment.",
    result("preferred", "only_eligible_option", "card", ["card"], CARD,
           excl("insufficient_funds", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["funds"]["available"] = usdc(100_000_000)
    usdc_opt(req)["funds"]["reserve"] = usdc(10_000_000)


@case(
    "042-funds-exactly-sufficient",
    "Exactly 97.50 USDC is spendable. The boundary is inclusive.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD, USDC_OPT,
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    usdc_opt(req)["funds"]["available"] = usdc(97_500_000)


@case(
    "043-funds-unverified",
    "The wallet balance could not be read. A cache or a guess never stands in for a balance.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("funds_unverified", amounts=USDC_AMOUNTS),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["funds"] = {"status": "unknown", "available": None, "reserve": None}


# --- Discounts ---------------------------------------------------------------


@case(
    "050-duplicate-discount-counted-once",
    "The same discount appears twice in the input. It is subtracted once.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD, USDC_OPT,
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    opt = usdc_opt(req)
    opt["discounts"].append(copy.deepcopy(opt["discounts"][0]))


@case(
    "051-discount-already-in-quote",
    "The merchant's stablecoin quote of $96.00 already reflects the $4 discount. It is not subtracted again.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD, USDC_OPT,
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    opt = usdc_opt(req)
    opt["quote"]["delivered_price"] = usd(9600)
    opt["discounts"][0]["included_in_quote"] = True


@case(
    "052-discount-exceeds-price",
    "A $120 discount on a $100 basket is capped at the price: only the $1.50 fee remains.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"], CARD,
           elig(1_500_000, 0, 1_500_000, 1_500_000), savings=95_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["amount"] = usd(12000)


@case(
    "053-hundred-percent-discount-earns-no-rewards",
    "A 100% card discount leaves nothing to earn rewards on: the card costs $0.00.",
    result("preferred", "default_is_lowest_cost", "card", ["card"],
           elig(0, 0, 0, 0), USDC_OPT, sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["discounts"].append({
        "discount_id": "card_full_discount",
        "amount": usd(10000),
        "included_in_quote": False,
        "immediately_usable": True,
        "eligibility": "verified",
        "description": "Gift promotion",
    })


@case(
    "054-discount-not-immediately-usable",
    "The stablecoin benefit is store credit on a future order. It does not reduce this payment and is not valued.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD,
           elig(101_500_000, 0, 101_500_000, 101_500_000), sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["immediately_usable"] = False


@case(
    "055-discount-unverified",
    "Eligibility for the stablecoin discount is unverified, so it is left out.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD,
           elig(101_500_000, 0, 101_500_000, 101_500_000), sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    usdc_opt(req)["discounts"][0]["eligibility"] = "unverified"


# --- Rewards -----------------------------------------------------------------


@case(
    "060-reward-cap-partially-consumed",
    "The card's cash back cap is $50.00 with $49.00 used. Only $1.00 of the $3.00 can be earned: $99.00.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 1_000_000, 99_000_000, 10000), USDC_OPT, savings=1_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["rewards"][0]["cap"] = {"limit": "5000", "consumed": "4900"}


@case(
    "061-reward-cap-already-consumed",
    "The cash back cap is fully used. The card earns nothing and costs $100.00.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 0, 100_000_000, 10000), USDC_OPT, savings=2_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["rewards"][0]["cap"] = {"limit": "5000", "consumed": "5000"}


@case(
    "062-reward-unverified",
    "The card's reward eligibility for this merchant category is unverified, so it is valued at zero.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 0, 100_000_000, 10000), USDC_OPT, savings=2_500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["rewards"][0]["eligibility"] = "unverified"


def points_rule(milli, cap=None):
    return {
        "rule_id": "card_points",
        "rule_version": "2026-09",
        "eligibility": "verified",
        "earn": {"type": "points", "program_id": "pts_demo", "milli_points_per_usd": str(milli)},
        "cap": cap,
        "description": "Points per dollar",
    }


def valuation(stability):
    return [{"program_id": "pts_demo", "valuation_version": "val-1", "micro_usd_per_point": "10000", "stability": stability}]


@case(
    "063-points-with-stable-valuation",
    "2 points per dollar on $100 is 200 points. At the customer's 1 cent per point that is $2.00: $98.00.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 2_000_000, 98_000_000, 10000), USDC_OPT, savings=500_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["rewards"] = [points_rule(2000)]
    req["preferences"]["point_valuations"] = valuation("stable")


@case(
    "064-points-with-unstable-valuation",
    "The point valuation is flagged unstable. The 200 points are shown but valued at zero.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 0, 100_000_000, 10000), USDC_OPT, savings=2_500_000,
           sensitivities=["quote_expiry", "margin", "unstable_point_valuation_excluded"]),
)
def _(req):
    card_opt(req)["rewards"] = [points_rule(2000)]
    req["preferences"]["point_valuations"] = valuation("unstable")


@case(
    "065-points-cap",
    "3 points per dollar would earn 300 points, but only 150 remain under the cap: $1.50, so $98.50.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(100_000_000, 1_500_000, 98_500_000, 10000), USDC_OPT, savings=1_000_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["rewards"] = [points_rule(3000, {"limit": "1000", "consumed": "850"})]
    req["preferences"]["point_valuations"] = valuation("stable")


@case(
    "066-points-round-down",
    "1.5 points per dollar on a $33.33 basket is 49.995 points, rounded down to 49: $0.49. "
    "Card: 33.33 - 0.49 = $32.84. Stablecoin: 33.33 - 4 + 1.50 = $30.83.",
    result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
           elig(33_330_000, 490_000, 32_840_000, 3333),
           elig(30_830_000, 0, 30_830_000, 30_830_000), savings=2_010_000,
           sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["quote"]["delivered_price"] = usd(3333)
    usdc_opt(req)["quote"]["delivered_price"] = usd(3333)
    card_opt(req)["rewards"] = [points_rule(1500)]
    req["preferences"]["point_valuations"] = valuation("stable")


@case(
    "067-sub-cent-precision",
    "1.25% cash back on $0.99 is 1.2375 cents, kept at micro-USD precision instead of being rounded to "
    "cents: $0.012375. Card: $0.977625. Stablecoin with no discount and a 0.000001 USDC fee: $0.990001.",
    result("preferred", "default_is_lowest_cost", "card", ["card"],
           elig(990_000, 12_375, 977_625, 99),
           elig(990_001, 0, 990_001, 990_001), sensitivities=["quote_expiry", "margin"]),
)
def _(req):
    card_opt(req)["quote"]["delivered_price"] = usd(99)
    usdc_opt(req)["quote"]["delivered_price"] = usd(99)
    card_opt(req)["rewards"][0]["earn"]["rate_bps"] = "125"
    usdc_opt(req)["discounts"] = []
    usdc_opt(req)["fees"][0]["amount"] = usdc(1)


# --- Rates, precision and range ----------------------------------------------


@case(
    "070-non-par-rate-directed-rounding",
    "USDC is quoted at $0.9997. A 1.234567 USDC fee is worth 1234196.6299 micro-USD, rounded up to 1234197. "
    "A 4.000001 USDC discount is worth 3998800.9997, rounded down to 3998800. "
    "Immediate cost: 100000000 - 3998800 + 1234197 = 97235397. "
    "Amount due: 97235397 / 0.9997 = 97264576.37..., rounded up to 97264577 base units.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD,
           elig(97_235_397, 0, 97_235_397, 97_264_577),
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    opt = usdc_opt(req)
    opt["quote"]["usd_rate"] = {"usd_num": "9997", "usd_den": "10000"}
    opt["fees"][0]["amount"] = usdc(1_234_567)
    opt["discounts"][0]["amount"] = usdc(4_000_001)


@case(
    "071-eighteen-decimal-asset",
    "The same economics as the plan example with an 18-decimal stablecoin: base-unit values near 10^20 "
    "are handled exactly.",
    result("preferred", "default_is_lowest_cost", "card", ["card"], CARD,
           elig(97_500_000, 0, 97_500_000, 97_500_000_000_000_000_000),
           sensitivities=["quote_expiry", "margin", "depends_on_estimated_rewards"]),
)
def _(req):
    dai = {"code": "DAIX", "network": "base", "decimals": 18}
    unit = 10**18
    req["policy"]["supported_stablecoins"] = [dai]
    opt = usdc_opt(req)
    opt["payment_asset"] = dai
    opt["quote"]["accepted_asset"] = dai
    opt["quote"]["delivered_price"] = {"asset": dai, "base_units": str(100 * unit)}
    opt["fees"][0]["amount"] = {"asset": dai, "base_units": str(unit * 3 // 2)}
    opt["discounts"][0]["amount"] = {"asset": dai, "base_units": str(4 * unit)}
    opt["funds"]["available"] = {"asset": dai, "base_units": str(250 * unit)}


@case(
    "072-amount-out-of-range",
    "A delivered price of 10^38 base units cannot be valued without overflow. The option is excluded "
    "instead of wrapping around.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("amount_out_of_range"),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["quote"]["delivered_price"] = usdc(10**38)


@case(
    "073-rate-unavailable",
    "No USD rate was supplied for the stablecoin, so its amounts cannot be compared.",
    result("insufficient_data", "options_missing_data", None, ["card"], CARD,
           excl("rate_unavailable", "unknown_required_fee"),
           sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires=CARD_ONLY_EXPIRY),
)
def _(req):
    usdc_opt(req)["quote"]["usd_rate"] = None


# --- Malformed requests ------------------------------------------------------


@case("900-zero-delivered-price", "A delivered price of zero is invalid.", expected_error="invalid_amount")
def _(req):
    card_opt(req)["quote"]["delivered_price"] = usd(0)


@case("901-negative-amount", "Negative amounts are not representable.", expected_error="malformed_request")
def _(req):
    usdc_opt(req)["fees"][0]["amount"]["base_units"] = "-1500000"


@case("902-amount-as-json-number", "Amounts must be decimal strings, never JSON numbers.", expected_error="malformed_request")
def _(req):
    card_opt(req)["quote"]["delivered_price"]["base_units"] = 10000


@case("903-fractional-amount", "Base units are integers.", expected_error="malformed_request")
def _(req):
    card_opt(req)["quote"]["delivered_price"]["base_units"] = "100.50"


@case(
    "904-conflicting-duplicate-discount",
    "The same discount id appears with two different amounts. The input is ambiguous and is rejected.",
    expected_error="conflicting_duplicate_discount",
)
def _(req):
    opt = usdc_opt(req)
    dup = copy.deepcopy(opt["discounts"][0])
    dup["amount"] = usd(500)
    opt["discounts"].append(dup)


@case("905-unknown-field", "Unknown fields are rejected, so a misspelled input is never ignored.", expected_error="malformed_request")
def _(req):
    usdc_opt(req)["fee"] = []


@case("906-unsupported-contract-version", "Only contract version 1 is implemented.", expected_error="unsupported_contract_version")
def _(req):
    req["contract_version"] = "2"


@case("907-cashback-rate-above-100-percent", "A cash back rate above 100% is invalid.", expected_error="invalid_reward")
def _(req):
    card_opt(req)["rewards"][0]["earn"]["rate_bps"] = "10001"


@case("908-default-option-not-offered", "The default must be one of the options.", expected_error="unknown_default_option")
def _(req):
    req["preferences"]["default_option_id"] = "debit"


@case("909-known-fee-without-amount", "A fee marked known must carry an amount.", expected_error="invalid_fee")
def _(req):
    usdc_opt(req)["fees"][0]["amount"] = None


@case("910-amount-beyond-i128", "A 40-digit amount exceeds the representable range.", expected_error="malformed_request")
def _(req):
    usdc_opt(req)["funds"]["available"]["base_units"] = "9" * 40


@case("911-zero-rate", "A zero rate is invalid.", expected_error="invalid_rate")
def _(req):
    usdc_opt(req)["quote"]["usd_rate"] = {"usd_num": "0", "usd_den": "1"}


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for stale in OUT.glob("*.json"):
        stale.unlink()
    names = set()
    for entry in CASES:
        assert entry["name"] not in names, entry["name"]
        names.add(entry["name"])
        path = OUT / f"{entry['name']}.json"
        path.write_text(json.dumps(entry, indent=2) + "\n")
    print(f"wrote {len(CASES)} vectors to {OUT}")


if __name__ == "__main__":
    main()
