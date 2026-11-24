//! Execution grant verification.
//!
//! The control plane issues an EdDSA (Ed25519) JWT after it has atomically
//! reserved budget. The gateway verifies the signature, the standard claims,
//! and then every bound field against the payment it is about to make. A
//! grant whose bindings differ from the local request context in any way is
//! refused; the gateway never "fixes up" a payload to match a grant.

use crate::amount::Amount;
use crate::x402::Quote;
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use jsonwebtoken::{decode, Algorithm, DecodingKey, Validation};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub const ISSUER: &str = "asa-control-plane";
pub const AUDIENCE: &str = "asa-gateway";

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum GrantError {
    #[error("grant signature or claims invalid: {0}")]
    Invalid(String),
    #[error("grant binding mismatch on {field}: grant={grant:?} request={request:?}")]
    Binding { field: &'static str, grant: String, request: String },
    #[error("grant {0} was already used")]
    Replayed(String),
    #[error("grant amount {0} is not a canonical amount")]
    Amount(String),
}

/// Claims as issued by the Go control plane (`internal/grant`).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Claims {
    pub iss: String,
    #[serde(default)]
    pub aud: Audience,
    pub sub: String,
    pub jti: String,
    pub iat: i64,
    #[serde(default)]
    pub nbf: Option<i64>,
    pub exp: i64,
    pub org: String,
    pub task: String,
    pub rsv: String,
    pub att: String,
    pub pol: String,
    pub method: String,
    pub url: String,
    pub pay_to: String,
    pub asset: String,
    pub network: String,
    pub scheme: String,
    pub amount_max: String,
    pub fee_max: String,
    pub req_hash: String,
}

/// `aud` may be a string or an array per RFC 7519.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(untagged)]
pub enum Audience {
    One(String),
    Many(Vec<String>),
    #[default]
    None,
}

impl Audience {
    fn contains(&self, a: &str) -> bool {
        match self {
            Audience::One(s) => s == a,
            Audience::Many(v) => v.iter().any(|s| s == a),
            Audience::None => false,
        }
    }
}

/// The local facts a grant must agree with.
#[derive(Debug, Clone)]
pub struct Binding<'a> {
    pub org_id: &'a str,
    pub agent_id: &'a str,
    pub task_id: &'a str,
    pub attempt_id: &'a str,
    pub method: &'a str,
    pub url: &'a str,
    pub quote: &'a Quote,
}

pub struct Verifier {
    key: DecodingKey,
    used: Mutex<HashMap<String, SystemTime>>,
}

impl Verifier {
    /// `public_key` is the raw 32-byte Ed25519 public key.
    pub fn new(public_key: &[u8]) -> Result<Self, GrantError> {
        if public_key.len() != 32 {
            return Err(GrantError::Invalid("public key must be 32 bytes".into()));
        }
        let key = DecodingKey::from_ed_components(&URL_SAFE_NO_PAD.encode(public_key))
            .map_err(|e| GrantError::Invalid(e.to_string()))?;
        Ok(Self { key, used: Mutex::new(HashMap::new()) })
    }

    /// Verify signature and standard claims only.
    pub fn decode(&self, token: &str) -> Result<Claims, GrantError> {
        let mut v = Validation::new(Algorithm::EdDSA);
        v.set_issuer(&[ISSUER]);
        v.set_audience(&[AUDIENCE]);
        v.set_required_spec_claims(&["exp", "iss", "aud", "sub", "jti"]);
        v.leeway = 5;
        v.validate_nbf = true;
        let data = decode::<Claims>(token, &self.key, &v).map_err(|e| GrantError::Invalid(e.to_string()))?;
        if !data.claims.aud.contains(AUDIENCE) {
            return Err(GrantError::Invalid("audience mismatch".into()));
        }
        Ok(data.claims)
    }

    /// Verify the token and all bindings, and record the jti as used.
    /// Returns the amount ceiling and fee allowance the grant authorizes.
    pub fn verify(&self, token: &str, b: &Binding<'_>) -> Result<(Claims, Amount, Amount), GrantError> {
        let c = self.decode(token)?;
        check("org", &c.org, b.org_id)?;
        check("sub", &c.sub, b.agent_id)?;
        check("task", &c.task, b.task_id)?;
        check("att", &c.att, b.attempt_id)?;
        check_ci("method", &c.method, b.method)?;
        check("url", &c.url, b.url)?;
        check_ci("pay_to", &c.pay_to, &b.quote.pay_to)?;
        check_ci("asset", &c.asset, &b.quote.asset)?;
        check_ci("network", &c.network, &b.quote.network)?;
        check_ci("scheme", &c.scheme, &b.quote.scheme)?;
        check_ci("req_hash", &c.req_hash, &b.quote.requirements_hash)?;
        let amount_max = Amount::parse(&c.amount_max).map_err(|_| GrantError::Amount(c.amount_max.clone()))?;
        let fee_max = Amount::parse(&c.fee_max).map_err(|_| GrantError::Amount(c.fee_max.clone()))?;
        if b.quote.max_amount_required > amount_max {
            return Err(GrantError::Binding {
                field: "amount_max",
                grant: c.amount_max.clone(),
                request: b.quote.max_amount_required.to_string(),
            });
        }
        self.mark_used(&c.jti, c.exp)?;
        Ok((c, amount_max, fee_max))
    }

