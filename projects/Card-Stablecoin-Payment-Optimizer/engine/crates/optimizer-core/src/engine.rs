//! Eligibility, fee normalization, reward caps, scoring and explanation data.

use std::collections::BTreeSet;

use crate::amount::{
    add, div_round, mul, pow10, sub, MicroUsd, Overflow, Round, Units, MICRO_PER_USD,
    MICRO_PER_USD_MINOR,
};
use crate::contract::*;
use crate::error::{RequestError, RequestErrorCode};
use crate::validate::validate;
use crate::{CONTRACT_VERSION, ENGINE_VERSION, FORMULA_VERSION, ROUNDING_VERSION};

/// Largest micro-USD magnitude any single component may take (10^24 USD).
/// Keeping components far below `i128::MAX` means sums and differences of a
/// bounded number of them cannot overflow.
const MAX_COMPONENT_MICRO: i128 = 10i128.pow(30);

const BPS_DENOMINATOR: i128 = 10_000;
const MILLI_POINTS_PER_POINT: i128 = 1_000;

#[derive(Debug)]
enum ValueError {
    /// The amount's asset cannot be expressed in USD with the data provided.
    Unvaluable,
    Overflow,
}

impl From<Overflow> for ValueError {
    fn from(_: Overflow) -> Self {
        ValueError::Overflow
    }
}

/// Converts amounts between an option's assets and micro-USD.
struct Valuer<'a> {
    payment_asset: &'a AssetRef,
    rate: Option<&'a Rate>,
}

impl Valuer<'_> {
    fn can_value(&self, asset: &AssetRef) -> bool {
        asset.is_usd() || (asset == self.payment_asset && self.rate.is_some())
    }

    fn micro(&self, amount: &Amount, round: Round) -> Result<i128, ValueError> {
        let base = amount.base_units.get();
        let value = if amount.asset.is_usd() {
            mul(base, MICRO_PER_USD_MINOR)?
        } else if &amount.asset == self.payment_asset {
            let rate = self.rate.ok_or(ValueError::Unvaluable)?;
            let numerator = mul(mul(base, rate.usd_num.get())?, MICRO_PER_USD)?;
            let denominator = mul(rate.usd_den.get(), pow10(amount.asset.decimals)?)?;
            div_round(numerator, denominator, round)?
        } else {
            return Err(ValueError::Unvaluable);
        };
        if value > MAX_COMPONENT_MICRO {
            return Err(ValueError::Overflow);
        }
        Ok(value)
    }

    /// Micro-USD expressed in base units of the payment asset.
    fn payment_units(&self, micro: i128, round: Round) -> Result<i128, ValueError> {
        if self.payment_asset.is_usd() {
            return Ok(div_round(micro, MICRO_PER_USD_MINOR, round)?);
        }
        let rate = self.rate.ok_or(ValueError::Unvaluable)?;
        let numerator = mul(
            mul(micro, rate.usd_den.get())?,
            pow10(self.payment_asset.decimals)?,
        )?;
        let denominator = mul(rate.usd_num.get(), MICRO_PER_USD)?;
        Ok(div_round(numerator, denominator, round)?)
    }
}

fn explanation(
    code: ExplanationCode,
    option_id: Option<&str>,
    params: &[(&str, String)],
) -> Explanation {
    Explanation {
        code,
        option_id: option_id.map(str::to_owned),
        params: params
            .iter()
            .map(|(k, v)| ((*k).to_owned(), v.clone()))
            .collect(),
    }
}

fn exclusion(code: ExclusionCode, params: &[(&str, String)]) -> Exclusion {
    Exclusion {
        code,
        kind: code.kind(),
        params: params
            .iter()
            .map(|(k, v)| ((*k).to_owned(), v.clone()))
            .collect(),
    }
}

fn snake<T: serde::Serialize + Copy + std::fmt::Debug>(value: T) -> String {
    // Enum wire names are snake_case of the variant name.
    let debug = format!("{value:?}");
    let mut out = String::with_capacity(debug.len() + 4);
    for (i, ch) in debug.chars().enumerate() {
        if ch.is_ascii_uppercase() {
            if i > 0 {
                out.push('_');
            }
            out.push(ch.to_ascii_lowercase());
        } else {
            out.push(ch);
        }
    }
    out
}

#[derive(Clone, Copy)]
struct Costs {
    immediate: i128,
    rewards: i128,
    net: i128,
}

struct Computed {
    breakdown: Breakdown,
    costs: Costs,
    funding_fees: i128,
    notes: Vec<Explanation>,
    assumptions: Vec<Explanation>,
    sensitivities: Vec<Explanation>,
}

