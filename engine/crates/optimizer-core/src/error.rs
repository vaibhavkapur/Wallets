//! Request-level errors. These reject the whole evaluation; problems with a
//! single option are reported as exclusions instead.

use std::fmt;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum RequestErrorCode {
    UnsupportedContractVersion,
    UnsupportedComparisonCurrency,
    InvalidOptions,
    DuplicateOptionId,
    UnknownDefaultOption,
    InvalidThreshold,
    InvalidAmount,
    InvalidAsset,
    InvalidRate,
    InvalidQuote,
    InvalidFee,
    ConflictingDuplicateDiscount,
    InvalidReward,
    InvalidFunds,
    InvalidFundingStep,
    InvalidPointValuation,
    ArithmeticOverflow,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
pub struct RequestError {
    pub code: RequestErrorCode,
    /// JSON-pointer-like path to the offending field.
    pub field: String,
    pub message: String,
}

impl RequestError {
    pub fn new(
        code: RequestErrorCode,
        field: impl Into<String>,
        message: impl Into<String>,
    ) -> Self {
        RequestError {
            code,
            field: field.into(),
            message: message.into(),
        }
    }
}

impl fmt::Display for RequestError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.field, self.message)
    }
}

impl std::error::Error for RequestError {}