    /// Local single-use enforcement (the control plane also enforces this
    /// when the gateway records the submission; both layers must agree).
    fn mark_used(&self, jti: &str, exp: i64) -> Result<(), GrantError> {
        let mut used = self.used.lock().expect("grant cache poisoned");
        let now = SystemTime::now();
        used.retain(|_, until| *until > now);
        if used.contains_key(jti) {
            return Err(GrantError::Replayed(jti.to_string()));
        }
        let until = UNIX_EPOCH + Duration::from_secs(exp.max(0) as u64) + Duration::from_secs(60);
        used.insert(jti.to_string(), until);
        Ok(())
    }
}

fn check(field: &'static str, grant: &str, request: &str) -> Result<(), GrantError> {
    if grant == request {
        Ok(())
    } else {
        Err(GrantError::Binding { field, grant: grant.to_string(), request: request.to_string() })
    }
}

fn check_ci(field: &'static str, grant: &str, request: &str) -> Result<(), GrantError> {
    if grant.eq_ignore_ascii_case(request) {
        Ok(())
    } else {
        Err(GrantError::Binding { field, grant: grant.to_string(), request: request.to_string() })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::x402::requirements_hash;
    use ed25519_dalek::SigningKey;
    use jsonwebtoken::{encode, EncodingKey, Header};
    use proptest::prelude::*;

    fn now() -> i64 {
        SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_secs() as i64
    }

    struct Fixture {
        verifier: Verifier,
        enc: EncodingKey,
        other: EncodingKey,
    }

    fn fixture() -> Fixture {
        let sk = SigningKey::generate(&mut rand_core::OsRng);
        let other = SigningKey::generate(&mut rand_core::OsRng);
        let pkcs8 = |sk: &SigningKey| {
            // PKCS#8 v1 wrapper for an Ed25519 seed.
            let mut der = vec![
                0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
            ];
            der.extend_from_slice(sk.as_bytes());
            EncodingKey::from_ed_der(&der)
        };
        Fixture {
            verifier: Verifier::new(sk.verifying_key().as_bytes()).unwrap(),
            enc: pkcs8(&sk),
            other: pkcs8(&other),
        }
    }

    fn quote() -> Quote {
        let mut q = Quote {
            scheme: "exact".into(),
            network: "base-sepolia".into(),
            asset: "0x036CbD53842c5426634e7929541eC2318f3dCF7e".into(),
            pay_to: "0x209693Bc6afc0C5328bA36FaF03C514EF312287C".into(),
            max_amount_required: Amount::parse("100000").unwrap(),
            resource: "https://api.example.test/v1/weather".into(),
            description: String::new(),
            mime_type: String::new(),
            max_timeout_seconds: 60,
            extra: None,
            requirements_hash: String::new(),
        };
        q.requirements_hash = requirements_hash(&q);
        q
    }

    fn claims(q: &Quote) -> Claims {
        Claims {
            iss: ISSUER.into(),
            aud: Audience::Many(vec![AUDIENCE.into()]),
            sub: "agt_1".into(),
            jti: format!("grt_{}", rand::random::<u64>()),
            iat: now(),
            nbf: Some(now() - 5),
            exp: now() + 60,
            org: "org_1".into(),
            task: "tsk_1".into(),
            rsv: "rsv_1".into(),
            att: "pay_1".into(),
            pol: "pol_1".into(),
            method: "GET".into(),
            url: "https://api.example.test/v1/weather".into(),
            pay_to: q.pay_to.clone(),
            asset: q.asset.clone(),
            network: q.network.clone(),
            scheme: q.scheme.clone(),
            amount_max: "100000".into(),
            fee_max: "0".into(),
            req_hash: q.requirements_hash.clone(),
        }
    }

    fn sign(key: &EncodingKey, c: &Claims) -> String {
        encode(&Header::new(Algorithm::EdDSA), c, key).unwrap()
    }

    fn binding<'a>(q: &'a Quote) -> Binding<'a> {
        Binding {
            org_id: "org_1",
            agent_id: "agt_1",
            task_id: "tsk_1",
            attempt_id: "pay_1",
            method: "GET",
            url: "https://api.example.test/v1/weather",
            quote: q,
        }
    }

    #[test]
    fn accepts_valid_grant_once() {
        let f = fixture();
        let q = quote();
        let tok = sign(&f.enc, &claims(&q));
        let (_, amount, fee) = f.verifier.verify(&tok, &binding(&q)).unwrap();
        assert_eq!(amount.to_string(), "100000");
        assert_eq!(fee.to_string(), "0");
        assert!(matches!(f.verifier.verify(&tok, &binding(&q)), Err(GrantError::Replayed(_))));
    }

    #[test]
    fn rejects_wrong_key_expired_and_wrong_audience() {
        let f = fixture();
        let q = quote();
        assert!(matches!(f.verifier.verify(&sign(&f.other, &claims(&q)), &binding(&q)), Err(GrantError::Invalid(_))));
        let mut c = claims(&q);
        c.exp = now() - 30;
        assert!(matches!(f.verifier.verify(&sign(&f.enc, &c), &binding(&q)), Err(GrantError::Invalid(_))));
        let mut c = claims(&q);
        c.aud = Audience::One("someone-else".into());
        assert!(matches!(f.verifier.verify(&sign(&f.enc, &c), &binding(&q)), Err(GrantError::Invalid(_))));
        let mut c = claims(&q);
        c.iss = "evil".into();
        assert!(matches!(f.verifier.verify(&sign(&f.enc, &c), &binding(&q)), Err(GrantError::Invalid(_))));
        // alg=none / HS256 tokens are rejected by the EdDSA-only validation.
        let hs = encode(&Header::new(Algorithm::HS256), &claims(&q), &EncodingKey::from_secret(b"x")).unwrap();
        assert!(matches!(f.verifier.verify(&hs, &binding(&q)), Err(GrantError::Invalid(_))));
    }

    #[test]
    fn rejects_every_binding_mutation() {
        let f = fixture();
        let q = quote();
        type Mutation = Box<dyn Fn(&mut Claims)>;
        let mutations: Vec<(&str, Mutation)> = vec![
            ("org", Box::new(|c| c.org = "org_2".into())),
            ("sub", Box::new(|c| c.sub = "agt_2".into())),
            ("task", Box::new(|c| c.task = "tsk_2".into())),
            ("att", Box::new(|c| c.att = "pay_2".into())),
            ("method", Box::new(|c| c.method = "POST".into())),
            ("url", Box::new(|c| c.url = "https://api.example.test/v1/other".into())),
            ("pay_to", Box::new(|c| c.pay_to = "0x000000000000000000000000000000000000dEaD".into())),
            ("asset", Box::new(|c| c.asset = "0x1111111111111111111111111111111111111111".into())),
            ("network", Box::new(|c| c.network = "base".into())),
            ("scheme", Box::new(|c| c.scheme = "upto".into())),
            ("req_hash", Box::new(|c| c.req_hash = "00".repeat(32))),
            ("amount_max", Box::new(|c| c.amount_max = "99999".into())),
        ];
        for (name, m) in mutations {
            let mut c = claims(&q);
            m(&mut c);
            let err = f.verifier.verify(&sign(&f.enc, &c), &binding(&q)).unwrap_err();
            assert!(matches!(err, GrantError::Binding { field, .. } if field == name), "{name}: {err:?}");
        }
    }

    #[test]
    fn payload_mutation_after_grant_is_detected() {
        let f = fixture();
        let q = quote();
        let tok = sign(&f.enc, &claims(&q));
        // The server (or a bug) swaps the beneficiary after authorization.
        let mut mutated = q.clone();
        mutated.pay_to = "0x000000000000000000000000000000000000dEaD".into();
        assert!(matches!(f.verifier.verify(&tok, &binding(&mutated)), Err(GrantError::Binding { field: "pay_to", .. })));
        // Or the amount grows.
        let mut mutated = q.clone();
        mutated.max_amount_required = Amount::parse("100001").unwrap();
        assert!(matches!(f.verifier.verify(&tok, &binding(&mutated)), Err(GrantError::Binding { field: "amount_max", .. })));
    }

    proptest! {
        #[test]
        fn garbage_tokens_are_rejected_without_panicking(s in "\\PC{0,300}") {
            let f = fixture();
            let q = quote();
            prop_assert!(f.verifier.verify(&s, &binding(&q)).is_err());
        }

        #[test]
        fn quote_amount_above_grant_is_rejected(req in any::<u128>(), granted in any::<u128>()) {
            let f = fixture();
            let mut q = quote();
            q.max_amount_required = Amount::from_u128(req);
            q.requirements_hash = requirements_hash(&q);
            let mut c = claims(&q);
            c.amount_max = granted.to_string();
            let res = f.verifier.verify(&sign(&f.enc, &c), &binding(&q));
            let is_amount_binding = matches!(res, Err(GrantError::Binding { field: "amount_max", .. }));
            if req > granted {
                prop_assert!(is_amount_binding, "expected amount_max binding error, got {:?}", res.err());
            } else {
                prop_assert!(res.is_ok(), "expected ok, got {:?}", res.err());
            }
        }
    }
}
