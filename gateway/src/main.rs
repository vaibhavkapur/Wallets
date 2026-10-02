//! asa-gateway: the Rust payment/resource gateway for Agent Spending Accounts.
//!
//! Serves `POST /v1/tasks/{task_id}/paid-requests` under an agent credential.
//! It cannot grant or increase budgets; it acts only on short-lived,
//! single-use execution grants issued by the Go control plane after an
//! atomic reservation.
//!
//! Environment:
//!   LISTEN_ADDR                default 0.0.0.0:8402
//!   CONTROL_PLANE_URL          e.g. http://127.0.0.1:8080
//!   INTERNAL_TOKEN             shared secret for the control plane's internal API
//!   SIGNER_URL                 signing provider base URL
//!   SIGNER_TOKEN               signing provider credential
//!   GRANT_PUBLIC_KEY_BASE64    optional; fetched from the control plane when unset
//!   MAX_RESPONSE_BYTES         default 1048576
//!   REQUEST_DEADLINE_SECONDS   default 30 (per outbound request)
//!   ALLOW_HTTP                 "true" to allow plain-http resources (development only)
//!   ALLOW_PRIVATE_TARGETS      "true" to allow loopback/private destinations (development only)

use asa_gateway::{client, control_plane, flow, grant, signer, urlguard};
use axum::{
    extract::{Path, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::{get, post},
    Json, Router,
};
use base64::{engine::general_purpose::STANDARD as B64, Engine};
use std::sync::Arc;
use std::time::Duration;
use tracing::{info, warn};

type Shared = Arc<flow::Gateway>;

fn env(k: &str) -> Option<String> {
    std::env::var(k).ok().filter(|v| !v.is_empty())
}

fn env_bool(k: &str) -> bool {
    matches!(env(k).as_deref(), Some("1") | Some("true") | Some("TRUE") | Some("yes"))
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()))
        .init();

    let cp_url = env("CONTROL_PLANE_URL").ok_or_else(|| anyhow::anyhow!("CONTROL_PLANE_URL is required"))?;
    let internal = env("INTERNAL_TOKEN").ok_or_else(|| anyhow::anyhow!("INTERNAL_TOKEN is required"))?;
    let signer_url = env("SIGNER_URL").ok_or_else(|| anyhow::anyhow!("SIGNER_URL is required"))?;
    let signer_token = env("SIGNER_TOKEN").unwrap_or_default();
    let cp = control_plane::ControlPlane::new(&cp_url, &internal)?;

    let public_key = match env("GRANT_PUBLIC_KEY_BASE64") {
        Some(b) => B64.decode(b.trim())?,
        None => {
            let mut last = None;
            let mut key = None;
            for attempt in 0..30 {
                match cp.grant_key().await {
                    Ok(k) => {
                        info!(kid = %k.kid, "fetched grant public key from control plane");
                        key = Some(B64.decode(k.public_key_base64.trim())?);
                        break;
                    }
                    Err(e) => {
                        last = Some(e);
                        if attempt == 0 {
                            warn!("control plane not ready; retrying");
                        }
                        tokio::time::sleep(Duration::from_secs(1)).await;
                    }
                }
            }
            key.ok_or_else(|| anyhow::anyhow!("could not fetch grant public key: {:?}", last))?
        }
    };
    let verifier = grant::Verifier::new(&public_key).map_err(|e| anyhow::anyhow!(e.to_string()))?;

    let url_policy = urlguard::Policy { allow_http: env_bool("ALLOW_HTTP"), allow_private_targets: env_bool("ALLOW_PRIVATE_TARGETS") };
    if url_policy.allow_http || url_policy.allow_private_targets {
        warn!(allow_http = url_policy.allow_http, allow_private_targets = url_policy.allow_private_targets, "development-only URL policy relaxations are enabled");
    }
    let gateway = Arc::new(flow::Gateway {
        cp,
        signer: signer::HttpSigner::new(&signer_url, &signer_token)?,
        verifier,
        fetcher: client::Fetcher {
            limits: client::Limits {
                max_body_bytes: env("MAX_RESPONSE_BYTES").and_then(|v| v.parse().ok()).unwrap_or(1024 * 1024),
                deadline: Duration::from_secs(env("REQUEST_DEADLINE_SECONDS").and_then(|v| v.parse().ok()).unwrap_or(30)),
            },
        },
        url_policy,
        max_inline_body: 256 * 1024,
    });

    let app = Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/v1/tasks/:task_id/paid-requests", post(paid_request))
        .layer(axum::extract::DefaultBodyLimit::max(2 * 1024 * 1024))
        .with_state(gateway);

    let addr = env("LISTEN_ADDR").unwrap_or_else(|| "0.0.0.0:8402".into());
    let listener = tokio::net::TcpListener::bind(&addr).await?;
    info!(%addr, "asa-gateway listening");
    axum::serve(listener, app).with_graceful_shutdown(async { let _ = tokio::signal::ctrl_c().await; }).await?;
    Ok(())
}

fn bearer(headers: &HeaderMap) -> Option<String> {
    let v = headers.get("authorization")?.to_str().ok()?;
    let (scheme, rest) = v.split_once(' ')?;
    if scheme.eq_ignore_ascii_case("bearer") {
        Some(rest.trim().to_string())
    } else {
        None
    }
}

#[derive(serde::Serialize)]
struct ErrorBody<'a> {
    error: ErrorInner<'a>,
}

#[derive(serde::Serialize)]
struct ErrorInner<'a> {
    code: &'a str,
    message: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    attempt_id: Option<&'a str>,
}

fn error_response(status: u16, code: &str, message: &str, attempt_id: Option<&str>) -> Response {
    let st = StatusCode::from_u16(status).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
    (st, Json(ErrorBody { error: ErrorInner { code, message, attempt_id } })).into_response()
}

async fn paid_request(
    State(gw): State<Shared>,
    Path(task_id): Path<String>,
    headers: HeaderMap,
    body: Result<Json<flow::PaidRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let Some(agent_key) = bearer(&headers) else {
        return error_response(401, "unauthorized", "agent API key required", None);
    };
    if !agent_key.starts_with("agent_") {
        return error_response(401, "unauthorized", "agent API key required", None);
    }
    let req = match body {
        Ok(Json(r)) => r,
        Err(e) => return error_response(400, "invalid_request", &e.body_text(), None),
    };
    // Never log credentials, bodies or payment headers.
    info!(task = %task_id, url = %req.url, method = %req.method, "paid request");
    match gw.execute(&agent_key, &task_id, req).await {
        Ok(resp) => (StatusCode::OK, Json(resp)).into_response(),
        Err(e) => {
            warn!(code = %e.code, status = e.status, attempt = ?e.attempt_id, "paid request denied/failed: {}", e.message);
            error_response(e.status, &e.code, &e.message, e.attempt_id.as_deref())
        }
    }
}
