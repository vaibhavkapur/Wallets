//! Versioned evaluation contract (v1): the request the orchestrator sends and
//! the response the engine returns. Unknown fields are rejected so a misspelled
//! fee or discount can never be silently ignored.

use std::borrow::Cow;
use std::collections::BTreeMap;
use std::fmt;

use schemars::{json_schema, JsonSchema, Schema, SchemaGenerator};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use time::format_description::well_known::Rfc3339;
use time::{OffsetDateTime, UtcOffset};

use crate::amount::{MicroUsd, Units};

// ---------------------------------------------------------------------------
// Shared primitives
// ---------------------------------------------------------------------------

/// RFC 3339 instant. Serialized in UTC.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Timestamp(pub OffsetDateTime);

impl Timestamp {
    pub fn parse(s: &str) -> Result<Timestamp, time::error::Parse> {
        OffsetDateTime::parse(s, &Rfc3339).map(Timestamp)
    }
}

impl fmt::Display for Timestamp {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let utc = self.0.to_offset(UtcOffset::UTC);
        f.write_str(&utc.format(&Rfc3339).map_err(|_| fmt::Error)?)
    }
}

impl Serialize for Timestamp {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for Timestamp {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        let s = String::deserialize(d)?;
        Timestamp::parse(&s).map_err(serde::de::Error::custom)
    }
}

impl JsonSchema for Timestamp {
    fn schema_name() -> Cow<'static, str> {
        "Timestamp".into()
    }
    fn json_schema(_: &mut SchemaGenerator) -> Schema {
        json_schema!({ "type": "string", "format": "date-time" })
    }
}

/// An asset with explicit network and decimals. Fiat has no network.
#[derive(Clone, Debug, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct AssetRef {
    pub code: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub network: Option<String>,
    pub decimals: u8,
}

impl AssetRef {
    pub fn usd() -> AssetRef {
        AssetRef {
            code: "USD".into(),
            network: None,
            decimals: 2,
        }
    }

    pub fn is_usd(&self) -> bool {
        self.code == "USD" && self.network.is_none() && self.decimals == 2
    }
}

/// Integer base units of an explicit asset.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Amount {
    pub asset: AssetRef,
    pub base_units: Units,
}

/// USD value of one whole unit of an asset, as the exact ratio `usd_num / usd_den`.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Rate {
    pub usd_num: Units,
    pub usd_den: Units,
}

#[derive(
    Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "snake_case")]
pub enum Rail {
    Card,
    Stablecoin,
}

/// Result of an eligibility check performed upstream. `unverified` is never
/// treated as a pass.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Verification {
    Verified,
    Unverified,
    Rejected,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Tri {
    Yes,
    No,
    Unknown,
}