struct OptionEval {
    result: OptionResult,
    costs: Option<Costs>,
    /// Excluded only for reasons that better or fresher data could resolve.
    missing_data_only: bool,
    assumptions: Vec<Explanation>,
    sensitivities: Vec<Explanation>,
}

/// Evaluates a request. Pure and deterministic: equal requests produce equal responses.
pub fn evaluate(req: &EvaluationRequest) -> Result<EvaluationResponse, RequestError> {
    validate(req)?;

    let evals: Vec<OptionEval> = req
        .options
        .iter()
        .map(|opt| evaluate_option(req, opt))
        .collect();

    let eligible: Vec<(&OptionEval, Costs)> = evals
        .iter()
        .filter(|e| e.result.status == OptionStatus::Eligible)
        .filter_map(|e| e.costs.map(|c| (e, c)))
        .collect();
    let any_missing_data = evals.iter().any(|e| e.missing_data_only);

    let threshold = mul(
        req.preferences.min_savings_threshold.base_units.get(),
        MICRO_PER_USD_MINOR,
    )
    .map_err(|_| {
        RequestError::new(
            RequestErrorCode::InvalidThreshold,
            "/preferences/min_savings_threshold",
            "the savings threshold is out of range",
        )
    })?;
    let default = req.preferences.default_option_id.as_deref();

    let by_net: Vec<(&str, i128)> = eligible
        .iter()
        .map(|(e, c)| (e.result.option_id.as_str(), c.net))
        .collect();
    let by_immediate: Vec<(&str, i128)> = eligible
        .iter()
        .map(|(e, c)| (e.result.option_id.as_str(), c.immediate))
        .collect();

    let overflow = |_: Overflow| {
        RequestError::new(
            RequestErrorCode::ArithmeticOverflow,
            "/options",
            "comparison arithmetic left the representable range",
        )
    };
    let decision = decide(&by_net, default, threshold, any_missing_data).map_err(overflow)?;
    let immediate_only =
        decide(&by_immediate, default, threshold, any_missing_data).map_err(overflow)?;

    let baseline = default.filter(|d| by_net.iter().any(|(id, _)| id == d));
    let cost_of = |id: &str| {
        eligible
            .iter()
            .find(|(e, _)| e.result.option_id == id)
            .map(|(_, c)| *c)
    };

    let mut savings = None;
    if let (Some(base_id), Some(low_id)) = (baseline, decision.lowest.first()) {
        if !decision.lowest.iter().any(|id| id == base_id) {
            if let (Some(base), Some(low)) = (cost_of(base_id), cost_of(low_id)) {
                let total = sub(base.net, low.net).map_err(overflow)?;
                savings = Some(Savings {
                    baseline_option_id: base_id.to_owned(),
                    option_id: low_id.clone(),
                    immediate_micro_usd: MicroUsd(
                        sub(base.immediate, low.immediate).map_err(overflow)?,
                    ),
                    estimated_rewards_micro_usd: MicroUsd(
                        sub(low.rewards, base.rewards).map_err(overflow)?,
                    ),
                    estimated_total_micro_usd: MicroUsd(total),
                    threshold_micro_usd: MicroUsd(threshold),
                    meets_threshold: total >= threshold,
                });
            }
        }
    }

    let expires_at = eligible
        .iter()
        .flat_map(|(e, _)| {
            std::iter::once(e.result.quote_valid_until)
                .chain(e.result.funding_step.as_ref().map(|s| s.valid_until))
        })
        .min();

    // Assumptions: one global note when any reward is modelled, then per option.
    let mut assumptions = Vec::new();
    if req.options.iter().any(|o| !o.rewards.is_empty()) {
        assumptions.push(explanation(
            ExplanationCode::RewardBaseExcludesFees,
            None,
            &[],
        ));
    }
    let mut sensitivities = Vec::new();
    if let Some(at) = expires_at {
        sensitivities.push(explanation(
            ExplanationCode::QuoteExpiry,
            None,
            &[("expires_at", at.to_string())],
        ));
    }
    if let (Outcome::Preferred, Some(pref)) = (decision.outcome, decision.preferred.as_deref()) {
        if let Some(pref_cost) = cost_of(pref) {
            let runner_up = by_net
                .iter()
                .filter(|(id, _)| *id != pref)
                .min_by_key(|(_, cost)| *cost);
            if let Some((runner_id, runner_cost)) = runner_up {
                sensitivities.push(explanation(
                    ExplanationCode::Margin,
                    Some(pref),
                    &[
                        ("runner_up_option_id", (*runner_id).to_owned()),
                        (
                            "margin_micro_usd",
                            sub(*runner_cost, pref_cost.net)
                                .map_err(overflow)?
                                .to_string(),
                        ),
                    ],
                ));
            }
        }
    }
    if immediate_only.outcome != decision.outcome || immediate_only.preferred != decision.preferred
    {
        sensitivities.push(explanation(
            ExplanationCode::DependsOnEstimatedRewards,
            decision.preferred.as_deref(),
            &[
                ("immediate_only_outcome", snake(immediate_only.outcome)),
                (
                    "immediate_only_preferred_option_id",
                    immediate_only.preferred.clone().unwrap_or_default(),
                ),
            ],
        ));
    }
    if let Some(s) = savings.as_ref().filter(|s| !s.meets_threshold) {
        sensitivities.push(explanation(
            ExplanationCode::SavingsBelowThreshold,
            Some(&s.option_id),
            &[
                (
                    "estimated_total_micro_usd",
                    s.estimated_total_micro_usd.to_string(),
                ),
                ("threshold_micro_usd", s.threshold_micro_usd.to_string()),
            ],
        ));
    }
    for e in &evals {
        assumptions.extend(e.assumptions.iter().cloned());
        if e.result.status == OptionStatus::Eligible {
            sensitivities.extend(e.sensitivities.iter().cloned());
        }
        if e.missing_data_only {
            let codes: Vec<String> = e.result.exclusions.iter().map(|x| snake(x.code)).collect();
            sensitivities.push(explanation(
                ExplanationCode::ExcludedOptionCouldChangeResult,
                Some(&e.result.option_id),
                &[("exclusion_codes", codes.join(","))],
            ));
        }
    }

    Ok(EvaluationResponse {
        contract_version: CONTRACT_VERSION.to_owned(),
        engine_version: ENGINE_VERSION.to_owned(),
        formula_version: FORMULA_VERSION.to_owned(),
        rounding_version: ROUNDING_VERSION.to_owned(),
        policy_version: req.policy.policy_version.clone(),
        preference_version: req.preferences.preference_version.clone(),
        evaluated_at: req.evaluated_at,
        basket_id: req.basket.basket_id.clone(),
        outcome: decision.outcome,
        rationale: decision.rationale,
        preferred_option_id: decision.preferred,
        tied_option_ids: decision.tied,
        lowest_cost_option_ids: decision.lowest,
        baseline_option_id: baseline.map(str::to_owned),
        savings_vs_baseline: savings,
        expires_at,
        options: evals.into_iter().map(|e| e.result).collect(),
        assumptions,
        sensitivities,
    })
}

