#!/usr/bin/env python3
"""Writes the versioned recorded dataset in fixtures/recorded/v1 (milestone O1).

Every scenario has the shape of a golden vector plus a `catalog` entry, so the
same files drive the offline evaluation, the independent checker, and the
recorded quote provider behind the API in shadow mode.

ALL SCENARIOS ARE SYNTHETIC. They illustrate the data the optimizer needs from
a merchant and a stablecoin acceptance provider; none is a real quote. Replace
them with captured quotes once a merchant with comparable card and stablecoin
checkout paths is on board (first backlog item in plan.md).

Expected values were worked out by hand; independent_check.py recomputes them.
"""

import copy
import hashlib
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import build_golden as g  # noqa: E402

OUT = pathlib.Path(__file__).resolve().parent.parent / "recorded" / "v1"
RECORDED_AT = "2026-10-02T15:00:00Z"

OUTFITTER = ("mer_demo_outfitter", "Demo Outfitter (synthetic)")
PANTRY = ("mer_demo_pantry", "Demo Pantry (synthetic)")


def basket_hash(merchant_id, lines):
    canonical = json.dumps({"merchant_id": merchant_id, "lines": lines}, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(canonical.encode()).hexdigest()


def scenario(name, merchant, basket_ref, title, lines, delivered_minor, description, expected, mutate):
    merchant_id, merchant_name = merchant
    req = g.base()
    h = basket_hash(merchant_id, lines)
    req["evaluated_at"] = RECORDED_AT
    req["basket"] = {"basket_id": f"bsk_rec_{basket_ref}", "merchant_id": merchant_id, "basket_hash": h}
    req["preferences"]["min_savings_threshold"] = g.usd(100)
    for i, opt in enumerate(req["options"]):
        opt["quote"]["basket_hash"] = h
        opt["quote"]["quote_id"] = f"q_rec_{basket_ref}_{opt['option_id']}"
        opt["quote"]["delivered_price"] = g.usd(delivered_minor)
        opt["quote"]["obtained_at"] = "2026-10-02T14:59:40Z"
        opt["quote"]["valid_until"] = "2026-10-02T15:10:00Z" if i == 0 else "2026-10-02T15:05:00Z"
    card, usdc = req["options"]
    card["rewards"][0]["rule_id"] = "card_cashback_base"
    card["rewards"][0]["earn"]["rate_bps"] = "200"
    card["rewards"][0]["description"] = "2% cash back"
    usdc["discounts"] = []
    usdc["fees"][0]["amount"] = g.usdc(50000)
    mutate(req)
    expected = dict(expected)
    if expected["expires_at"] == "2026-10-02T12:05:00Z":  # g.result()'s default; recorded quotes expire later
        expected["expires_at"] = "2026-10-02T15:05:00Z"
    return {
        "name": name,
        "description": description,
        "synthetic": True,
        "catalog": {
            "merchant_id": merchant_id,
            "merchant_name": merchant_name,
            "basket_ref": basket_ref,
            "title": title,
            "lines": lines,
            "synthetic": True,
        },
        "request": req,
        "expected": expected,
    }


def line(sku, description, quantity, unit_minor):
    return {"sku": sku, "description": description, "quantity": quantity, "unit_price_minor": str(unit_minor)}


def pay_discount(minor, description):
    return {
        "discount_id": "usdc_pay_discount",
        "amount": g.usd(minor),
        "included_in_quote": False,
        "immediately_usable": True,
        "eligibility": "verified",
        "description": description,
    }


SCENARIOS = []


def add(*args, **kwargs):
    SCENARIOS.append(scenario(*args, **kwargs))


# 1. Trail running shoes: $129.99 + $8.45 shipping = $138.44.
#    Card: 2% of 138.44 = 2.7688 -> 135.6712. USDC: 3% of the item ($3.90) off, 0.05 USDC fee -> 134.59.
def s1(req):
    req["options"][1]["discounts"] = [pay_discount(390, "3% off for paying with USDC")]


add(
    "r001-trail-running-shoes", OUTFITTER, "trail-running-shoes", "Trail running shoes",
    [line("SHOE-TR-42", "Trail running shoes, size 42", 1, 12999), line("SHIP-STD", "Standard shipping", 1, 845)],
    13844,
    "Stablecoin saves $1.08 after the card's 2% cash back; above the $1.00 threshold.",
    g.result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
             g.elig(138_440_000, 2_768_800, 135_671_200, 13844),
             g.elig(134_590_000, 0, 134_590_000, 134_590_000), savings=1_081_200,
             sensitivities=["quote_expiry", "margin"]),
    s1,
)


