//! Exact integer quantities and the only rounding primitives the engine uses.
//!
//! Every quantity is an `i128` handled with checked arithmetic. Inputs are
//! non-negative canonical decimal strings; JSON numbers are rejected so that no
//! client can pass a value through a floating-point representation.

use std::borrow::Cow;
use std::fmt;

use schemars::{json_schema, JsonSchema, Schema, SchemaGenerator};
use serde::de::{self, Visitor};
use serde::{Deserialize, Deserializer, Serialize, Serializer};

/// Longest accepted decimal string. `i128::MAX` has 39 digits.
pub const MAX_DIGITS: usize = 39;

/// Micro-USD per USD minor unit (cent).
pub const MICRO_PER_USD_MINOR: i128 = 10_000;
/// Micro-USD per whole USD.
pub const MICRO_PER_USD: i128 = 1_000_000;

/// Non-negative integer quantity (base units, points, basis points).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Units(i128);

impl Units {
    pub const ZERO: Units = Units(0);

    pub fn new(value: i128) -> Option<Units> {
        (value >= 0).then_some(Units(value))
    }

    pub fn get(self) -> i128 {
        self.0
    }

    /// Parses a canonical non-negative decimal string: ASCII digits only, no
    /// sign, no leading zeros, at most [`MAX_DIGITS`] digits, within `i128`.
    pub fn parse(s: &str) -> Result<Units, &'static str> {
        if s.is_empty() {
            return Err("amount is empty");
        }
        if !s.bytes().all(|b| b.is_ascii_digit()) {
            return Err("amount must contain only decimal digits");
        }
        if s.len() > 1 && s.starts_with('0') {
            return Err("amount must not have leading zeros");
        }
        if s.len() > MAX_DIGITS {
            return Err("amount exceeds the representable range");
        }
        s.parse::<i128>()
            .map(Units)
            .map_err(|_| "amount exceeds the representable range")
    }
}

impl fmt::Display for Units {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl Serialize for Units {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for Units {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl Visitor<'_> for V {
            type Value = Units;
            fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str("a non-negative integer encoded as a decimal string")
            }
            fn visit_str<E: de::Error>(self, v: &str) -> Result<Units, E> {
                Units::parse(v).map_err(E::custom)
            }
        }
        d.deserialize_str(V)
    }
}

impl JsonSchema for Units {
    fn schema_name() -> Cow<'static, str> {
        "Units".into()
    }
    fn json_schema(_: &mut SchemaGenerator) -> Schema {
        json_schema!({
            "type": "string",
            "pattern": "^(0|[1-9][0-9]{0,38})$",
            "description": "Non-negative integer as a decimal string. Never a JSON number."
        })
    }
}

/// Signed micro-USD value (1 USD = 1,000,000) used for all comparable results.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct MicroUsd(pub i128);

impl fmt::Display for MicroUsd {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl Serialize for MicroUsd {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for MicroUsd {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl Visitor<'_> for V {
            type Value = MicroUsd;
            fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str("a signed integer encoded as a decimal string")
            }
            fn visit_str<E: de::Error>(self, v: &str) -> Result<MicroUsd, E> {
                let digits = v.strip_prefix('-').unwrap_or(v);
                let magnitude = Units::parse(digits).map_err(E::custom)?;
                if v.starts_with('-') {
                    if magnitude.get() == 0 {
                        return Err(E::custom("negative zero is not canonical"));
                    }
                    Ok(MicroUsd(-magnitude.get()))
                } else {
                    Ok(MicroUsd(magnitude.get()))
                }
            }
        }
        d.deserialize_str(V)
    }
}

impl JsonSchema for MicroUsd {
    fn schema_name() -> Cow<'static, str> {
        "MicroUsd".into()
    }
    fn json_schema(_: &mut SchemaGenerator) -> Schema {
        json_schema!({
            "type": "string",
            "pattern": "^(0|-?[1-9][0-9]{0,38})$",
            "description": "Signed micro-USD (1 USD = 1000000) as a decimal string. Never a JSON number."
        })
    }
}

