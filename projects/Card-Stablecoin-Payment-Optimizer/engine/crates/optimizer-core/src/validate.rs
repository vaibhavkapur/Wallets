//! Structural validation of a request. Anything rejected here is malformed
//! input, not a property of one payment option.

use std::collections::{BTreeMap, BTreeSet};

use crate::contract::*;
use crate::error::{RequestError, RequestErrorCode as Code};
use crate::CONTRACT_VERSION;

/// Upper bounds that keep an evaluation small and its work predictable.
pub const MAX_OPTIONS: usize = 16;
pub const MAX_LINES: usize = 64;
pub const MAX_DECIMALS: u8 = 18;

fn err(code: Code, field: impl Into<String>, message: impl Into<String>) -> RequestError {
    RequestError::new(code, field, message)
}

fn check_asset(asset: &AssetRef, field: &str) -> Result<(), RequestError> {
    if asset.code.is_empty() {
        return Err(err(Code::InvalidAsset, field, "asset code is empty"));
    }
    if asset.decimals > MAX_DECIMALS {
        return Err(err(Code::InvalidAsset, field, "asset decimals exceed 18"));
    }
    if asset.code == "USD" && asset.network.is_none() && asset.decimals != 2 {
        return Err(err(
            Code::InvalidAsset,
            field,
            "fiat USD must use 2 decimals",
        ));
    }
    if asset.network.as_deref() == Some("") {
        return Err(err(Code::InvalidAsset, field, "asset network is empty"));
    }
    Ok(())
}

pub fn validate(req: &EvaluationRequest) -> Result<(), RequestError> {
    if req.contract_version != CONTRACT_VERSION {
        return Err(err(
            Code::UnsupportedContractVersion,
            "/contract_version",
            format!("this engine implements contract version {CONTRACT_VERSION}"),
        ));
    }
    if req.policy.comparison_currency != "USD" {
        return Err(err(
            Code::UnsupportedComparisonCurrency,
            "/policy/comparison_currency",
            "contract version 1 compares in USD only",
        ));
    }
    for (i, asset) in req.policy.supported_stablecoins.iter().enumerate() {
        let field = format!("/policy/supported_stablecoins/{i}");
        check_asset(asset, &field)?;
        if asset.network.is_none() {
            return Err(err(
                Code::InvalidAsset,
                field,
                "a stablecoin must name its network",
            ));
        }
    }

    let prefs = &req.preferences;
    if !prefs.min_savings_threshold.asset.is_usd() {
        return Err(err(
            Code::InvalidThreshold,
            "/preferences/min_savings_threshold",
            "the savings threshold must be in USD minor units",
        ));
    }
    let mut programs = BTreeSet::new();
    for (i, v) in prefs.point_valuations.iter().enumerate() {
        if v.program_id.is_empty() || !programs.insert(v.program_id.as_str()) {
            return Err(err(
                Code::InvalidPointValuation,
                format!("/preferences/point_valuations/{i}"),
                "program_id must be non-empty and unique",
            ));
        }
    }

    if req.options.is_empty() || req.options.len() > MAX_OPTIONS {
        return Err(err(
            Code::InvalidOptions,
            "/options",
            format!("between 1 and {MAX_OPTIONS} options are required"),
        ));
    }
    let mut ids = BTreeSet::new();
    for (i, opt) in req.options.iter().enumerate() {
        if opt.option_id.is_empty() || !ids.insert(opt.option_id.as_str()) {
            return Err(err(
                Code::DuplicateOptionId,
                format!("/options/{i}/option_id"),
                "option_id must be non-empty and unique",
            ));
        }
        validate_option(opt, &format!("/options/{i}"))?;
    }
    if let Some(default) = &prefs.default_option_id {
        if !ids.contains(default.as_str()) {
            return Err(err(
                Code::UnknownDefaultOption,
                "/preferences/default_option_id",
                "the default option is not among the options",
            ));
        }
    }
    Ok(())
}

