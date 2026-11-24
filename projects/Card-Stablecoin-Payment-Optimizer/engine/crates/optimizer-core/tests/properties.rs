//! Property tests over arithmetic boundaries, malformed input and decision
//! invariants. They start from the plan-example vector and vary it.

use optimizer_core::amount::Units;
use optimizer_core::contract::*;
use optimizer_core::evaluate;
use proptest::prelude::*;

fn base() -> EvaluationRequest {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../fixtures/golden/v1/001-plan-example.json"
    );
    let vector: serde_json::Value =
        serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
    serde_json::from_value(vector["request"].clone()).unwrap()
}

fn units(v: i128) -> Units {
    Units::new(v).unwrap()
}

/// Mostly realistic magnitudes, with a tail that reaches `i128::MAX`.
fn any_units() -> impl Strategy<Value = Units> {
    prop_oneof![
        6 => (0i128..=2_000_000_000).prop_map(units),
        2 => (0i128..=i128::MAX).prop_map(units),
        1 => Just(units(i128::MAX)),
        1 => Just(units(0)),
    ]
}

fn positive_units() -> impl Strategy<Value = Units> {
    any_units().prop_map(|u| if u.get() == 0 { units(1) } else { u })
}

#[derive(Debug, Clone)]
struct Scenario {
    card_price: Units,
    coin_price: Units,
    coin_price_in_asset: bool,
    fee: Units,
    discount: Units,
    card_discount: Option<Units>,
    rate: (Units, Units),
    decimals: u8,
    bps: i128,
    cap: Option<(Units, Units)>,
    points_milli: Option<Units>,
    point_value: Units,
    funds: Units,
    reserve: Option<Units>,
    threshold: Units,
    default: Option<bool>,
}

fn scenario() -> impl Strategy<Value = Scenario> {
    (
        (
            positive_units(),
            positive_units(),
            any::<bool>(),
            any_units(),
            any_units(),
        ),
        (
            proptest::option::of(any_units()),
            (positive_units(), positive_units()),
            0u8..=18,
            0i128..=10_000,
        ),
        (
            proptest::option::of((any_units(), any_units())),
            proptest::option::of(any_units()),
            any_units(),
            any_units(),
            proptest::option::of(any_units()),
            any_units(),
            proptest::option::of(any::<bool>()),
        ),
    )
        .prop_map(|(a, b, c)| Scenario {
            card_price: a.0,
            coin_price: a.1,
            coin_price_in_asset: a.2,
            fee: a.3,
            discount: a.4,
            card_discount: b.0,
            rate: b.1,
            decimals: b.2,
            bps: b.3,
            cap: c.0,
            points_milli: c.1,
            point_value: c.2,
            funds: c.3,
            reserve: c.4,
            threshold: c.5,
            default: c.6,
        })
}