#[derive(Debug, PartialEq, Eq)]
struct Decision {
    outcome: Outcome,
    rationale: Rationale,
    preferred: Option<String>,
    tied: Vec<String>,
    lowest: Vec<String>,
}

/// Chooses among eligible options by comparable cost.
///
/// The customer's default is kept unless another option is cheaper by at least
/// `threshold`. Exactly equal lowest costs are a tie, never an arbitrary pick.
fn decide(
    candidates: &[(&str, i128)],
    default: Option<&str>,
    threshold: i128,
    any_missing_data: bool,
) -> Result<Decision, Overflow> {
    let best = candidates.iter().map(|(_, cost)| *cost).min();
    let lowest: Vec<String> = candidates
        .iter()
        .filter(|(_, cost)| Some(*cost) == best)
        .map(|(id, _)| (*id).to_owned())
        .collect();
    let none = |outcome, rationale, lowest| Decision {
        outcome,
        rationale,
        preferred: None,
        tied: vec![],
        lowest,
    };

    let Some(best) = best else {
        return Ok(if any_missing_data {
            none(
                Outcome::InsufficientData,
                Rationale::OptionsMissingData,
                lowest,
            )
        } else {
            none(
                Outcome::NoEligibleOption,
                Rationale::NoEligibleOption,
                lowest,
            )
        });
    };
    if any_missing_data {
        return Ok(none(
            Outcome::InsufficientData,
            Rationale::OptionsMissingData,
            lowest,
        ));
    }

    let prefer = |id: &str, rationale, lowest| Decision {
        outcome: Outcome::Preferred,
        rationale,
        preferred: Some(id.to_owned()),
        tied: vec![],
        lowest,
    };
    let tie = |lowest: Vec<String>| Decision {
        outcome: Outcome::Tie,
        rationale: Rationale::EqualNetCost,
        preferred: None,
        tied: lowest.clone(),
        lowest,
    };

    if candidates.len() == 1 {
        return Ok(prefer(
            candidates[0].0,
            Rationale::OnlyEligibleOption,
            lowest,
        ));
    }

    let baseline = default.and_then(|d| candidates.iter().find(|(id, _)| *id == d));
    match baseline {
        Some((base_id, base_cost)) => {
            if lowest.iter().any(|id| id == base_id) {
                if lowest.len() == 1 {
                    Ok(prefer(base_id, Rationale::DefaultIsLowestCost, lowest))
                } else {
                    Ok(tie(lowest))
                }
            } else if sub(*base_cost, best)? < threshold {
                Ok(prefer(base_id, Rationale::SavingsBelowThreshold, lowest))
            } else if lowest.len() == 1 {
                let id = lowest[0].clone();
                Ok(prefer(&id, Rationale::SavingsMeetThreshold, lowest))
            } else {
                Ok(tie(lowest))
            }
        }
        None if lowest.len() == 1 => {
            let id = lowest[0].clone();
            Ok(prefer(&id, Rationale::LowestCostNoDefault, lowest))
        }
        None => Ok(tie(lowest)),
    }
}

