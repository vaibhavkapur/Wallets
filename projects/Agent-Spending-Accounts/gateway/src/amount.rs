//! Exact base-unit amounts with checked `u128` arithmetic.
//!
//! The canonical string form is identical to the Go control plane's
//! `money.Amount`: ASCII digits only, no sign, no leading zeros, at most 39
//! digits, not above `u128::MAX`. No floating point anywhere.

use serde::{de, Deserialize, Deserializer, Serialize, Serializer};
use std::fmt;

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum AmountError {
    #[error("invalid amount format: {0:?}")]
    InvalidFormat(String),
    #[error("amount exceeds maximum representable value")]
    Overflow,
    #[error("amount would be negative")]
    Negative,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Default)]
pub struct Amount(u128);

impl Amount {
    pub const ZERO: Amount = Amount(0);
    pub const MAX_DIGITS: usize = 39;

    pub fn parse(s: &str) -> Result<Amount, AmountError> {
        if s.is_empty() || s.len() > Self::MAX_DIGITS {
            return Err(AmountError::InvalidFormat(s.to_string()));
        }
        if !s.bytes().all(|b| b.is_ascii_digit()) {
            return Err(AmountError::InvalidFormat(s.to_string()));
        }
        if s.len() > 1 && s.starts_with('0') {
            return Err(AmountError::InvalidFormat(s.to_string()));
        }
        s.parse::<u128>()
            .map(Amount)
            .map_err(|_| AmountError::Overflow)
    }

    pub const fn from_u128(v: u128) -> Amount {
        Amount(v)
    }

    pub fn raw(self) -> u128 {
        self.0
    }

    pub fn is_zero(self) -> bool {
        self.0 == 0
    }

    pub fn checked_add(self, other: Amount) -> Result<Amount, AmountError> {
        self.0
            .checked_add(other.0)
            .map(Amount)
            .ok_or(AmountError::Overflow)
    }

    pub fn checked_sub(self, other: Amount) -> Result<Amount, AmountError> {
        self.0
            .checked_sub(other.0)
            .map(Amount)
            .ok_or(AmountError::Negative)
    }
}

impl fmt::Display for Amount {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl Serialize for Amount {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&self.0.to_string())
    }
}

impl<'de> Deserialize<'de> for Amount {
    /// Accepts only JSON strings; numbers are rejected to avoid precision loss
    /// in whichever client produced them.
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        let s = String::deserialize(d)?;
        Amount::parse(&s).map_err(de::Error::custom)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use proptest::prelude::*;

    #[test]
    fn canonical_parsing() {
        assert_eq!(Amount::parse("0").unwrap().raw(), 0);
        assert_eq!(Amount::parse("1000000").unwrap().raw(), 1_000_000);
        assert_eq!(
            Amount::parse("340282366920938463463374607431768211455").unwrap(),
            Amount(u128::MAX)
        );
        for bad in ["", "-1", "+1", "01", "1.5", "1e6", " 1", "1 ", "١", "0x10"] {
            assert!(Amount::parse(bad).is_err(), "{bad:?} should be rejected");
        }
        assert_eq!(
            Amount::parse("340282366920938463463374607431768211456"),
            Err(AmountError::Overflow)
        );
        assert!(Amount::parse("1000000000000000000000000000000000000000").is_err());
    }

    #[test]
    fn checked_arithmetic() {
        assert_eq!(Amount(u128::MAX).checked_add(Amount(1)), Err(AmountError::Overflow));
        assert_eq!(Amount(1).checked_sub(Amount(2)), Err(AmountError::Negative));
        assert_eq!(Amount(5).checked_add(Amount(6)).unwrap(), Amount(11));
    }

    #[test]
    fn json_strings_only() {
        let a: Amount = serde_json::from_str("\"42\"").unwrap();
        assert_eq!(a, Amount(42));
        assert!(serde_json::from_str::<Amount>("42").is_err());
        assert!(serde_json::from_str::<Amount>("\"4.2\"").is_err());
        assert_eq!(serde_json::to_string(&Amount(7)).unwrap(), "\"7\"");
    }

    proptest! {
        #[test]
        fn round_trips_every_u128(v in any::<u128>()) {
            let s = v.to_string();
            let a = Amount::parse(&s).unwrap();
            prop_assert_eq!(a.raw(), v);
            prop_assert_eq!(a.to_string(), s);
        }

        #[test]
        fn never_panics_on_arbitrary_input(s in "\\PC{0,64}") {
            let _ = Amount::parse(&s);
        }

        #[test]
        fn add_then_sub_is_identity(a in any::<u128>(), b in any::<u128>()) {
            if let Ok(sum) = Amount(a).checked_add(Amount(b)) {
                prop_assert_eq!(sum.checked_sub(Amount(b)).unwrap(), Amount(a));
            }
        }
    }
}