fn validate_option(opt: &PaymentOption, at: &str) -> Result<(), RequestError> {
    check_asset(&opt.payment_asset, &format!("{at}/payment_asset"))?;

    let q = &opt.quote;
    check_asset(&q.accepted_asset, &format!("{at}/quote/accepted_asset"))?;
    check_asset(
        &q.delivered_price.asset,
        &format!("{at}/quote/delivered_price/asset"),
    )?;
    if q.delivered_price.base_units.get() == 0 {
        return Err(err(
            Code::InvalidAmount,
            format!("{at}/quote/delivered_price/base_units"),
            "the delivered price must be greater than zero",
        ));
    }
    if q.valid_until < q.obtained_at {
        return Err(err(
            Code::InvalidQuote,
            format!("{at}/quote/valid_until"),
            "a quote cannot expire before it was obtained",
        ));
    }
    if let Some(rate) = &q.usd_rate {
        if rate.usd_num.get() == 0 || rate.usd_den.get() == 0 {
            return Err(err(
                Code::InvalidRate,
                format!("{at}/quote/usd_rate"),
                "rate numerator and denominator must be greater than zero",
            ));
        }
    }

    for (name, len) in [
        ("fees", opt.fees.len()),
        ("discounts", opt.discounts.len()),
        ("rewards", opt.rewards.len()),
    ] {
        if len > MAX_LINES {
            return Err(err(
                Code::InvalidOptions,
                format!("{at}/{name}"),
                format!("at most {MAX_LINES} entries are allowed"),
            ));
        }
    }

    let mut fee_ids = BTreeSet::new();
    for (i, fee) in opt.fees.iter().enumerate() {
        let field = format!("{at}/fees/{i}");
        if fee.fee_id.is_empty() || !fee_ids.insert(fee.fee_id.as_str()) {
            return Err(err(
                Code::InvalidFee,
                field,
                "fee_id must be non-empty and unique",
            ));
        }
        match (fee.status, &fee.amount) {
            (FeeStatus::Known, Some(amount)) => {
                check_asset(&amount.asset, &format!("{field}/amount/asset"))?
            }
            (FeeStatus::Known, None) => {
                return Err(err(
                    Code::InvalidFee,
                    field,
                    "a known fee requires an amount",
                ));
            }
            (_, Some(_)) => {
                return Err(err(
                    Code::InvalidFee,
                    field,
                    "an unknown or not applicable fee must not carry an amount",
                ));
            }
            (_, None) => {}
        }
    }

    let mut discounts: BTreeMap<&str, &Discount> = BTreeMap::new();
    for (i, d) in opt.discounts.iter().enumerate() {
        let field = format!("{at}/discounts/{i}");
        check_asset(&d.amount.asset, &format!("{field}/amount/asset"))?;
        if d.discount_id.is_empty() {
            return Err(err(Code::InvalidAmount, field, "discount_id is empty"));
        }
        if let Some(first) = discounts.insert(d.discount_id.as_str(), d) {
            if first != d {
                return Err(err(
                    Code::ConflictingDuplicateDiscount,
                    field,
                    "the same discount_id appears with different contents",
                ));
            }
        }
    }

    let mut rule_ids = BTreeSet::new();
    for (i, r) in opt.rewards.iter().enumerate() {
        let field = format!("{at}/rewards/{i}");
        if r.rule_id.is_empty() || !rule_ids.insert(r.rule_id.as_str()) {
            return Err(err(
                Code::InvalidReward,
                field,
                "rule_id must be non-empty and unique",
            ));
        }
        match &r.earn {
            Earn::Cashback { rate_bps } if rate_bps.get() > 10_000 => {
                return Err(err(
                    Code::InvalidReward,
                    field,
                    "cashback rate exceeds 100%",
                ));
            }
            Earn::Points { program_id, .. } if program_id.is_empty() => {
                return Err(err(Code::InvalidReward, field, "program_id is empty"));
            }
            _ => {}
        }
    }

    let funds = &opt.funds;
    match (funds.status, &funds.available) {
        (FundsStatus::Known, None) => {
            return Err(err(
                Code::InvalidFunds,
                format!("{at}/funds/available"),
                "known funds require an available amount",
            ));
        }
        (FundsStatus::Unknown, Some(_)) => {
            return Err(err(
                Code::InvalidFunds,
                format!("{at}/funds/available"),
                "unknown funds must not carry an available amount",
            ));
        }
        _ => {}
    }
    for (name, amount) in [("available", &funds.available), ("reserve", &funds.reserve)] {
        if let Some(amount) = amount {
            if amount.asset != opt.payment_asset {
                return Err(err(
                    Code::InvalidFunds,
                    format!("{at}/funds/{name}/asset"),
                    "funds must be denominated in the payment asset",
                ));
            }
        }
    }

    if let Some(step) = &opt.funding_step {
        if step.amount.asset != opt.payment_asset || step.amount.base_units.get() == 0 {
            return Err(err(
                Code::InvalidFundingStep,
                format!("{at}/funding_step/amount"),
                "a funding step must add a positive amount of the payment asset",
            ));
        }
    }
    Ok(())
}