fn evaluate_option(req: &EvaluationRequest, opt: &PaymentOption) -> OptionEval {
    use ExclusionCode as X;

    let mut exclusions: Vec<Exclusion> = Vec::new();
    let now = req.evaluated_at;
    let quote = &opt.quote;

    // Availability and consent.
    let availability = &opt.availability;
    if !availability.method_available {
        exclusions.push(exclusion(X::MethodUnavailable, &[]));
    }
    match availability.jurisdiction {
        Verification::Verified => {}
        Verification::Unverified => exclusions.push(exclusion(X::JurisdictionUnverified, &[])),
        Verification::Rejected => exclusions.push(exclusion(X::UnsupportedJurisdiction, &[])),
    }
    match availability.merchant_acceptance {
        Verification::Verified => {}
        Verification::Unverified => {
            exclusions.push(exclusion(X::MerchantAcceptanceUnverified, &[]))
        }
        Verification::Rejected => exclusions.push(exclusion(X::MerchantDoesNotAccept, &[])),
    }
    if !availability.customer_consent {
        exclusions.push(exclusion(X::NotConsented, &[]));
    }

    // Customer hard constraints.
    let constraints = &req.preferences.hard_constraints;
    if let Some(allowed) = &constraints.allowed_rails {
        if !allowed.contains(&opt.rail) {
            exclusions.push(exclusion(X::RailNotAllowed, &[("rail", snake(opt.rail))]));
        }
    }
    if constraints.require_purchase_protection && opt.tradeoffs.purchase_protection != Tri::Yes {
        exclusions.push(exclusion(
            X::PurchaseProtectionRequired,
            &[(
                "purchase_protection",
                snake(opt.tradeoffs.purchase_protection),
            )],
        ));
    }
    if constraints.avoid_credit && opt.tradeoffs.uses_credit {
        exclusions.push(exclusion(X::CreditNotAllowed, &[]));
    }

    // Quote freshness and comparability.
    if quote.valid_until <= now {
        exclusions.push(exclusion(
            X::StaleQuote,
            &[
                ("quote_id", quote.quote_id.clone()),
                ("valid_until", quote.valid_until.to_string()),
            ],
        ));
    }
    if quote.basket_hash != req.basket.basket_hash {
        exclusions.push(exclusion(
            X::BasketMismatch,
            &[("quote_id", quote.quote_id.clone())],
        ));
    }

    // Assets.
    let rail_asset_ok = match opt.rail {
        Rail::Card => opt.payment_asset.is_usd(),
        Rail::Stablecoin => req
            .policy
            .supported_stablecoins
            .contains(&opt.payment_asset),
    };
    if !rail_asset_ok || opt.payment_asset != quote.accepted_asset {
        exclusions.push(exclusion(
            X::IncompatibleAsset,
            &[
                ("payment_asset", describe_asset(&opt.payment_asset)),
                ("accepted_asset", describe_asset(&quote.accepted_asset)),
            ],
        ));
    }
    let valuer = Valuer {
        payment_asset: &opt.payment_asset,
        rate: quote.usd_rate.as_ref(),
    };
    let mut computable = true;
    if !valuer.can_value(&opt.payment_asset) || !valuer.can_value(&quote.delivered_price.asset) {
        exclusions.push(exclusion(
            X::RateUnavailable,
            &[("asset", describe_asset(&opt.payment_asset))],
        ));
        computable = false;
    }

    // Funding step.
    if let Some(step) = &opt.funding_step {
        if !req.policy.permitted_funding_sources.contains(&step.source) {
            exclusions.push(exclusion(
                X::FundingSourceNotPermitted,
                &[("source", snake(step.source))],
            ));
        }
        if !step.customer_consent {
            exclusions.push(exclusion(X::FundingNotConsented, &[]));
        }
        if step.valid_until <= now {
            exclusions.push(exclusion(
                X::StaleQuote,
                &[
                    ("quote_id", step.quote_id.clone()),
                    ("valid_until", step.valid_until.to_string()),
                ],
            ));
        }
    }

    // Fees: a required kind must be present, and nothing listed may be unknown.
    let mut required: BTreeSet<FeeKind> = match opt.rail {
        Rail::Card => req.policy.required_fee_kinds.card.iter().copied().collect(),
        Rail::Stablecoin => req
            .policy
            .required_fee_kinds
            .stablecoin
            .iter()
            .copied()
            .collect(),
    };
    if opt.funding_step.is_some() {
        required.extend(
            req.policy
                .required_fee_kinds
                .with_funding_step
                .iter()
                .copied(),
        );
    }
    for kind in &required {
        if !opt.fees.iter().any(|f| f.kind == *kind) {
            exclusions.push(exclusion(
                X::UnknownRequiredFee,
                &[("kind", snake(*kind)), ("reason", "missing".into())],
            ));
            computable = false;
        }
    }
    for fee in &opt.fees {
        let reason = match (&fee.status, &fee.amount) {
            (FeeStatus::Unknown, _) => Some("unknown"),
            (FeeStatus::Known, Some(amount)) if !valuer.can_value(&amount.asset) => {
                Some("unvaluable_asset")
            }
            _ => None,
        };
        if let Some(reason) = reason {
            exclusions.push(exclusion(
                X::UnknownRequiredFee,
                &[
                    ("kind", snake(fee.kind)),
                    ("fee_id", fee.fee_id.clone()),
                    ("reason", reason.into()),
                ],
            ));
            computable = false;
        }
    }

    // Amounts.
    let mut computed = None;
    if computable {
        match compute(req, opt, &valuer) {
            Ok(c) => computed = Some(c),
            Err(ValueError::Overflow) => exclusions.push(exclusion(X::AmountOutOfRange, &[])),
            Err(ValueError::Unvaluable) => exclusions.push(exclusion(
                X::RateUnavailable,
                &[("asset", describe_asset(&opt.payment_asset))],
            )),
        }
    }

    // Funds. Checked against the amount charged to the method, so it needs amounts.
    match (&opt.funds.status, &opt.funds.available) {
        (FundsStatus::Unknown, _) | (_, None) => {
            exclusions.push(exclusion(X::FundsUnverified, &[]))
        }
        (FundsStatus::Known, Some(available)) => {
            if let Some(c) = &computed {
                let reserve = opt.funds.reserve.as_ref().map_or(0, |r| r.base_units.get());
                let funding = opt
                    .funding_step
                    .as_ref()
                    .map_or(0, |s| s.amount.base_units.get());
                let own = (available.base_units.get() - reserve).max(0);
                match add(own, funding) {
                    Ok(spendable) if spendable >= c.breakdown.amount_due.base_units.get() => {}
                    Ok(spendable) => exclusions.push(exclusion(
                        X::InsufficientFunds,
                        &[
                            (
                                "amount_due_base_units",
                                c.breakdown.amount_due.base_units.to_string(),
                            ),
                            ("spendable_base_units", spendable.to_string()),
                        ],
                    )),
                    Err(Overflow) => exclusions.push(exclusion(X::AmountOutOfRange, &[])),
                }
            }
        }
    }

    let status = if exclusions.is_empty() {
        OptionStatus::Eligible
    } else {
        OptionStatus::Excluded
    };
    let missing_data_only = !exclusions.is_empty()
        && exclusions
            .iter()
            .all(|x| x.kind == ExclusionKind::MissingData);

    let (breakdown, costs, funding_fees, notes, assumptions, sensitivities) = match computed {
        Some(c) => (
            Some(c.breakdown),
            Some(c.costs),
            c.funding_fees,
            c.notes,
            c.assumptions,
            c.sensitivities,
        ),
        None => (None, None, 0, vec![], vec![], vec![]),
    };

    OptionEval {
        result: OptionResult {
            option_id: opt.option_id.clone(),
            rail: opt.rail,
            label: opt.label.clone(),
            payment_asset: opt.payment_asset.clone(),
            quote_id: quote.quote_id.clone(),
            quote_valid_until: quote.valid_until,
            status,
            exclusions,
            breakdown,
            funding_step: opt.funding_step.as_ref().map(|s| FundingStepResult {
                source: s.source,
                amount: s.amount.clone(),
                quote_id: s.quote_id.clone(),
                valid_until: s.valid_until,
                fees_micro_usd: MicroUsd(funding_fees),
            }),
            notes,
            tradeoffs: opt.tradeoffs.clone(),
        },
        costs,
        missing_data_only,
        assumptions,
        sensitivities,
    }
}