// ---------------------------------------------------------------------------
// Request
// ---------------------------------------------------------------------------

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct EvaluationRequest {
    /// Must equal the engine's contract version ("1").
    pub contract_version: String,
    /// Supplied by the caller so that an evaluation is reproducible.
    pub evaluated_at: Timestamp,
    pub basket: Basket,
    pub policy: Policy,
    pub preferences: Preferences,
    pub options: Vec<PaymentOption>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Basket {
    pub basket_id: String,
    pub merchant_id: String,
    /// Identifies the exact basket contents; every quote must carry the same hash.
    pub basket_hash: String,
}

/// Platform policy for an evaluation. Versioned and recorded with every result.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    pub policy_version: String,
    /// Must be "USD" in contract v1.
    pub comparison_currency: String,
    /// Stablecoin assets (code, network, decimals) that may be compared.
    pub supported_stablecoins: Vec<AssetRef>,
    /// Fee kinds that must be known or explicitly not applicable, per rail.
    pub required_fee_kinds: RequiredFeeKinds,
    /// Funding sources that may be shown as a separate quoted step.
    pub permitted_funding_sources: Vec<FundingSource>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct RequiredFeeKinds {
    pub card: Vec<FeeKind>,
    pub stablecoin: Vec<FeeKind>,
    /// Additionally required whenever an option includes a funding step.
    pub with_funding_step: Vec<FeeKind>,
}

/// Customer preferences. Versioned and recorded with every result.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Preferences {
    pub preference_version: String,
    /// The customer's stated default method; the baseline for savings.
    #[serde(default)]
    pub default_option_id: Option<String>,
    /// Minimum estimated saving (USD) before recommending a switch away from the default.
    pub min_savings_threshold: Amount,
    pub hard_constraints: HardConstraints,
    #[serde(default)]
    pub point_valuations: Vec<PointValuation>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct HardConstraints {
    /// When present, only these rails may be recommended.
    #[serde(default)]
    pub allowed_rails: Option<Vec<Rail>>,
    #[serde(default)]
    pub require_purchase_protection: bool,
    #[serde(default)]
    pub avoid_credit: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct PointValuation {
    pub program_id: String,
    pub valuation_version: String,
    /// Customer's conservative value of one point, in micro-USD.
    pub micro_usd_per_point: Units,
    pub stability: Stability,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Stability {
    Stable,
    Unstable,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct PaymentOption {
    pub option_id: String,
    pub rail: Rail,
    pub label: String,
    /// The asset the customer would pay with.
    pub payment_asset: AssetRef,
    pub quote: RailQuote,
    pub availability: Availability,
    #[serde(default)]
    pub fees: Vec<Fee>,
    #[serde(default)]
    pub discounts: Vec<Discount>,
    #[serde(default)]
    pub rewards: Vec<RewardRule>,
    pub funds: Funds,
    #[serde(default)]
    pub funding_step: Option<FundingStep>,
    pub tradeoffs: Tradeoffs,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct RailQuote {
    pub quote_id: String,
    pub provider: String,
    pub basket_hash: String,
    /// Delivered price (items, shipping, tax) for this rail before payment-specific adjustments.
    pub delivered_price: Amount,
    /// The asset the merchant accepts on this rail.
    pub accepted_asset: AssetRef,
    /// Required to value a non-USD payment asset.
    #[serde(default)]
    pub usd_rate: Option<Rate>,
    pub obtained_at: Timestamp,
    pub valid_until: Timestamp,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Availability {
    pub method_available: bool,
    pub jurisdiction: Verification,
    pub merchant_acceptance: Verification,
    pub customer_consent: bool,
}

#[derive(
    Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "snake_case")]
pub enum FeeKind {
    PaymentSurcharge,
    Fx,
    Funding,
    Conversion,
    Network,
    Other,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum FeeStatus {
    Known,
    Unknown,
    NotApplicable,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Fee {
    pub fee_id: String,
    pub kind: FeeKind,
    pub status: FeeStatus,
    /// Required when `status` is `known`; must be absent otherwise.
    #[serde(default)]
    pub amount: Option<Amount>,
    #[serde(default)]
    pub description: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Discount {
    pub discount_id: String,
    pub amount: Amount,
    /// True when the merchant quote's delivered price already reflects this discount.
    pub included_in_quote: bool,
    /// False for benefits that cannot reduce this payment (e.g. credit on a future order).
    pub immediately_usable: bool,
    pub eligibility: Verification,
    #[serde(default)]
    pub description: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct RewardRule {
    pub rule_id: String,
    pub rule_version: String,
    pub eligibility: Verification,
    pub earn: Earn,
    #[serde(default)]
    pub cap: Option<RewardCap>,
    #[serde(default)]
    pub description: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum Earn {
    /// Cash back as basis points of the reward base.
    Cashback { rate_bps: Units },
    /// Points earned per USD of the reward base, in thousandths of a point.
    Points {
        program_id: String,
        milli_points_per_usd: Units,
    },
}

/// Period cap. Units are USD minor units for cashback and whole points for points.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct RewardCap {
    pub limit: Units,
    pub consumed: Units,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum FundsStatus {
    Known,
    Unknown,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Funds {
    pub status: FundsStatus,
    /// Spendable balance or available credit in the payment asset. Required when known.
    #[serde(default)]
    pub available: Option<Amount>,
    /// Liquidity the customer wants left untouched, in the payment asset.
    #[serde(default)]
    pub reserve: Option<Amount>,
}

#[derive(
    Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "snake_case")]
pub enum FundingSource {
    BankTransfer,
    DebitCard,
    CreditCard,
    FiatBalance,
}

/// Optional funding or conversion, shown to the customer as a separate quoted step.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct FundingStep {
    pub source: FundingSource,
    /// Amount added to the spendable balance, in the payment asset.
    pub amount: Amount,
    pub quote_id: String,
    pub valid_until: Timestamp,
    pub customer_consent: bool,
}

/// Non-price considerations. Displayed as trade-offs; never converted to cash values.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Tradeoffs {
    pub purchase_protection: Tri,
    pub uses_credit: bool,
    pub refund_asset: String,
    pub refund_timing: String,
    pub settlement: String,
    #[serde(default)]
    pub notes: Vec<String>,
}

// ---------------------------------------------------------------------------
// Response
// ---------------------------------------------------------------------------

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct EvaluationResponse {
    pub contract_version: String,
    pub engine_version: String,
    pub formula_version: String,
    pub rounding_version: String,
    pub policy_version: String,
    pub preference_version: String,
    pub evaluated_at: Timestamp,
    pub basket_id: String,
    pub outcome: Outcome,
    pub rationale: Rationale,
    pub preferred_option_id: Option<String>,
    pub tied_option_ids: Vec<String>,
    /// Eligible option(s) with the lowest estimated net cost, before the savings threshold.
    pub lowest_cost_option_ids: Vec<String>,
    /// The customer's default, when it is eligible.
    pub baseline_option_id: Option<String>,
    /// Lowest-cost option compared with the baseline, when they differ.
    pub savings_vs_baseline: Option<Savings>,
    /// Earliest expiry among the quotes the result depends on.
    pub expires_at: Option<Timestamp>,
    pub options: Vec<OptionResult>,
    pub assumptions: Vec<Explanation>,
    /// What could change the result.
    pub sensitivities: Vec<Explanation>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Preferred,
    Tie,
    InsufficientData,
    NoEligibleOption,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Rationale {
    DefaultIsLowestCost,
    SavingsMeetThreshold,
    SavingsBelowThreshold,
    LowestCostNoDefault,
    OnlyEligibleOption,
    EqualNetCost,
    OptionsMissingData,
    NoEligibleOption,
}

/// Estimated saving of `option_id` relative to the customer's default. Immediate
/// amounts and estimated rewards are reported separately.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct Savings {
    pub baseline_option_id: String,
    pub option_id: String,
    /// Baseline immediate cost minus this option's immediate cost.
    pub immediate_micro_usd: MicroUsd,
    /// This option's estimated rewards minus the baseline's.
    pub estimated_rewards_micro_usd: MicroUsd,
    /// Sum of the two; compared with the threshold.
    pub estimated_total_micro_usd: MicroUsd,
    pub threshold_micro_usd: MicroUsd,
    pub meets_threshold: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum OptionStatus {
    Eligible,
    Excluded,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct OptionResult {
    pub option_id: String,
    pub rail: Rail,
    pub label: String,
    pub payment_asset: AssetRef,
    pub quote_id: String,
    pub quote_valid_until: Timestamp,
    pub status: OptionStatus,
    pub exclusions: Vec<Exclusion>,
    /// Present whenever the amounts could be computed, including for excluded
    /// options (shown for information only).
    pub breakdown: Option<Breakdown>,
    pub funding_step: Option<FundingStepResult>,
    pub notes: Vec<Explanation>,
    pub tradeoffs: Tradeoffs,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct Exclusion {
    pub code: ExclusionCode,
    pub kind: ExclusionKind,
    pub params: BTreeMap<String, String>,
}

/// `missing_data` exclusions could be resolved by better or fresher data, so
/// the evaluation reports insufficient data instead of a recommendation.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum ExclusionKind {
    Definite,
    MissingData,
}

#[derive(
    Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "snake_case")]
pub enum ExclusionCode {
    MethodUnavailable,
    UnsupportedJurisdiction,
    JurisdictionUnverified,
    MerchantDoesNotAccept,
    MerchantAcceptanceUnverified,
    NotConsented,
    RailNotAllowed,
    PurchaseProtectionRequired,
    CreditNotAllowed,
    StaleQuote,
    BasketMismatch,
    IncompatibleAsset,
    RateUnavailable,
    FundingSourceNotPermitted,
    FundingNotConsented,
    UnknownRequiredFee,
    AmountOutOfRange,
    FundsUnverified,
    InsufficientFunds,
}

impl ExclusionCode {
    pub fn kind(self) -> ExclusionKind {
        use ExclusionCode::*;
        match self {
            JurisdictionUnverified
            | MerchantAcceptanceUnverified
            | StaleQuote
            | BasketMismatch
            | RateUnavailable
            | UnknownRequiredFee
            | AmountOutOfRange
            | FundsUnverified => ExclusionKind::MissingData,
            MethodUnavailable
            | UnsupportedJurisdiction
            | MerchantDoesNotAccept
            | NotConsented
            | RailNotAllowed
            | PurchaseProtectionRequired
            | CreditNotAllowed
            | IncompatibleAsset
            | FundingSourceNotPermitted
            | FundingNotConsented
            | InsufficientFunds => ExclusionKind::Definite,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct Breakdown {
    pub delivered_price_micro_usd: MicroUsd,
    pub fees: Vec<FeeLine>,
    pub fees_total_micro_usd: MicroUsd,
    pub discounts: Vec<DiscountLine>,
    pub discounts_applied_total_micro_usd: MicroUsd,
    /// Delivered price plus fees minus applied discounts. No estimates.
    pub immediate_cost_micro_usd: MicroUsd,
    pub rewards: Vec<RewardLine>,
    /// Conservatively valued future rewards. An estimate, not a realized saving.
    pub estimated_rewards_total_micro_usd: MicroUsd,
    /// Immediate cost minus estimated rewards.
    pub estimated_net_cost_micro_usd: MicroUsd,
    /// Upper bound on what is charged to the payment method, in its own asset.
    pub amount_due: Amount,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct FeeLine {
    pub fee_id: String,
    pub kind: FeeKind,
    pub description: String,
    pub source_amount: Amount,
    pub micro_usd: MicroUsd,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct DiscountLine {
    pub discount_id: String,
    pub description: String,
    pub applied: bool,
    /// Amount subtracted from the cost; zero when not applied.
    pub micro_usd: MicroUsd,
    pub not_applied_reason: Option<ExplanationCode>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct RewardLine {
    pub rule_id: String,
    pub rule_version: String,
    pub description: String,
    /// Points earned after caps; absent for cashback.
    pub points: Option<Units>,
    /// Value counted in the estimated net cost; zero when not counted.
    pub micro_usd: MicroUsd,
    pub counted: bool,
    pub cap_applied: bool,
    pub not_counted_reason: Option<ExplanationCode>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct FundingStepResult {
    pub source: FundingSource,
    pub amount: Amount,
    pub quote_id: String,
    pub valid_until: Timestamp,
    /// Funding and conversion fees for this step; included in the fee total.
    pub fees_micro_usd: MicroUsd,
}

/// Machine-readable explanation component. The interface renders these; it
/// never recomputes them.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct Explanation {
    pub code: ExplanationCode,
    pub option_id: Option<String>,
    pub params: BTreeMap<String, String>,
}

#[derive(
    Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "snake_case")]
pub enum ExplanationCode {
    // Option notes
    DiscountAlreadyInQuote,
    DiscountNotImmediatelyUsable,
    DiscountUnverified,
    DiscountUnvaluable,
    DuplicateDiscountIgnored,
    DiscountCappedAtPrice,
    RewardUnverified,
    RewardCapApplied,
    RewardCapExhausted,
    PointValuationMissing,
    PointValuationUnstable,
    // Assumptions
    UsdRateUsed,
    PointValuationUsed,
    RewardBaseExcludesFees,
    AmountDueIsUpperBound,
    // Sensitivities
    QuoteExpiry,
    Margin,
    DependsOnEstimatedRewards,
    UnstablePointValuationExcluded,
    SavingsBelowThreshold,
    ExcludedOptionCouldChangeResult,
}
