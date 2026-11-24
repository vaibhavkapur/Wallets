//! The isolated signing boundary.
//!
//! Wallet keys never live in this process. The gateway sends a fully
//! constrained authorization (recipient, exact value, validity window, nonce)
//! to the signing provider and receives a signature for that authorization
//! only. There is no "sign arbitrary bytes" capability anywhere in the gateway.

use crate::x402::Authorization;
use serde::{Deserialize, Serialize};
use std::time::Duration;

#[derive(Debug, thiserror::Error)]
pub enum SignError {
    #[error("signing provider unreachable: {0}")]
    Transport(String),
    #[error("signing provider refused: {status} {body}")]
    Refused { status: u16, body: String },
}

#[derive(Debug, Clone)]
pub struct Signed {
    pub from: String,
    pub signature: String,
}

/// A constrained authorization request to the provider.
#[derive(Debug, Clone, Serialize)]
pub struct SignRequest<'a> {
    pub network: &'a str,
    pub asset: &'a str,
    pub to: &'a str,
    pub value: &'a str,
    pub valid_after: &'a str,
    pub valid_before: &'a str,
    pub nonce: &'a str,
    pub grant_id: &'a str,
}

#[derive(Debug, Deserialize)]
struct SignResponse {
    from: String,
    signature: String,
}

#[derive(Clone)]
pub struct HttpSigner {
    url: String,
    token: String,
    http: reqwest::Client,
}

impl HttpSigner {
    pub fn new(base: &str, token: &str) -> anyhow::Result<Self> {
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(15))
            .connect_timeout(Duration::from_secs(5))
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        Ok(Self { url: format!("{}/v1/sign", base.trim_end_matches('/')), token: token.to_string(), http })
    }

    pub async fn sign(&self, network: &str, asset: &str, auth: &Authorization, grant_id: &str) -> Result<Signed, SignError> {
        let req = SignRequest {
            network,
            asset,
            to: &auth.to,
            value: &auth.value,
            valid_after: &auth.valid_after,
            valid_before: &auth.valid_before,
            nonce: &auth.nonce,
            grant_id,
        };
        let resp = self
            .http
            .post(&self.url)
            .bearer_auth(&self.token)
            .json(&req)
            .send()
            .await
            .map_err(|e| SignError::Transport(e.to_string()))?;
        let status = resp.status().as_u16();
        let bytes = resp.bytes().await.map_err(|e| SignError::Transport(e.to_string()))?;
        if status != 200 {
            return Err(SignError::Refused { status, body: String::from_utf8_lossy(&bytes).chars().take(300).collect() });
        }
        let r: SignResponse = serde_json::from_slice(&bytes).map_err(|e| SignError::Transport(format!("decode: {e}")))?;
        if r.signature.is_empty() || r.from.is_empty() {
            return Err(SignError::Transport("empty signature".into()));
        }
        Ok(Signed { from: r.from, signature: r.signature })
    }
}