fn describe_asset(asset: &AssetRef) -> String {
    match &asset.network {
        Some(network) => format!("{}:{}:{}", asset.code, network, asset.decimals),
        None => format!("{}:{}", asset.code, asset.decimals),
    }
}

fn compute(
    req: &EvaluationRequest,
    opt: &PaymentOption,
    valuer: &Valuer<'_>,
) -> Result<Computed, ValueError> {
    let id = Some(opt.option_id.as_str());
    let mut notes = Vec::new();
    let mut assumptions = Vec::new();
    let mut sensitivities = Vec::new();

    let price = valuer.micro(&opt.quote.delivered_price, Round::Up)?;

    // Fees round up. Fees paid in the payment asset reduce the same balance,
    // except funding and conversion fees, which belong to the separate step.
    let mut fee_lines = Vec::new();
    let mut fees_total = 0i128;
    let mut funding_fees = 0i128;
    let mut charged_fees = 0i128;
    for fee in &opt.fees {
        let (FeeStatus::Known, Some(amount)) = (&fee.status, &fee.amount) else {
            continue;
        };
        let value = valuer.micro(amount, Round::Up)?;
        fees_total = add(fees_total, value)?;
        let funding_kind = matches!(fee.kind, FeeKind::Funding | FeeKind::Conversion);
        if funding_kind {
            funding_fees = add(funding_fees, value)?;
        } else if &amount.asset == valuer.payment_asset {
            charged_fees = add(charged_fees, value)?;
        }
        fee_lines.push(FeeLine {
            fee_id: fee.fee_id.clone(),
            kind: fee.kind,
            description: fee.description.clone(),
            source_amount: amount.clone(),
            micro_usd: MicroUsd(value),
        });
    }

    // Discounts round down, are counted once, and never exceed the price.
    let mut discount_lines = Vec::new();
    let mut discounts_total = 0i128;
    let mut seen = BTreeSet::new();
    for d in &opt.discounts {
        let params = [("discount_id", d.discount_id.clone())];
        if !seen.insert(d.discount_id.as_str()) {
            notes.push(explanation(
                ExplanationCode::DuplicateDiscountIgnored,
                id,
                &params,
            ));
            continue;
        }
        let skip = if d.included_in_quote {
            Some(ExplanationCode::DiscountAlreadyInQuote)
        } else if d.eligibility != Verification::Verified {
            Some(ExplanationCode::DiscountUnverified)
        } else if !d.immediately_usable {
            Some(ExplanationCode::DiscountNotImmediatelyUsable)
        } else if !valuer.can_value(&d.amount.asset) {
            Some(ExplanationCode::DiscountUnvaluable)
        } else {
            None
        };
        let mut value = 0i128;
        if let Some(code) = skip {
            notes.push(explanation(code, id, &params));
        } else {
            value = valuer.micro(&d.amount, Round::Down)?;
            let room = sub(price, discounts_total)?;
            if value > room {
                value = room;
                notes.push(explanation(
                    ExplanationCode::DiscountCappedAtPrice,
                    id,
                    &params,
                ));
            }
            discounts_total = add(discounts_total, value)?;
        }
        discount_lines.push(DiscountLine {
            discount_id: d.discount_id.clone(),
            description: d.description.clone(),
            applied: skip.is_none(),
            micro_usd: MicroUsd(value),
            not_applied_reason: skip,
        });
    }

    let discounted_price = sub(price, discounts_total)?;
    let immediate = add(discounted_price, fees_total)?;

    // Rewards round down and are earned on the discounted price, excluding fees.
    let mut reward_lines = Vec::new();
    let mut rewards_total = 0i128;
    for rule in &opt.rewards {
        let params = [("rule_id", rule.rule_id.clone())];
        let mut line = RewardLine {
            rule_id: rule.rule_id.clone(),
            rule_version: rule.rule_version.clone(),
            description: rule.description.clone(),
            points: None,
            micro_usd: MicroUsd(0),
            counted: false,
            cap_applied: false,
            not_counted_reason: None,
        };
        if rule.eligibility != Verification::Verified {
            line.not_counted_reason = Some(ExplanationCode::RewardUnverified);
            notes.push(explanation(ExplanationCode::RewardUnverified, id, &params));
            reward_lines.push(line);
            continue;
        }
        let remaining = rule
            .cap
            .as_ref()
            .map(|cap| (cap.limit.get() - cap.consumed.get()).max(0));
        if remaining == Some(0) {
            if let Earn::Points { .. } = rule.earn {
                line.points = Some(Units::ZERO);
            }
            line.cap_applied = true;
            line.not_counted_reason = Some(ExplanationCode::RewardCapExhausted);
            notes.push(explanation(
                ExplanationCode::RewardCapExhausted,
                id,
                &params,
            ));
            reward_lines.push(line);
            continue;
        }
        match &rule.earn {
            Earn::Cashback { rate_bps } => {
                let mut value = div_round(
                    mul(discounted_price, rate_bps.get())?,
                    BPS_DENOMINATOR,
                    Round::Down,
                )?;
                // A cap too large to express in micro-USD cannot bind.
                let cap_micro =
                    remaining.map(|r| r.checked_mul(MICRO_PER_USD_MINOR).unwrap_or(i128::MAX));
                if let Some(cap_micro) = cap_micro.filter(|cap| value > *cap) {
                    value = cap_micro;
                    line.cap_applied = true;
                    notes.push(explanation(ExplanationCode::RewardCapApplied, id, &params));
                }
                line.micro_usd = MicroUsd(value);
                line.counted = true;
                rewards_total = add(rewards_total, value)?;
            }
            Earn::Points {
                program_id,
                milli_points_per_usd,
            } => {
                let mut points = div_round(
                    mul(discounted_price, milli_points_per_usd.get())?,
                    MICRO_PER_USD * MILLI_POINTS_PER_POINT,
                    Round::Down,
                )?;
                if let Some(cap_points) = remaining.filter(|cap| points > *cap) {
                    points = cap_points;
                    line.cap_applied = true;
                    notes.push(explanation(ExplanationCode::RewardCapApplied, id, &params));
                }
                line.points = Units::new(points);
                let valuation = req
                    .preferences
                    .point_valuations
                    .iter()
                    .find(|v| &v.program_id == program_id);
                let program = ("program_id", program_id.clone());
                match valuation {
                    None => {
                        line.not_counted_reason = Some(ExplanationCode::PointValuationMissing);
                        notes.push(explanation(
                            ExplanationCode::PointValuationMissing,
                            id,
                            &[params[0].clone(), program],
                        ));
                    }
                    Some(v) if v.stability == Stability::Unstable => {
                        line.not_counted_reason = Some(ExplanationCode::PointValuationUnstable);
                        notes.push(explanation(
                            ExplanationCode::PointValuationUnstable,
                            id,
                            &[params[0].clone(), program.clone()],
                        ));
                        let mut sens =
                            vec![params[0].clone(), program, ("points", points.to_string())];
                        if let Ok(hypothetical) = mul(points, v.micro_usd_per_point.get()) {
                            sens.push(("hypothetical_micro_usd", hypothetical.to_string()));
                        }
                        sensitivities.push(explanation(
                            ExplanationCode::UnstablePointValuationExcluded,
                            id,
                            &sens,
                        ));
                    }
                    Some(v) => {
                        let value = mul(points, v.micro_usd_per_point.get())?;
                        if value > MAX_COMPONENT_MICRO {
                            return Err(ValueError::Overflow);
                        }
                        line.micro_usd = MicroUsd(value);
                        line.counted = true;
                        rewards_total = add(rewards_total, value)?;
                        assumptions.push(explanation(
                            ExplanationCode::PointValuationUsed,
                            id,
                            &[
                                program,
                                ("micro_usd_per_point", v.micro_usd_per_point.to_string()),
                                ("valuation_version", v.valuation_version.clone()),
                            ],
                        ));
                    }
                }
            }
        }
        reward_lines.push(line);
    }

    let net = sub(immediate, rewards_total)?;

    let amount_due_units = valuer.payment_units(add(discounted_price, charged_fees)?, Round::Up)?;
    let amount_due = Amount {
        asset: opt.payment_asset.clone(),
        base_units: Units::new(amount_due_units).ok_or(ValueError::Overflow)?,
    };

    if let (false, Some(rate)) = (opt.payment_asset.is_usd(), valuer.rate) {
        assumptions.push(explanation(
            ExplanationCode::UsdRateUsed,
            id,
            &[
                ("asset", describe_asset(&opt.payment_asset)),
                ("usd_num", rate.usd_num.to_string()),
                ("usd_den", rate.usd_den.to_string()),
                ("quote_id", opt.quote.quote_id.clone()),
            ],
        ));
        assumptions.push(explanation(ExplanationCode::AmountDueIsUpperBound, id, &[]));
    }

    Ok(Computed {
        breakdown: Breakdown {
            delivered_price_micro_usd: MicroUsd(price),
            fees: fee_lines,
            fees_total_micro_usd: MicroUsd(fees_total),
            discounts: discount_lines,
            discounts_applied_total_micro_usd: MicroUsd(discounts_total),
            immediate_cost_micro_usd: MicroUsd(immediate),
            rewards: reward_lines,
            estimated_rewards_total_micro_usd: MicroUsd(rewards_total),
            estimated_net_cost_micro_usd: MicroUsd(net),
            amount_due,
        },
        costs: Costs {
            immediate,
            rewards: rewards_total,
            net,
        },
        funding_fees,
        notes,
        assumptions,
        sensitivities,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn d(candidates: &[(&str, i128)], default: Option<&str>, threshold: i128) -> Decision {
        decide(candidates, default, threshold, false).unwrap()
    }

    #[test]
    fn default_kept_when_it_is_cheapest() {
        let r = d(&[("card", 97), ("usdc", 98)], Some("card"), 0);
        assert_eq!(
            (r.outcome, r.rationale),
            (Outcome::Preferred, Rationale::DefaultIsLowestCost)
        );
        assert_eq!(r.preferred.as_deref(), Some("card"));
    }

    #[test]
    fn threshold_boundary_is_inclusive() {
        let below = d(&[("card", 100), ("usdc", 51)], Some("card"), 50);
        assert_eq!(below.rationale, Rationale::SavingsBelowThreshold);
        assert_eq!(below.preferred.as_deref(), Some("card"));
        assert_eq!(below.lowest, vec!["usdc"]);

        let at = d(&[("card", 100), ("usdc", 50)], Some("card"), 50);
        assert_eq!(at.rationale, Rationale::SavingsMeetThreshold);
        assert_eq!(at.preferred.as_deref(), Some("usdc"));
    }

    #[test]
    fn equal_costs_are_a_tie_even_with_a_default() {
        let r = d(&[("card", 100), ("usdc", 100)], Some("card"), 0);
        assert_eq!(r.outcome, Outcome::Tie);
        assert_eq!(r.preferred, None);
        assert_eq!(r.tied, vec!["card", "usdc"]);
    }

    #[test]
    fn tie_between_cheaper_alternatives_stays_a_tie() {
        let r = d(&[("card", 100), ("a", 10), ("b", 10)], Some("card"), 50);
        assert_eq!(r.outcome, Outcome::Tie);
        assert_eq!(r.tied, vec!["a", "b"]);
        let kept = d(&[("card", 100), ("a", 60), ("b", 60)], Some("card"), 50);
        assert_eq!(kept.preferred.as_deref(), Some("card"));
    }

    #[test]
    fn no_default_picks_unique_lowest() {
        let r = d(&[("card", 100), ("usdc", 99)], None, 1_000);
        assert_eq!(
            (r.rationale, r.preferred.as_deref()),
            (Rationale::LowestCostNoDefault, Some("usdc"))
        );
    }

    #[test]
    fn missing_data_blocks_a_recommendation() {
        let r = decide(&[("card", 100)], Some("card"), 0, true).unwrap();
        assert_eq!((r.outcome, r.preferred), (Outcome::InsufficientData, None));
        let none = decide(&[], None, 0, false).unwrap();
        assert_eq!(none.outcome, Outcome::NoEligibleOption);
        let none_missing = decide(&[], None, 0, true).unwrap();
        assert_eq!(none_missing.outcome, Outcome::InsufficientData);
    }

    #[test]
    fn snake_matches_wire_names() {
        assert_eq!(
            snake(ExclusionCode::UnknownRequiredFee),
            "unknown_required_fee"
        );
        assert_eq!(snake(Outcome::InsufficientData), "insufficient_data");
        assert_eq!(snake(FeeKind::Fx), "fx");
        assert_eq!(
            serde_json::to_value(ExclusionCode::MerchantAcceptanceUnverified).unwrap(),
            serde_json::Value::String(snake(ExclusionCode::MerchantAcceptanceUnverified))
        );
    }
}
