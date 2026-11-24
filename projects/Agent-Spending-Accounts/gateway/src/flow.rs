//! The paid-request flow, step by step:
//!
//! 1. Validate the destination URL shape (before anything leaves the process).
//! 2. `begin` with the control plane: authenticates the agent, applies the
//!    endpoint allowlist, and de-duplicates by idempotency key.
//! 3. SSRF checks + DNS pinning; endpoint prefix re-checked locally.
//! 4. Unpaid probe request; parse and validate the 402 challenge against the
//!    policy constraints the control plane returned.
//! 5. `authorize`: the control plane atomically reserves and issues a grant.
//! 6. Verify the grant against the exact payload; have the isolated signer
//!    sign only that authorization.
//! 7. Write-ahead `submitted`, transmit the paid request, report the payment
//!    outcome and the service outcome separately.
//!
//! An unknown outcome (timeout after send, ambiguous response) is reported
//! as `unknown`; the control plane keeps the reservation until reconciled.

use crate::amount::Amount;
use crate::client::{sanitize_headers, FetchError, Fetched, Fetcher};
use crate::control_plane::{ControlPlane, CpError, PaymentOutcome, ServiceOutcome};
use crate::grant::{Binding, Verifier};
use crate::signer::HttpSigner;
use crate::urlguard::{self, Policy as UrlPolicy};
use crate::x402::{self, Authorization, Constraints, ExactPayload, PaymentHeader, Quote};
use base64::{engine::general_purpose::STANDARD as B64, Engine};
use bytes::Bytes;
use rand::RngCore;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Debug, Deserialize)]
pub struct PaidRequest {
    pub idempotency_key: String,
    #[serde(default = "default_method")]
    pub method: String,
    pub url: String,
    #[serde(default)]
    pub headers: BTreeMap<String, String>,
    #[serde(default)]
    pub body: Option<String>,
    #[serde(default)]
    pub body_base64: Option<String>,
    /// Optional caller-side ceiling, never above policy.
    #[serde(default)]
    pub max_amount: Option<Amount>,
}

fn default_method() -> String {
    "GET".into()
}