# 2. Camp stove: $89.99. Card 3%: 2.6997 -> 87.2903. USDC: $1 off, 0.05 fee -> 89.04. Card wins.
def s2(req):
    req["options"][0]["rewards"][0]["rule_id"] = "card_cashback_outdoor_bonus"
    req["options"][0]["rewards"][0]["earn"]["rate_bps"] = "300"
    req["options"][0]["rewards"][0]["description"] = "3% cash back on outdoor retailers"
    req["options"][1]["discounts"] = [pay_discount(100, "$1 off for paying with USDC")]


add(
    "r002-camp-stove", OUTFITTER, "camp-stove", "Backpacking camp stove",
    [line("STOVE-BP-1", "Backpacking camp stove", 1, 8999)],
    8999,
    "A category bonus makes the card cheaper than a small stablecoin discount.",
    g.result("preferred", "default_is_lowest_cost", "card", ["card"],
             g.elig(89_990_000, 2_699_700, 87_290_300, 8999),
             g.elig(89_040_000, 0, 89_040_000, 89_040_000),
             sensitivities=["quote_expiry", "margin"]),
    s2,
)


# 3. Water filter: the merchant does not accept stablecoin for this item.
def s3(req):
    req["options"][1]["availability"]["merchant_acceptance"] = "rejected"


add(
    "r003-water-filter", OUTFITTER, "water-filter", "Gravity water filter",
    [line("FILT-GR-3L", "Gravity water filter, 3 L", 1, 5450)],
    5450,
    "Stablecoin is not accepted for this basket, so the card is the only eligible option.",
    g.result("preferred", "only_eligible_option", "card", ["card"],
             g.elig(54_500_000, 1_090_000, 53_410_000, 5450),
             g.excl("merchant_does_not_accept", amounts=(54_550_000, 0, 54_550_000, 54_550_000)),
             sensitivities=["quote_expiry"], expires="2026-10-02T15:10:00Z"),
    s3,
)


# 4. Headlamp bundle $64.50. Card: 2 points/$ at 1c = 129 points = $1.29 -> 63.21.
#    USDC: 20 USDC on hand, 50 USDC funding step (bank transfer, $0.30 + $0.15), $2.50 off, 0.05 fee:
#    64.50 - 2.50 + 0.05 + 0.30 + 0.15 = 62.50; due 62.05 USDC. Saving $0.71 < $1.00 -> card kept.
def s4(req):
    card, usdc = req["options"]
    card["rewards"] = [g.points_rule(2000)]
    req["preferences"]["point_valuations"] = g.valuation("stable")
    usdc["discounts"] = [pay_discount(250, "$2.50 off for paying with USDC")]
    usdc["funds"]["available"] = g.usdc(20_000_000)
    usdc["funding_step"] = {
        "source": "bank_transfer",
        "amount": g.usdc(50_000_000),
        "quote_id": "q_rec_headlamp_fund",
        "valid_until": "2026-10-02T15:04:00Z",
        "customer_consent": True,
    }
    usdc["fees"] += [
        {"fee_id": "usdc_funding", "kind": "funding", "status": "known", "amount": g.usd(30), "description": "Bank transfer fee"},
        {"fee_id": "usdc_conversion", "kind": "conversion", "status": "known", "amount": g.usd(15), "description": "USD to USDC conversion"},
    ]