fn build(s: &Scenario) -> EvaluationRequest {
    let mut req = base();
    let coin = AssetRef {
        code: "USDC".into(),
        network: Some("base".into()),
        decimals: s.decimals,
    };
    let in_coin = |base_units| Amount {
        asset: coin.clone(),
        base_units,
    };
    let in_usd = |base_units| Amount {
        asset: AssetRef::usd(),
        base_units,
    };

    req.policy.supported_stablecoins = vec![coin.clone()];
    req.preferences.min_savings_threshold = in_usd(s.threshold);
    req.preferences.default_option_id =
        s.default
            .map(|card| if card { "card".into() } else { "usdc".into() });
    req.preferences.point_valuations = vec![PointValuation {
        program_id: "pts".into(),
        valuation_version: "v".into(),
        micro_usd_per_point: s.point_value,
        stability: Stability::Stable,
    }];

    let card = &mut req.options[0];
    card.quote.delivered_price = in_usd(s.card_price);
    card.funds.available = Some(in_usd(units(i128::MAX)));
    card.rewards[0].earn = Earn::Cashback {
        rate_bps: units(s.bps),
    };
    card.rewards[0].cap = s.cap.map(|(limit, consumed)| RewardCap { limit, consumed });
    if let Some(milli) = s.points_milli {
        let mut rule = card.rewards[0].clone();
        rule.rule_id = "points".into();
        rule.cap = None;
        rule.earn = Earn::Points {
            program_id: "pts".into(),
            milli_points_per_usd: milli,
        };
        card.rewards.push(rule);
    }
    if let Some(amount) = s.card_discount {
        card.discounts.push(Discount {
            discount_id: "card_discount".into(),
            amount: in_usd(amount),
            included_in_quote: false,
            immediately_usable: true,
            eligibility: Verification::Verified,
            description: String::new(),
        });
    }

    let usdc = &mut req.options[1];
    usdc.payment_asset = coin.clone();
    usdc.quote.accepted_asset = coin.clone();
    usdc.quote.usd_rate = Some(Rate {
        usd_num: s.rate.0,
        usd_den: s.rate.1,
    });
    usdc.quote.delivered_price = if s.coin_price_in_asset {
        in_coin(s.coin_price)
    } else {
        in_usd(s.coin_price)
    };
    usdc.fees[0].amount = Some(in_coin(s.fee));
    usdc.discounts[0].amount = in_coin(s.discount);
    usdc.funds.available = Some(in_coin(s.funds));
    usdc.funds.reserve = s.reserve.map(in_coin);
    req
}