#[derive(Debug, Serialize, Clone)]
pub struct PaymentSummary {
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub amount: Option<Amount>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub asset: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub network: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pay_to: Option<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub transaction: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub payer: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Debug, Serialize, Clone)]
pub struct ServiceSummary {
    pub http_status: u16,
    pub delivered: bool,
    pub content_type: String,
    pub body_bytes: u64,
    pub truncated: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub body: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub body_base64: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct PaidResponse {
    pub attempt_id: String,
    pub replay: bool,
    pub payment: PaymentSummary,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub service: Option<ServiceSummary>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub receipt: Option<serde_json::Value>,
}

#[derive(Debug, thiserror::Error)]
#[error("{code}: {message}")]
pub struct FlowError {
    pub status: u16,
    pub code: String,
    pub message: String,
    pub attempt_id: Option<String>,
}

impl FlowError {
    fn new(status: u16, code: &str, message: impl Into<String>) -> Self {
        Self { status, code: code.into(), message: message.into(), attempt_id: None }
    }
    fn with_attempt(mut self, id: &str) -> Self {
        self.attempt_id = Some(id.to_string());
        self
    }
}

impl From<CpError> for FlowError {
    fn from(e: CpError) -> Self {
        match e {
            CpError::Denied { status, code, message, attempt_id } => {
                let status = if status == 401 { 401 } else if status == 404 { 404 } else if status == 409 { 409 } else { 403 };
                FlowError { status, code, message, attempt_id }
            }
            CpError::Transport(m) => FlowError::new(503, "control_plane_unavailable", m),
            CpError::Unexpected { status, body } => FlowError::new(502, "control_plane_error", format!("{status}: {body}")),
        }
    }
}

pub struct Gateway {
    pub cp: ControlPlane,
    pub signer: HttpSigner,
    pub verifier: Verifier,
    pub fetcher: Fetcher,
    pub url_policy: UrlPolicy,
    pub max_inline_body: usize,
}

impl Gateway {
    pub async fn execute(self: &Arc<Self>, agent_key: &str, task_id: &str, req: PaidRequest) -> Result<PaidResponse, FlowError> {
        // 1. Shape validation before any network activity.
        let method = req.method.trim().to_ascii_uppercase();
        if !matches!(method.as_str(), "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD") {
            return Err(FlowError::new(400, "invalid_request", format!("method {method:?} not supported")));
        }
        if req.idempotency_key.trim().is_empty() {
            return Err(FlowError::new(400, "invalid_request", "idempotency_key is required"));
        }
        let url = urlguard::normalize(&req.url, &self.url_policy).map_err(|e| FlowError::new(403, "url_rejected", e.to_string()))?;
        let body = match (&req.body, &req.body_base64) {
            (Some(_), Some(_)) => return Err(FlowError::new(400, "invalid_request", "provide body or body_base64, not both")),
            (Some(s), None) => Some(Bytes::from(s.clone())),
            (None, Some(b)) => Some(Bytes::from(B64.decode(b).map_err(|e| FlowError::new(400, "invalid_request", format!("body_base64: {e}")))?)),
            (None, None) => None,
        };
        let header_pairs: Vec<(String, String)> = req.headers.iter().map(|(k, v)| (k.clone(), v.clone())).collect();
        let headers = sanitize_headers(&header_pairs).map_err(|e| FlowError::new(400, "invalid_request", e.to_string()))?;

        // 2. Begin: identity, task, allowlist, idempotency (control plane).
        let begin = self.cp.begin(agent_key, task_id, req.idempotency_key.trim(), &method, url.as_str()).await?;
        let attempt_id = begin.attempt.id.clone();
        if begin.replay {
            return Ok(replay_response(begin));
        }
        let ctx = begin
            .context
            .ok_or_else(|| FlowError::new(502, "control_plane_error", "begin response lacked context").with_attempt(&attempt_id))?;

        // 3. SSRF + pinning; local allowlist re-check (defense in depth).
        urlguard::within_prefix(&ctx.endpoint_rule.url_prefix, &url)
            .map_err(|e| FlowError::new(403, "endpoint_not_allowed", e.to_string()).with_attempt(&attempt_id))?;
        let pinned = match urlguard::resolve_pinned(&url, &self.url_policy).await {
            Ok(p) => p,
            Err(e) => {
                let _ = self.cp.abandon(&attempt_id, "destination_rejected", &e.to_string()).await;
                return Err(FlowError::new(403, "destination_rejected", e.to_string()).with_attempt(&attempt_id));
            }
        };

        // 4. Probe: expect a 402 challenge.
        let probe = match self.fetcher.fetch(&method, &url, &pinned, headers.clone(), body.clone()).await {
            Ok(r) => r,
            Err(e) => {
                let _ = self.cp.abandon(&attempt_id, "resource_unreachable", &e.to_string()).await;
                return Err(FlowError::new(502, "resource_unreachable", e.to_string()).with_attempt(&attempt_id));
            }
        };
        if probe.status != 402 {
            // No payment demanded (or the resource errored). Nothing is held.
            let code = if (200..300).contains(&probe.status) { "no_payment_required" } else { "resource_error" };
            let _ = self.cp.abandon(&attempt_id, code, &format!("resource returned {}", probe.status)).await;
            if (300..400).contains(&probe.status) {
                return Err(FlowError::new(502, "redirect_refused", format!("resource answered {} with a redirect; redirects are not followed", probe.status)).with_attempt(&attempt_id));
            }
            let (status, error) = if code == "no_payment_required" {
                ("not_required".to_string(), String::new())
            } else {
                ("not_attempted".to_string(), format!("resource returned {} before any payment", probe.status))
            };
            return Ok(PaidResponse {
                attempt_id,
                replay: false,
                payment: PaymentSummary { status, amount: None, asset: None, network: None, pay_to: None, transaction: String::new(), payer: String::new(), error },
                service: Some(self.service_summary(&probe)),
                receipt: None,
            });
        }
        let challenge = match x402::parse_challenge(&probe.body) {
            Ok(c) => c,
            Err(e) => {
                let _ = self.cp.abandon(&attempt_id, "challenge_malformed", &e.to_string()).await;
                return Err(FlowError::new(502, "challenge_malformed", e.to_string()).with_attempt(&attempt_id));
            }
        };
        let constraints = Constraints {
            scheme: &ctx.asset.scheme,
            network: &ctx.asset.network,
            asset: &ctx.asset.asset,
            recipients: &ctx.endpoint_rule.recipients,
            per_request_max: ctx.per_request_max,
            client_max: req.max_amount,
        };
        let quote: Quote = match x402::select_requirement(&challenge, &constraints) {
            Ok(q) => q,
            Err(e) => {
                let _ = self.cp.abandon(&attempt_id, "challenge_rejected", &e.to_string()).await;
                return Err(FlowError::new(403, "challenge_rejected", e.to_string()).with_attempt(&attempt_id));
            }
        };
        if !quote.resource.is_empty() {
            if let Ok(ru) = urlguard::normalize(&quote.resource, &self.url_policy) {
                if urlguard::within_prefix(&ctx.endpoint_rule.url_prefix, &ru).is_err() {
                    let _ = self.cp.abandon(&attempt_id, "challenge_rejected", "resource outside endpoint").await;
                    return Err(FlowError::new(403, "challenge_rejected", "challenge resource is outside the approved endpoint").with_attempt(&attempt_id));
                }
            }
        }

        // 5. Authorize: atomic reservation + grant.
        let auth = self.cp.authorize(&attempt_id, &quote).await?;

        // 6. Verify the grant against exactly what we will pay, then sign.
        let binding = Binding {
            org_id: &ctx.organization_id,
            agent_id: &ctx.agent_id,
            task_id: &ctx.task_id,
            attempt_id: &attempt_id,
            method: &method,
            url: url.as_str(),
            quote: &quote,
        };
        let (claims, amount_max, _fee_max) = match self.verifier.verify(&auth.grant_token, &binding) {
            Ok(v) => v,
            Err(e) => {
                // Nothing has been transmitted: release immediately.
                let _ = self.cp.outcome(&attempt_id, &failed("grant verification failed: ".to_string() + &e.to_string()), None).await;
                return Err(FlowError::new(500, "grant_invalid", e.to_string()).with_attempt(&attempt_id));
            }
        };
        if claims.jti != auth.grant_id {
            let _ = self.cp.outcome(&attempt_id, &failed("grant id mismatch".into()), None).await;
            return Err(FlowError::new(500, "grant_invalid", "grant id mismatch").with_attempt(&attempt_id));
        }
        // The exact scheme pays maxAmountRequired; it can never exceed the grant ceiling.
        let value = quote.max_amount_required;
        if value > amount_max {
            let _ = self.cp.outcome(&attempt_id, &failed("value exceeds grant".into()), None).await;
            return Err(FlowError::new(500, "grant_invalid", "value exceeds grant ceiling").with_attempt(&attempt_id));
        }
        let now = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
        let timeout = if quote.max_timeout_seconds == 0 { 60 } else { quote.max_timeout_seconds as i64 };
        let valid_before = (now + timeout).min(claims.exp);
        let mut nonce = [0u8; 32];
        rand::thread_rng().fill_bytes(&mut nonce);
        let mut authorization = Authorization {
            from: String::new(),
            to: quote.pay_to.clone(),
            value: value.to_string(),
            valid_after: (now - 60).max(0).to_string(),
            valid_before: valid_before.to_string(),
            nonce: format!("0x{}", hex::encode(nonce)),
        };
        let signed = match self.signer.sign(&quote.network, &quote.asset, &authorization, &auth.grant_id).await {
            Ok(s) => s,
            Err(e) => {
                let _ = self.cp.outcome(&attempt_id, &failed(format!("signer: {e}")), None).await;
                return Err(FlowError::new(502, "signer_unavailable", e.to_string()).with_attempt(&attempt_id));
            }
        };
        authorization.from = signed.from.clone();
        let header = PaymentHeader {
            x402_version: x402::X402_VERSION,
            scheme: quote.scheme.clone(),
            network: quote.network.clone(),
            payload: ExactPayload { signature: signed.signature, authorization: authorization.clone() },
        };
        let encoded = header.encode();
        let payment_hash = x402::payment_hash(&encoded);

        // 7. Write-ahead, transmit, report.
        self.cp.submitted(&attempt_id, &auth.grant_id, &payment_hash).await?;
        let mut paid_headers = headers.clone();
        paid_headers.insert("x-payment", encoded.parse().map_err(|_| FlowError::new(500, "internal", "payment header").with_attempt(&attempt_id))?);
        paid_headers.insert("access-control-expose-headers", "X-PAYMENT-RESPONSE".parse().unwrap());
        let result = self.fetcher.fetch(&method, &url, &pinned, paid_headers, body).await;

        let (payment, service) = match result {
            Err(FetchError::Connect(m)) | Err(FetchError::Build(m)) => {
                // The payment header never left the process.
                (failed(format!("connection failed before payment was transmitted: {m}")), None)
            }
            Err(FetchError::AfterSend(m)) => (unknown(format!("response lost after payment was transmitted: {m}")), None),
            Err(FetchError::Deadline(d)) => (unknown(format!("no response within {d:?} after payment was transmitted")), None),
            Ok(resp) => {
                let service = self.service_outcome(&resp);
                let settlement = resp.header("x-payment-response").and_then(x402::parse_settlement_header);
                let payment = match settlement {
                    Some(s) if s.success => PaymentOutcome {
                        status: "settled",
                        submission_ref: s.transaction,
                        payer_ref: if s.payer.is_empty() { signed.from.clone() } else { s.payer },
                        settled_amount: value,
                        fee_actual: Amount::ZERO,
                        error: String::new(),
                    },
                    Some(s) => failed(format!("resource reported settlement failure: {}", s.error_reason.unwrap_or_default())),
                    None if resp.status == 402 => failed(format!("payment rejected by resource: {}", challenge_error(&resp))),
                    None => unknown(format!("resource answered {} without settlement evidence", resp.status)),
                };
                (payment, Some((service, resp)))
            }
        };
        let service_outcome = service.as_ref().map(|(so, _)| so.clone());
        let outcome = self.cp.outcome(&attempt_id, &payment, service_outcome.as_ref()).await?;

        Ok(PaidResponse {
            attempt_id,
            replay: false,
            payment: PaymentSummary {
                status: payment.status.to_string(),
                amount: Some(value),
                asset: Some(quote.asset.clone()),
                network: Some(quote.network.clone()),
                pay_to: Some(quote.pay_to.clone()),
                transaction: payment.submission_ref.clone(),
                payer: payment.payer_ref.clone(),
                error: payment.error.clone(),
            },
            service: service.map(|(_, resp)| self.service_summary(&resp)),
            receipt: outcome.receipt,
        })
    }