add(
    "r004-headlamp-bundle", OUTFITTER, "headlamp-bundle", "Headlamp and batteries",
    [line("LAMP-HL-400", "Headlamp, 400 lm", 1, 4950), line("BATT-AAA-8", "AAA batteries, 8 pack", 1, 1500)],
    6450,
    "Stablecoin needs a quoted top-up. Even with funding fees it is $0.71 cheaper, below the $1.00 threshold, "
    "and the card's advantage depends on estimated points.",
    g.result("preferred", "savings_below_threshold", "card", ["usdc"],
             g.elig(64_500_000, 1_290_000, 63_210_000, 6450),
             g.elig(62_500_000, 0, 62_500_000, 62_050_000), savings=710_000,
             sensitivities=["quote_expiry", "margin", "savings_below_threshold", "depends_on_estimated_rewards"],
             expires="2026-10-02T15:04:00Z"),
    s4,
)


# 5. Pantry restock $47.18. Card 1.5%: 0.7077. USDC network fee unknown -> insufficient data.
def s5(req):
    card, usdc = req["options"]
    card["rewards"][0]["rule_id"] = "card_cashback_grocery"
    card["rewards"][0]["earn"]["rate_bps"] = "150"
    card["rewards"][0]["description"] = "1.5% cash back"
    usdc["fees"][0]["status"] = "unknown"
    usdc["fees"][0]["amount"] = None


add(
    "r005-pantry-restock", PANTRY, "pantry-restock", "Pantry restock",
    [line("RICE-5KG", "Basmati rice, 5 kg", 1, 1899), line("OIL-OLV-1L", "Olive oil, 1 L", 2, 1299), line("SHIP-STD", "Standard shipping", 1, 721)],
    4718,
    "The stablecoin network fee could not be quoted. The option is not costed as if the fee were zero.",
    g.result("insufficient_data", "options_missing_data", None, ["card"],
             g.elig(47_180_000, 707_700, 46_472_300, 4718),
             g.excl("unknown_required_fee"),
             sensitivities=["quote_expiry", "excluded_option_could_change_result"], expires="2026-10-02T15:10:00Z"),
    s5,
)


# 6. Coffee box $36.00. Card 5% rotating category, cap $75 with $74.50 used -> $0.50. USDC $2 off, 0.05 fee -> 34.05.
def s6(req):
    card, usdc = req["options"]
    card["rewards"][0]["rule_id"] = "card_cashback_rotating_q4"
    card["rewards"][0]["earn"]["rate_bps"] = "500"
    card["rewards"][0]["cap"] = {"limit": "7500", "consumed": "7450"}
    card["rewards"][0]["description"] = "5% rotating category, capped"
    usdc["discounts"] = [pay_discount(200, "$2 off for paying with USDC")]


add(
    "r006-coffee-box", PANTRY, "coffee-box", "Coffee subscription box",
    [line("COF-BOX-M", "Coffee box, monthly", 1, 3600)],
    3600,
    "The card's 5% category bonus is nearly used up for the quarter: only $0.50 remains, so stablecoin saves $1.45.",
    g.result("preferred", "savings_meet_threshold", "usdc", ["usdc"],
             g.elig(36_000_000, 500_000, 35_500_000, 3600),
             g.elig(34_050_000, 0, 34_050_000, 34_050_000), savings=1_450_000,
             sensitivities=["quote_expiry", "margin"]),
    s6,
)


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for stale in OUT.glob("*.json"):
        stale.unlink()
    for entry in SCENARIOS:
        (OUT / f"{entry['name']}.json").write_text(json.dumps(entry, indent=2) + "\n")
    print(f"wrote {len(SCENARIOS)} recorded scenarios to {OUT}")


if __name__ == "__main__":
    main()
