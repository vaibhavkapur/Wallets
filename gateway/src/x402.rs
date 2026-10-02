//! x402 (v1) payment-required challenge parsing, requirement selection, the
//! canonical requirements hash shared with the control plane, and the
//! `X-PAYMENT` / `X-PAYMENT-RESPONSE` header encodings for the `exact` scheme.
//!
//! Resource servers are untrusted. Everything here is bounded and strict:
//! unknown fields are tolerated (the protocol allows `extra`), but amounts,
//! addresses, networks and schemes must match the owner's policy exactly.

use crate::amount::Amount;
use base64::{engine::general_purpose::STANDARD as B64, Engine};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

pub const X402_VERSION: u32 = 1;
pub const SCHEME_EXACT: &str = "exact";
pub const MAX_CHALLENGE_BYTES: usize = 64 * 1024;
pub const MAX_ACCEPTS: usize = 32;
pub const MAX_TIMEOUT_SECONDS: u64 = 3600;

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum ChallengeError {
    #[error("challenge body is not valid x402 JSON: {0}")]
    Malformed(String),
    #[error("unsupported x402Version {0}")]
    Version(u32),
    #[error("challenge lists too many payment options")]
    TooManyAccepts,
    #[error("no payment option matches policy (scheme={scheme}, network={network}, asset={asset})")]
    NoMatch { scheme: String, network: String, asset: String },
    #[error("payment option rejected: {0}")]
    Rejected(String),
}

/// One entry of the `accepts` array.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Requirement {
    pub scheme: String,
    pub network: String,
    pub max_amount_required: String,
    #[serde(default)]
    pub resource: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub mime_type: String,
    pub pay_to: String,
    #[serde(default)]
    pub max_timeout_seconds: u64,
    pub asset: String,
    #[serde(default)]
    pub extra: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PaymentRequired {
    pub x402_version: u32,
    #[serde(default)]
    pub accepts: Vec<Requirement>,
    #[serde(default)]
    pub error: Option<String>,
}

/// The selected, validated requirement in control-plane shape.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Quote {
    pub scheme: String,
    pub network: String,
    pub asset: String,
    pub pay_to: String,
    pub max_amount_required: Amount,
    pub resource: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mime_type: String,
    pub max_timeout_seconds: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub extra: Option<serde_json::Value>,
    pub requirements_hash: String,
}

/// Parse a 402 body.
pub fn parse_challenge(body: &[u8]) -> Result<PaymentRequired, ChallengeError> {
    if body.len() > MAX_CHALLENGE_BYTES {
        return Err(ChallengeError::Malformed("challenge body too large".into()));
    }
    let pr: PaymentRequired =
        serde_json::from_slice(body).map_err(|e| ChallengeError::Malformed(e.to_string()))?;
    if pr.x402_version != X402_VERSION {
        return Err(ChallengeError::Version(pr.x402_version));
    }
    if pr.accepts.len() > MAX_ACCEPTS {
        return Err(ChallengeError::TooManyAccepts);
    }
    Ok(pr)
}

/// What the owner's policy pins for this request (from the control plane).
#[derive(Debug, Clone)]
pub struct Constraints<'a> {
    pub scheme: &'a str,
    pub network: &'a str,
    pub asset: &'a str,
    pub recipients: &'a [String],
    pub per_request_max: Amount,
    /// Optional tighter ceiling supplied by the agent for this call.
    pub client_max: Option<Amount>,
}

/// Choose the first `accepts` entry compatible with policy and validate it.
/// Entries that merely do not match (different network, scheme) are skipped;
/// an entry that matches on scheme/network/asset but violates a constraint is
/// a hard rejection so that a server cannot trick the gateway into a
/// different beneficiary by listing it first.
pub fn select_requirement(pr: &PaymentRequired, c: &Constraints<'_>) -> Result<Quote, ChallengeError> {
    for r in &pr.accepts {
        let matches = r.scheme.eq_ignore_ascii_case(c.scheme)
            && r.network.eq_ignore_ascii_case(c.network)
            && r.asset.eq_ignore_ascii_case(c.asset);
        if !matches {
            continue;
        }
        if r.scheme.to_ascii_lowercase() != SCHEME_EXACT {
            return Err(ChallengeError::Rejected(format!("scheme {:?} is not implemented", r.scheme)));
        }
        let amount = Amount::parse(&r.max_amount_required)
            .map_err(|e| ChallengeError::Rejected(format!("maxAmountRequired: {e}")))?;
        if amount > c.per_request_max {
            return Err(ChallengeError::Rejected(format!(
                "amount {amount} exceeds per-request max {}",
                c.per_request_max
            )));
        }
        if let Some(m) = c.client_max {
            if amount > m {
                return Err(ChallengeError::Rejected(format!("amount {amount} exceeds caller max {m}")));
            }
        }
        if !c.recipients.iter().any(|a| a.trim().eq_ignore_ascii_case(r.pay_to.trim())) {
            return Err(ChallengeError::Rejected(format!("beneficiary {:?} is not approved", r.pay_to)));
        }
        if r.max_timeout_seconds > MAX_TIMEOUT_SECONDS {
            return Err(ChallengeError::Rejected("maxTimeoutSeconds out of range".into()));
        }
        if r.pay_to.len() > 128 || r.asset.len() > 128 || r.network.len() > 64 || r.resource.len() > 2048 {
            return Err(ChallengeError::Rejected("field too long".into()));
        }
        let mut q = Quote {
            scheme: r.scheme.to_ascii_lowercase(),
            network: r.network.to_ascii_lowercase(),
            asset: r.asset.clone(),
            pay_to: r.pay_to.clone(),
            max_amount_required: amount,
            resource: r.resource.clone(),
            description: truncate(&r.description, 512),
            mime_type: truncate(&r.mime_type, 128),
            max_timeout_seconds: r.max_timeout_seconds,
            extra: r.extra.clone(),
            requirements_hash: String::new(),
        };
        q.requirements_hash = requirements_hash(&q);
        return Ok(q);
    }
    Err(ChallengeError::NoMatch {
        scheme: c.scheme.to_string(),
        network: c.network.to_string(),
        asset: c.asset.to_string(),
    })
}