/// Direction of rounding. Costs round up and benefits round down, so that an
/// estimate never understates what the customer pays.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Round {
    Up,
    Down,
}

/// Arithmetic left the representable range.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Overflow;

/// `10^exp`, or `Overflow`.
pub fn pow10(exp: u8) -> Result<i128, Overflow> {
    10i128.checked_pow(u32::from(exp)).ok_or(Overflow)
}

/// `a / d` rounded in the given direction, for `a >= 0` and `d > 0`.
pub fn div_round(a: i128, d: i128, round: Round) -> Result<i128, Overflow> {
    if a < 0 || d <= 0 {
        return Err(Overflow);
    }
    let q = a / d;
    match round {
        Round::Down => Ok(q),
        Round::Up if a % d == 0 => Ok(q),
        Round::Up => q.checked_add(1).ok_or(Overflow),
    }
}

pub fn add(a: i128, b: i128) -> Result<i128, Overflow> {
    a.checked_add(b).ok_or(Overflow)
}

pub fn sub(a: i128, b: i128) -> Result<i128, Overflow> {
    a.checked_sub(b).ok_or(Overflow)
}

pub fn mul(a: i128, b: i128) -> Result<i128, Overflow> {
    a.checked_mul(b).ok_or(Overflow)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_canonical_strings_only() {
        assert_eq!(Units::parse("0").unwrap().get(), 0);
        assert_eq!(Units::parse("10000").unwrap().get(), 10_000);
        for bad in ["", "-1", "+1", "01", "1.0", "1e3", " 1", "1 ", "٣"] {
            assert!(Units::parse(bad).is_err(), "{bad:?} must be rejected");
        }
    }

    #[test]
    fn range_boundaries() {
        let max = i128::MAX.to_string();
        assert_eq!(Units::parse(&max).unwrap().get(), i128::MAX);
        // i128::MAX + 1
        assert!(Units::parse("170141183460469231731687303715884105728").is_err());
        assert!(Units::parse(&"9".repeat(40)).is_err());
    }

    #[test]
    fn json_numbers_are_rejected() {
        assert!(serde_json::from_str::<Units>("100").is_err());
        assert!(serde_json::from_str::<Units>("100.5").is_err());
        assert_eq!(serde_json::from_str::<Units>("\"100\"").unwrap().get(), 100);
        assert_eq!(
            serde_json::to_string(&Units::new(7).unwrap()).unwrap(),
            "\"7\""
        );
    }

    #[test]
    fn micro_usd_round_trips_signed_values() {
        for v in [0i128, 1, -1, 97_500_000, -500_000, i128::MAX, -i128::MAX] {
            let json = serde_json::to_string(&MicroUsd(v)).unwrap();
            assert_eq!(
                serde_json::from_str::<MicroUsd>(&json).unwrap(),
                MicroUsd(v)
            );
        }
        assert!(serde_json::from_str::<MicroUsd>("\"-0\"").is_err());
    }

    #[test]
    fn directed_division() {
        assert_eq!(div_round(10, 4, Round::Down), Ok(2));
        assert_eq!(div_round(10, 4, Round::Up), Ok(3));
        assert_eq!(div_round(8, 4, Round::Up), Ok(2));
        assert_eq!(div_round(0, 4, Round::Up), Ok(0));
        assert_eq!(div_round(1, 0, Round::Up), Err(Overflow));
        assert_eq!(div_round(-1, 2, Round::Up), Err(Overflow));
    }

    #[test]
    fn checked_helpers_report_overflow() {
        assert_eq!(mul(i128::MAX, 2), Err(Overflow));
        assert_eq!(add(i128::MAX, 1), Err(Overflow));
        assert_eq!(pow10(38), Ok(10i128.pow(38)));
        assert_eq!(pow10(39), Err(Overflow));
    }
}