fn check_breakdown(opt: &OptionResult) -> Result<(), TestCaseError> {
    prop_assert_eq!(
        opt.status == OptionStatus::Eligible,
        opt.exclusions.is_empty()
    );
    if opt.status == OptionStatus::Eligible {
        prop_assert!(opt.breakdown.is_some());
    }
    let Some(b) = &opt.breakdown else {
        return Ok(());
    };
    let fees: i128 = b.fees.iter().map(|f| f.micro_usd.0).sum();
    let discounts: i128 = b.discounts.iter().map(|d| d.micro_usd.0).sum();
    let rewards: i128 = b.rewards.iter().map(|r| r.micro_usd.0).sum();
    prop_assert_eq!(fees, b.fees_total_micro_usd.0);
    prop_assert_eq!(discounts, b.discounts_applied_total_micro_usd.0);
    prop_assert_eq!(rewards, b.estimated_rewards_total_micro_usd.0);
    prop_assert!(
        discounts <= b.delivered_price_micro_usd.0,
        "discounts never exceed the price"
    );
    prop_assert!(b.immediate_cost_micro_usd.0 >= 0);
    prop_assert_eq!(
        b.immediate_cost_micro_usd.0,
        b.delivered_price_micro_usd.0 + fees - discounts
    );
    prop_assert_eq!(
        b.estimated_net_cost_micro_usd.0,
        b.immediate_cost_micro_usd.0 - rewards
    );
    for line in b.rewards.iter().chain(std::iter::empty()) {
        prop_assert!(line.micro_usd.0 >= 0);
        prop_assert!(
            line.counted || line.micro_usd.0 == 0,
            "uncounted rewards carry no value"
        );
    }
    Ok(())
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(2000))]

    #[test]
    fn arbitrary_strings_never_panic_the_parser(s in "\\PC{0,48}") {
        if let Ok(parsed) = Units::parse(&s) {
            prop_assert_eq!(parsed.to_string(), s);
        }
    }

    #[test]
    fn arbitrary_json_never_panics_the_deserializer(s in "\\PC{0,256}") {
        let _ = serde_json::from_str::<EvaluationRequest>(&s);
    }

    #[test]
    fn evaluation_is_total_deterministic_and_consistent(s in scenario()) {
        let req = build(&s);
        // No input in range may panic; overflow must surface as an exclusion or an error.
        let Ok(first) = evaluate(&req) else { return Ok(()) };
        prop_assert_eq!(&first, &evaluate(&req).unwrap());
        for opt in &first.options {
            check_breakdown(opt)?;
        }
        if let Some(pref) = &first.preferred_option_id {
            let eligible = first.options.iter().any(|o| &o.option_id == pref && o.status == OptionStatus::Eligible);
            prop_assert!(eligible);
        }
        if first.outcome == Outcome::Tie {
            prop_assert!(first.tied_option_ids.len() >= 2);
            prop_assert!(first.preferred_option_id.is_none());
        }
    }

    #[test]
    fn option_order_does_not_change_the_result(s in scenario()) {
        let req = build(&s);
        let mut reversed = req.clone();
        reversed.options.reverse();
        match (evaluate(&req), evaluate(&reversed)) {
            (Ok(a), Ok(b)) => {
                prop_assert_eq!(a.outcome, b.outcome);
                prop_assert_eq!(a.rationale, b.rationale);
                prop_assert_eq!(a.preferred_option_id, b.preferred_option_id);
                let (mut x, mut y) = (a.lowest_cost_option_ids, b.lowest_cost_option_ids);
                x.sort();
                y.sort();
                prop_assert_eq!(x, y);
            }
            (Err(a), Err(b)) => prop_assert_eq!(a.code, b.code),
            (a, b) => prop_assert!(false, "order changed success: {:?} vs {:?}", a.is_ok(), b.is_ok()),
        }
    }

    #[test]
    fn a_higher_fee_never_lowers_the_cost(s in scenario(), extra in 1i128..=1_000_000_000) {
        let low = build(&s);
        let mut bumped = s.clone();
        let Some(fee) = s.fee.get().checked_add(extra).and_then(Units::new) else { return Ok(()) };
        bumped.fee = fee;
        let high = build(&bumped);
        let (Ok(a), Ok(b)) = (evaluate(&low), evaluate(&high)) else { return Ok(()) };
        if let (Some(x), Some(y)) = (&a.options[1].breakdown, &b.options[1].breakdown) {
            prop_assert!(y.estimated_net_cost_micro_usd.0 >= x.estimated_net_cost_micro_usd.0);
            prop_assert!(y.amount_due.base_units >= x.amount_due.base_units);
        }
    }

    #[test]
    fn a_larger_discount_never_raises_the_cost(s in scenario(), extra in 1i128..=1_000_000_000) {
        let small = build(&s);
        let mut bumped = s.clone();
        let Some(discount) = s.discount.get().checked_add(extra).and_then(Units::new) else { return Ok(()) };
        bumped.discount = discount;
        let large = build(&bumped);
        let (Ok(a), Ok(b)) = (evaluate(&small), evaluate(&large)) else { return Ok(()) };
        if let (Some(x), Some(y)) = (&a.options[1].breakdown, &b.options[1].breakdown) {
            prop_assert!(y.immediate_cost_micro_usd.0 <= x.immediate_cost_micro_usd.0);
        }
    }

    #[test]
    fn cashback_never_exceeds_the_remaining_cap(s in scenario()) {
        let req = build(&s);
        let Ok(resp) = evaluate(&req) else { return Ok(()) };
        let (Some(b), Some((limit, consumed))) = (&resp.options[0].breakdown, s.cap) else { return Ok(()) };
        let remaining_minor = (limit.get() - consumed.get()).max(0);
        let line = &b.rewards[0];
        // Compared in cents to stay in range: value_micro <= remaining_minor * 10^4.
        prop_assert!(line.micro_usd.0 / 10_000 <= remaining_minor);
        if remaining_minor == 0 {
            prop_assert!(!line.counted);
        }
    }

    #[test]
    fn eligible_stablecoin_payment_fits_spendable_funds(s in scenario()) {
        let req = build(&s);
        let Ok(resp) = evaluate(&req) else { return Ok(()) };
        let opt = &resp.options[1];
        if opt.status == OptionStatus::Eligible {
            let spendable = (s.funds.get() - s.reserve.map_or(0, Units::get)).max(0);
            prop_assert!(opt.breakdown.as_ref().unwrap().amount_due.base_units.get() <= spendable);
        }
    }
}
