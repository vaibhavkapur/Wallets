//! Client for the Go control plane's internal API. The gateway authenticates
//! with a shared internal token; it never holds owner credentials.

use crate::amount::Amount;
use crate::x402::Quote;
use serde::{Deserialize, Serialize};
use std::time::Duration;

#[derive(Debug, thiserror::Error)]
pub enum CpError {
    #[error("control plane unreachable: {0}")]
    Transport(String),
    /// A structured policy / state response (4xx) from the control plane.
    #[error("{code}: {message}")]
    Denied { status: u16, code: String, message: String, attempt_id: Option<String> },
    #[error("control plane error {status}: {body}")]
    Unexpected { status: u16, body: String },
}

#[derive(Debug, Deserialize)]
struct ErrorBody {
    error: ErrorInner,
}

#[derive(Debug, Deserialize)]
struct ErrorInner {
    code: String,
    message: String,
    #[serde(default)]
    details: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct AssetSpec {
    pub asset: String,
    pub network: String,
    pub decimals: i64,
    pub scheme: String,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct EndpointRule {
    pub url_prefix: String,
    #[serde(default)]
    pub methods: Vec<String>,
    pub recipients: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct BeginContext {
    pub organization_id: String,
    pub agent_id: String,
    pub task_id: String,
    pub policy_version_id: String,
    pub asset: AssetSpec,
    pub per_request_max: Amount,
    pub fee_allowance: Amount,
    pub endpoint_rule: EndpointRule,
    pub grant_ttl_seconds: i64,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Attempt {
    pub id: String,
    pub organization_id: String,
    pub agent_id: String,
    pub task_id: String,
    pub status: String,
    #[serde(default)]
    pub reject_reason: String,
    #[serde(default)]
    pub submission_ref: String,
    #[serde(default)]
    pub settled_amount: Option<Amount>,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub quote: Option<Quote>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct BeginResponse {
    #[serde(default)]
    pub replay: bool,
    pub attempt: Attempt,
    #[serde(default)]
    pub context: Option<BeginContext>,
    #[serde(default)]
    pub receipt: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AuthorizeResponse {
    pub grant_token: String,
    pub grant_id: String,
    pub reservation_id: String,
    pub attempt_id: String,
    pub expires_at: String,
    pub amount_max: Amount,
    pub fee_max: Amount,
}

#[derive(Debug, Clone, Serialize, Default)]
pub struct ServiceOutcome {
    pub http_status: u16,
    pub delivered: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub content_type: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub body_sha256: String,
    pub body_bytes: u64,
    pub truncated: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct PaymentOutcome {
    pub status: &'static str, // settled | failed | unknown
    #[serde(skip_serializing_if = "String::is_empty")]
    pub submission_ref: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub payer_ref: String,
    pub settled_amount: Amount,
    pub fee_actual: Amount,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct OutcomeResponse {
    pub attempt: Attempt,
    #[serde(default)]
    pub receipt: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct GrantKey {
    pub kid: String,
    pub public_key_base64: String,
}

#[derive(Clone)]
pub struct ControlPlane {
    base: String,
    token: String,
    http: reqwest::Client,
}

impl ControlPlane {
    pub fn new(base: &str, token: &str) -> anyhow::Result<Self> {
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(15))
            .connect_timeout(Duration::from_secs(5))
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        Ok(Self { base: base.trim_end_matches('/').to_string(), token: token.to_string(), http })
    }

    async fn post<T: for<'de> Deserialize<'de>>(&self, path: &str, body: &impl Serialize) -> Result<T, CpError> {
        let resp = self
            .http
            .post(format!("{}{}", self.base, path))
            .bearer_auth(&self.token)
            .json(body)
            .send()
            .await
            .map_err(|e| CpError::Transport(e.to_string()))?;
        self.handle(resp).await
    }

    async fn get<T: for<'de> Deserialize<'de>>(&self, path: &str) -> Result<T, CpError> {
        let resp = self
            .http
            .get(format!("{}{}", self.base, path))
            .bearer_auth(&self.token)
            .send()
            .await
            .map_err(|e| CpError::Transport(e.to_string()))?;
        self.handle(resp).await
    }

    async fn handle<T: for<'de> Deserialize<'de>>(&self, resp: reqwest::Response) -> Result<T, CpError> {
        let status = resp.status().as_u16();
        let bytes = resp.bytes().await.map_err(|e| CpError::Transport(e.to_string()))?;
        if (200..300).contains(&status) {
            return serde_json::from_slice(&bytes).map_err(|e| CpError::Unexpected { status, body: format!("decode: {e}") });
        }
        if let Ok(eb) = serde_json::from_slice::<ErrorBody>(&bytes) {
            let attempt_id = eb
                .error
                .details
                .as_ref()
                .and_then(|d| d.get("attempt_id"))
                .and_then(|v| v.as_str())
                .map(str::to_string);
            return Err(CpError::Denied { status, code: eb.error.code, message: eb.error.message, attempt_id });
        }
        Err(CpError::Unexpected { status, body: String::from_utf8_lossy(&bytes).into_owned() })
    }

    pub async fn grant_key(&self) -> Result<GrantKey, CpError> {
        self.get("/internal/v1/grant-public-key").await
    }

    pub async fn begin(&self, agent_key: &str, task_id: &str, idempotency_key: &str, method: &str, url: &str) -> Result<BeginResponse, CpError> {
        #[derive(Serialize)]
        struct Req<'a> {
            agent_key: &'a str,
            task_id: &'a str,
            idempotency_key: &'a str,
            method: &'a str,
            url: &'a str,
        }
        self.post("/internal/v1/paid-requests/begin", &Req { agent_key, task_id, idempotency_key, method, url }).await
    }

    pub async fn authorize(&self, attempt_id: &str, quote: &Quote) -> Result<AuthorizeResponse, CpError> {
        #[derive(Serialize)]
        struct Req<'a> {
            quote: &'a Quote,
        }
        self.post(&format!("/internal/v1/paid-requests/{attempt_id}/authorize"), &Req { quote }).await
    }

    /// Write-ahead record: MUST succeed before the payment is transmitted.
    pub async fn submitted(&self, attempt_id: &str, grant_id: &str, payment_hash: &str) -> Result<Attempt, CpError> {
        #[derive(Serialize)]
        struct Req<'a> {
            grant_id: &'a str,
            payment_hash: &'a str,
        }
        self.post(&format!("/internal/v1/paid-requests/{attempt_id}/submitted"), &Req { grant_id, payment_hash }).await
    }

    pub async fn outcome(&self, attempt_id: &str, payment: &PaymentOutcome, service: Option<&ServiceOutcome>) -> Result<OutcomeResponse, CpError> {
        #[derive(Serialize)]
        struct Req<'a> {
            payment: &'a PaymentOutcome,
            #[serde(skip_serializing_if = "Option::is_none")]
            service: Option<&'a ServiceOutcome>,
        }
        self.post(&format!("/internal/v1/paid-requests/{attempt_id}/outcome"), &Req { payment, service }).await
    }

    /// Abandon an initiated attempt that will never be paid (no challenge,
    /// invalid challenge, resource error before payment).
    pub async fn abandon(&self, attempt_id: &str, code: &str, detail: &str) -> Result<Attempt, CpError> {
        #[derive(Serialize)]
        struct Req<'a> {
            code: &'a str,
            detail: &'a str,
        }
        self.post(&format!("/internal/v1/paid-requests/{attempt_id}/abandon"), &Req { code, detail }).await
    }
}