fn truncate(s: &str, n: usize) -> String {
    s.chars().take(n).collect()
}

/// Canonical SHA-256 of the requirement fields. Must match the Go
/// implementation byte-for-byte: lowercase scheme, network, asset and pay_to;
/// canonical decimal amount; resource verbatim; timeout as decimal; joined by "\n".
pub fn requirements_hash(q: &Quote) -> String {
    let canon = [
        q.scheme.to_lowercase(),
        q.network.to_lowercase(),
        q.asset.to_lowercase(),
        q.pay_to.to_lowercase(),
        q.max_amount_required.to_string(),
        q.resource.clone(),
        q.max_timeout_seconds.to_string(),
    ]
    .join("\n");
    hex::encode(Sha256::digest(canon.as_bytes()))
}

// --- X-PAYMENT (exact scheme, EIP-3009 style authorization) ------------------

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Authorization {
    pub from: String,
    pub to: String,
    pub value: String,
    pub valid_after: String,
    pub valid_before: String,
    pub nonce: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExactPayload {
    pub signature: String,
    pub authorization: Authorization,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PaymentHeader {
    pub x402_version: u32,
    pub scheme: String,
    pub network: String,
    pub payload: ExactPayload,
}

impl PaymentHeader {
    /// Base64 (standard alphabet) of the JSON document, as x402 clients send.
    pub fn encode(&self) -> String {
        B64.encode(serde_json::to_vec(self).expect("serializable"))
    }
}

/// SHA-256 hex of the encoded header; this is the `payment_hash` the control
/// plane stores and the facilitator lookup is keyed by.
pub fn payment_hash(encoded_header: &str) -> String {
    hex::encode(Sha256::digest(encoded_header.as_bytes()))
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct SettlementResponse {
    #[serde(default)]
    pub success: bool,
    #[serde(default)]
    pub transaction: String,
    #[serde(default)]
    pub network: String,
    #[serde(default)]
    pub payer: String,
    #[serde(default, rename = "errorReason")]
    pub error_reason: Option<String>,
}

/// Decode an `X-PAYMENT-RESPONSE` header (base64 JSON).
pub fn parse_settlement_header(v: &str) -> Option<SettlementResponse> {
    let raw = B64.decode(v.trim()).ok()?;
    if raw.len() > 16 * 1024 {
        return None;
    }
    serde_json::from_slice(&raw).ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use proptest::prelude::*;

    const ASSET: &str = "0x036CbD53842c5426634e7929541eC2318f3dCF7e";
    const PAY_TO: &str = "0x209693Bc6afc0C5328bA36FaF03C514EF312287C";

    fn challenge(json: serde_json::Value) -> PaymentRequired {
        parse_challenge(&serde_json::to_vec(&json).unwrap()).unwrap()
    }

    fn req(overrides: serde_json::Value) -> serde_json::Value {
        let mut base = serde_json::json!({
            "scheme": "exact", "network": "base-sepolia", "maxAmountRequired": "100000",
            "resource": "https://api.example.test/v1/weather", "description": "w", "mimeType": "application/json",
            "payTo": PAY_TO, "maxTimeoutSeconds": 60, "asset": ASSET, "extra": {"name": "USDC", "version": "2"}
        });
        for (k, v) in overrides.as_object().unwrap() {
            base[k] = v.clone();
        }
        base
    }

    fn constraints<'a>(recipients: &'a [String]) -> Constraints<'a> {
        Constraints {
            scheme: "exact",
            network: "base-sepolia",
            asset: ASSET,
            recipients,
            per_request_max: Amount::parse("1000000").unwrap(),
            client_max: None,
        }
    }

    #[test]
    fn selects_matching_requirement_and_hashes_canonically() {
        let recips = vec![PAY_TO.to_string()];
        let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [req(serde_json::json!({}))]}));
        let q = select_requirement(&pr, &constraints(&recips)).unwrap();
        assert_eq!(q.max_amount_required.to_string(), "100000");
        // Known-answer test shared with the Go side (see grant.RequirementsHash).
        let expected = hex::encode(Sha256::digest(
            format!(
                "exact\nbase-sepolia\n{}\n{}\n100000\nhttps://api.example.test/v1/weather\n60",
                ASSET.to_lowercase(),
                PAY_TO.to_lowercase()
            )
            .as_bytes(),
        ));
        assert_eq!(q.requirements_hash, expected);
    }

    #[test]
    fn rejects_changed_beneficiary_even_if_listed_first() {
        let recips = vec![PAY_TO.to_string()];
        let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [
            req(serde_json::json!({"payTo": "0x000000000000000000000000000000000000dEaD"})),
            req(serde_json::json!({}))
        ]}));
        assert!(matches!(select_requirement(&pr, &constraints(&recips)), Err(ChallengeError::Rejected(_))));
    }

    #[test]
    fn skips_wrong_network_and_asset_then_fails_closed() {
        let recips = vec![PAY_TO.to_string()];
        let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [
            req(serde_json::json!({"network": "base"})),
            req(serde_json::json!({"asset": "0x1111111111111111111111111111111111111111"})),
            req(serde_json::json!({"scheme": "upto"}))
        ]}));
        assert!(matches!(select_requirement(&pr, &constraints(&recips)), Err(ChallengeError::NoMatch { .. })));
    }

    #[test]
    fn rejects_overpriced_malformed_amounts_and_bad_versions() {
        let recips = vec![PAY_TO.to_string()];
        for bad in ["1000001", "1e5", "-1", "0x10", "100000.0", " 1", "01"] {
            let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [req(serde_json::json!({"maxAmountRequired": bad}))]}));
            assert!(matches!(select_requirement(&pr, &constraints(&recips)), Err(ChallengeError::Rejected(_))), "{bad}");
        }
        assert!(matches!(parse_challenge(br#"{"x402Version":2,"accepts":[]}"#), Err(ChallengeError::Version(2))));
        assert!(matches!(parse_challenge(b"{not json"), Err(ChallengeError::Malformed(_))));
        let mut c = constraints(&recips);
        c.client_max = Some(Amount::parse("99999").unwrap());
        let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [req(serde_json::json!({}))]}));
        assert!(matches!(select_requirement(&pr, &c), Err(ChallengeError::Rejected(_))));
    }

    #[test]
    fn payment_header_round_trip() {
        let h = PaymentHeader {
            x402_version: 1,
            scheme: "exact".into(),
            network: "base-sepolia".into(),
            payload: ExactPayload {
                signature: "0xsig".into(),
                authorization: Authorization {
                    from: "0xfrom".into(),
                    to: PAY_TO.into(),
                    value: "100000".into(),
                    valid_after: "0".into(),
                    valid_before: "1".into(),
                    nonce: "0x00".into(),
                },
            },
        };
        let enc = h.encode();
        let dec: PaymentHeader = serde_json::from_slice(&B64.decode(&enc).unwrap()).unwrap();
        assert_eq!(dec.payload.authorization, h.payload.authorization);
        assert_eq!(payment_hash(&enc).len(), 64);
        let s = parse_settlement_header(&B64.encode(br#"{"success":true,"transaction":"0xabc","network":"base-sepolia","payer":"0xp"}"#)).unwrap();
        assert!(s.success && s.transaction == "0xabc");
        assert!(parse_settlement_header("!!!").is_none());
    }

    proptest! {
        #[test]
        fn parse_challenge_never_panics(body in proptest::collection::vec(any::<u8>(), 0..512)) {
            let _ = parse_challenge(&body);
        }

        #[test]
        fn arbitrary_requirement_fields_never_select_unapproved_recipient(
            pay_to in "\\PC{0,48}", amount in "[0-9]{0,40}", network in "[a-z-]{0,16}"
        ) {
            let recips = vec![PAY_TO.to_string()];
            let pr = challenge(serde_json::json!({"x402Version": 1, "accepts": [
                req(serde_json::json!({"payTo": pay_to, "maxAmountRequired": amount, "network": network}))
            ]}));
            if let Ok(q) = select_requirement(&pr, &constraints(&recips)) {
                prop_assert!(q.pay_to.trim().eq_ignore_ascii_case(PAY_TO));
                prop_assert!(q.max_amount_required <= Amount::parse("1000000").unwrap());
                prop_assert_eq!(q.network.as_str(), "base-sepolia");
            }
        }
    }
}