    fn service_outcome(&self, r: &Fetched) -> ServiceOutcome {
        ServiceOutcome {
            http_status: r.status,
            delivered: (200..300).contains(&r.status) && !r.truncated,
            content_type: r.content_type(),
            body_sha256: r.body_sha256(),
            body_bytes: r.body.len() as u64,
            truncated: r.truncated,
            error: if r.truncated { "response exceeded size limit".into() } else { String::new() },
        }
    }

    fn service_summary(&self, r: &Fetched) -> ServiceSummary {
        let (body, body_base64) = if r.body.len() > self.max_inline_body {
            (None, None)
        } else {
            match std::str::from_utf8(&r.body) {
                Ok(s) => (Some(s.to_string()), None),
                Err(_) => (None, Some(B64.encode(&r.body))),
            }
        };
        ServiceSummary {
            http_status: r.status,
            delivered: (200..300).contains(&r.status) && !r.truncated,
            content_type: r.content_type(),
            body_bytes: r.body.len() as u64,
            truncated: r.truncated,
            body,
            body_base64,
        }
    }
}

fn failed(error: String) -> PaymentOutcome {
    PaymentOutcome { status: "failed", submission_ref: String::new(), payer_ref: String::new(), settled_amount: Amount::ZERO, fee_actual: Amount::ZERO, error }
}

fn unknown(error: String) -> PaymentOutcome {
    PaymentOutcome { status: "unknown", submission_ref: String::new(), payer_ref: String::new(), settled_amount: Amount::ZERO, fee_actual: Amount::ZERO, error }
}

fn challenge_error(r: &Fetched) -> String {
    x402::parse_challenge(&r.body).ok().and_then(|c| c.error).unwrap_or_else(|| "no reason given".into())
}

fn replay_response(b: crate::control_plane::BeginResponse) -> PaidResponse {
    let a = b.attempt;
    let q = a.quote.as_ref();
    PaidResponse {
        attempt_id: a.id.clone(),
        replay: true,
        payment: PaymentSummary {
            status: a.status.clone(),
            amount: a.settled_amount.filter(|x| !x.is_zero()).or(q.map(|q| q.max_amount_required)),
            asset: q.map(|q| q.asset.clone()),
            network: q.map(|q| q.network.clone()),
            pay_to: q.map(|q| q.pay_to.clone()),
            transaction: a.submission_ref.clone(),
            payer: String::new(),
            error: if a.reject_reason.is_empty() { a.error.clone() } else { format!("{}: {}", a.reject_reason, a.error) },
        },
        service: None,
        receipt: b.receipt,
    }
}
