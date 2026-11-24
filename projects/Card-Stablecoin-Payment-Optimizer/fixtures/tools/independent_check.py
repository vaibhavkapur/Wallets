#!/usr/bin/env python3
"""Independent check of the golden vectors.

A second implementation of docs/calculation.md that shares no code with the
Rust engine. It works in exact rationals (fractions.Fraction) and applies
rounding only where the rules call for it, then compares every vector's
hand-written expectation with what it derives from the request.

Malformed-request vectors (`expected_error`) are out of scope here: they test
the engine's input validation, not a calculation.

Usage: independent_check.py [directory ...]   (default: fixtures/golden/v1)
"""

import json
import math
import pathlib
import sys
from datetime import datetime
from fractions import Fraction

MISSING_DATA = {
    "jurisdiction_unverified",
    "merchant_acceptance_unverified",
    "stale_quote",
    "basket_mismatch",
    "rate_unavailable",
    "unknown_required_fee",
    "amount_out_of_range",
    "funds_unverified",
}
I128_MAX = 2**127 - 1
COMPONENT_MAX = 10**30


class OutOfRange(Exception):
    pass


def ts(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def is_usd(asset):
    return asset["code"] == "USD" and asset.get("network") is None and asset["decimals"] == 2


def same(a, b):
    return (a["code"], a.get("network"), a["decimals"]) == (b["code"], b.get("network"), b["decimals"])


def checked(value):
    """The engine multiplies before dividing in i128; mirror its range limit."""
    if value > I128_MAX:
        raise OutOfRange
    return value


class Option:
    def __init__(self, raw):
        self.raw = raw
        self.asset = raw["payment_asset"]
        rate = raw["quote"].get("usd_rate")
        self.rate = Fraction(int(rate["usd_num"]), int(rate["usd_den"])) if rate else None
        self.rate_parts = (int(rate["usd_num"]), int(rate["usd_den"])) if rate else None

    def valuable(self, asset):
        return is_usd(asset) or (same(asset, self.asset) and self.rate is not None)

    def micro(self, amount, up):
        """Value of an amount in micro-USD, rounded up for costs and down for benefits."""
        units = int(amount["base_units"])
        asset = amount["asset"]
        if is_usd(asset):
            exact = Fraction(checked(units * 10_000))
        else:
            num, den = self.rate_parts
            checked(units * num * 10**6)
            checked(den * 10 ** asset["decimals"])
            exact = Fraction(units) / 10 ** asset["decimals"] * self.rate * 10**6
        value = math.ceil(exact) if up else math.floor(exact)
        if value > COMPONENT_MAX:
            raise OutOfRange
        return value

    def units_due(self, micro):
        if is_usd(self.asset):
            return math.ceil(Fraction(micro, 10_000))
        num, den = self.rate_parts
        checked(micro * den * 10 ** self.asset["decimals"])
        return math.ceil(Fraction(micro, 10**6) / self.rate * 10 ** self.asset["decimals"])


def evaluate_option(req, raw):
    opt = Option(raw)
    now = ts(req["evaluated_at"])
    quote = raw["quote"]
    policy = req["policy"]
    constraints = req["preferences"]["hard_constraints"]
    codes = set()

    availability = raw["availability"]
    if not availability["method_available"]:
        codes.add("method_unavailable")
    codes |= {"unverified": {"jurisdiction_unverified"}, "rejected": {"unsupported_jurisdiction"}}.get(availability["jurisdiction"], set())
    codes |= {"unverified": {"merchant_acceptance_unverified"}, "rejected": {"merchant_does_not_accept"}}.get(availability["merchant_acceptance"], set())
    if not availability["customer_consent"]:
        codes.add("not_consented")

    allowed = constraints.get("allowed_rails")
    if allowed is not None and raw["rail"] not in allowed:
        codes.add("rail_not_allowed")
    if constraints.get("require_purchase_protection") and raw["tradeoffs"]["purchase_protection"] != "yes":
        codes.add("purchase_protection_required")
    if constraints.get("avoid_credit") and raw["tradeoffs"]["uses_credit"]:
        codes.add("credit_not_allowed")

    if ts(quote["valid_until"]) <= now:
        codes.add("stale_quote")
    if quote["basket_hash"] != req["basket"]["basket_hash"]:
        codes.add("basket_mismatch")

    if raw["rail"] == "card":
        rail_ok = is_usd(opt.asset)
    else:
        rail_ok = any(same(opt.asset, s) for s in policy["supported_stablecoins"])
    if not rail_ok or not same(opt.asset, quote["accepted_asset"]):
        codes.add("incompatible_asset")

    computable = True
    if not opt.valuable(opt.asset) or not opt.valuable(quote["delivered_price"]["asset"]):
        codes.add("rate_unavailable")
        computable = False

    step = raw.get("funding_step")
    if step:
        if step["source"] not in policy["permitted_funding_sources"]:
            codes.add("funding_source_not_permitted")
        if not step["customer_consent"]:
            codes.add("funding_not_consented")
        if ts(step["valid_until"]) <= now:
            codes.add("stale_quote")

    required = set(policy["required_fee_kinds"][raw["rail"]])
    if step:
        required |= set(policy["required_fee_kinds"]["with_funding_step"])
    present = {fee["kind"] for fee in raw["fees"]}
    unknown = [f for f in raw["fees"] if f["status"] == "unknown"]
    unvaluable = [f for f in raw["fees"] if f["status"] == "known" and not opt.valuable(f["amount"]["asset"])]
    if required - present or unknown or unvaluable:
        codes.add("unknown_required_fee")
        computable = False

    amounts = None
    if computable:
        try:
            amounts = compute(req, raw, opt)
        except OutOfRange:
            codes.add("amount_out_of_range")

    funds = raw["funds"]
    if funds["status"] != "known":
        codes.add("funds_unverified")
    elif amounts:
        reserve = int(funds["reserve"]["base_units"]) if funds.get("reserve") else 0
        spendable = max(int(funds["available"]["base_units"]) - reserve, 0)
        if step:
            spendable += int(step["amount"]["base_units"])
        if spendable < amounts["due"]:
            codes.add("insufficient_funds")

    expiry = [ts(quote["valid_until"])] + ([ts(step["valid_until"])] if step else [])
    return {"id": raw["option_id"], "codes": codes, "amounts": amounts, "expiry": min(expiry)}


def compute(req, raw, opt):
    price = opt.micro(raw["quote"]["delivered_price"], up=True)

    fees = charged = 0
    for fee in raw["fees"]:
        if fee["status"] != "known":
            continue
        value = opt.micro(fee["amount"], up=True)
        fees += value
        if fee["kind"] not in ("funding", "conversion") and same(fee["amount"]["asset"], opt.asset):
            charged += value

    discounts = 0
    seen = set()
    for d in raw["discounts"]:
        if d["discount_id"] in seen:
            continue
        seen.add(d["discount_id"])
        usable = (
            not d["included_in_quote"]
            and d["eligibility"] == "verified"
            and d["immediately_usable"]
            and opt.valuable(d["amount"]["asset"])
        )
        if usable:
            discounts += min(opt.micro(d["amount"], up=False), price - discounts)

    base = price - discounts
    immediate = base + fees

    valuations = {v["program_id"]: v for v in req["preferences"].get("point_valuations", [])}
    rewards = 0
    for rule in raw["rewards"]:
        if rule["eligibility"] != "verified":
            continue
        cap = rule.get("cap")
        remaining = max(int(cap["limit"]) - int(cap["consumed"]), 0) if cap else None
        earn = rule["earn"]
        if earn["type"] == "cashback":
            value = math.floor(Fraction(base * int(earn["rate_bps"]), 10_000))
            if remaining is not None:
                value = min(value, remaining * 10_000)
            rewards += value
        else:
            points = math.floor(Fraction(base, 10**6) * Fraction(int(earn["milli_points_per_usd"]), 1000))
            if remaining is not None:
                points = min(points, remaining)
            valuation = valuations.get(earn["program_id"])
            if valuation and valuation["stability"] == "stable":
                rewards += points * int(valuation["micro_usd_per_point"])

    return {
        "immediate": immediate,
        "rewards": rewards,
        "net": immediate - rewards,
        "due": opt.units_due(base + charged),
    }


def decide(costs, default, threshold, missing_data):
    """costs: list of (option_id, cost) for eligible options, in request order."""
    if not costs:
        return ("insufficient_data", "options_missing_data", None, []) if missing_data else ("no_eligible_option", "no_eligible_option", None, [])
    best = min(cost for _, cost in costs)
    lowest = [oid for oid, cost in costs if cost == best]
    if missing_data:
        return ("insufficient_data", "options_missing_data", None, lowest)
    if len(costs) == 1:
        return ("preferred", "only_eligible_option", costs[0][0], lowest)
    by_id = dict(costs)
    if default in by_id:
        if default in lowest:
            if len(lowest) == 1:
                return ("preferred", "default_is_lowest_cost", default, lowest)
            return ("tie", "equal_net_cost", None, lowest)
        if by_id[default] - best < threshold:
            return ("preferred", "savings_below_threshold", default, lowest)
    if len(lowest) == 1:
        return ("preferred", "savings_meet_threshold" if default in by_id else "lowest_cost_no_default", lowest[0], lowest)
    return ("tie", "equal_net_cost", None, lowest)


def evaluate(req):
    options = [evaluate_option(req, raw) for raw in req["options"]]
    eligible = [o for o in options if not o["codes"]]
    missing_data = any(o["codes"] and o["codes"] <= MISSING_DATA for o in options)
    default = req["preferences"].get("default_option_id")
    threshold = int(req["preferences"]["min_savings_threshold"]["base_units"]) * 10_000

    net = [(o["id"], o["amounts"]["net"]) for o in eligible]
    outcome, rationale, preferred, lowest = decide(net, default, threshold, missing_data)

    savings = None
    by_id = dict(net)
    if default in by_id and lowest and default not in lowest:
        savings = by_id[default] - by_id[lowest[0]]

    expires = min((o["expiry"] for o in eligible), default=None)
    out = {
        "outcome": outcome,
        "rationale": rationale,
        "preferred_option_id": preferred,
        "tied_option_ids": lowest if outcome == "tie" else [],
        "lowest_cost_option_ids": lowest,
        "savings_estimated_total_micro_usd": None if savings is None else str(savings),
        "expires_at": expires.strftime("%Y-%m-%dT%H:%M:%SZ") if expires else None,
        "options": {},
    }
    for o in options:
        entry = {"status": "excluded" if o["codes"] else "eligible", "exclusion_codes": sorted(o["codes"])}
        if o["amounts"]:
            a = o["amounts"]
            entry.update(
                immediate_cost_micro_usd=str(a["immediate"]),
                estimated_rewards_micro_usd=str(a["rewards"]),
                estimated_net_cost_micro_usd=str(a["net"]),
                amount_due_base_units=str(a["due"]),
            )
        out["options"][o["id"]] = entry
    return out


def main(argv):
    root = pathlib.Path(__file__).resolve().parent.parent
    dirs = [pathlib.Path(a) for a in argv] or [root / "golden" / "v1"]
    checked_count = skipped = 0
    failures = []
    for directory in dirs:
        for path in sorted(directory.glob("*.json")):
            vector = json.loads(path.read_text())
            if "expected_error" in vector:
                skipped += 1
                continue
            expected = dict(vector["expected"])
            expected.pop("sensitivity_codes", None)  # explanation data, not a calculation
            actual = evaluate(vector["request"])
            checked_count += 1
            if actual != expected:
                failures.append((path.name, expected, actual))
    for name, expected, actual in failures:
        print(f"MISMATCH {name}")
        for key in sorted(set(expected) | set(actual)):
            if expected.get(key) != actual.get(key):
                print(f"  {key}:\n    vector:      {json.dumps(expected.get(key))}\n    independent: {json.dumps(actual.get(key))}")
    print(f"{checked_count} vectors checked independently, {skipped} malformed-request vectors skipped, {len(failures)} mismatches")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
