//! Internal evaluation service.
//!
//! Stateless and free of provider credentials: it holds one shared bearer
//! token for the orchestrator and nothing else. Every response carries the
//! SHA-256 of the exact request bytes it was computed from.

use std::sync::Arc;

use axum::body::Bytes;
use axum::extract::{DefaultBodyLimit, State};
use axum::http::{header, HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use optimizer_core::contract::{EvaluationRequest, EvaluationResponse};
use optimizer_core::{
    evaluate, RequestError, CONTRACT_VERSION, ENGINE_VERSION, FORMULA_VERSION, ROUNDING_VERSION,
};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;

/// Requests are small; anything larger is not a legitimate evaluation.
pub const MAX_BODY_BYTES: usize = 1024 * 1024;
/// Shortest shared token the service will start with.
pub const MIN_TOKEN_BYTES: usize = 24;

pub struct AppState {
    token_digest: [u8; 32],
}

impl AppState {
    pub fn new(token: &str) -> Result<AppState, &'static str> {
        if token.len() < MIN_TOKEN_BYTES {
            return Err("ENGINE_AUTH_TOKEN must be at least 24 bytes");
        }
        Ok(AppState {
            token_digest: Sha256::digest(token.as_bytes()).into(),
        })
    }

    fn authorized(&self, headers: &HeaderMap) -> bool {
        let Some(presented) = headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.strip_prefix("Bearer "))
        else {
            return false;
        };
        // Compare digests so the comparison is constant-time and length-independent.
        let digest: [u8; 32] = Sha256::digest(presented.as_bytes()).into();
        digest.ct_eq(&self.token_digest).into()
    }
}

#[derive(Debug, Serialize, Deserialize, JsonSchema)]
pub struct EvaluateEnvelope {
    /// SHA-256 of the request body exactly as received.
    pub input_sha256: String,
    pub result: EvaluationResponse,
}

#[derive(Debug, Serialize, Deserialize, JsonSchema)]
pub struct ErrorEnvelope {
    pub error: ErrorBody,
}

#[derive(Debug, Serialize, Deserialize, JsonSchema)]
pub struct ErrorBody {
    /// `unauthorized`, `malformed_request`, or a request error code.
    pub code: String,
    pub message: String,
    pub field: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, JsonSchema)]
pub struct Versions {
    pub contract_version: String,
    pub engine_version: String,
    pub formula_version: String,
    pub rounding_version: String,
}

/// Everything that crosses the engine boundary, for schema export.
#[derive(JsonSchema)]
#[allow(dead_code)]
pub struct EngineContractV1 {
    request: EvaluationRequest,
    response: EvaluateEnvelope,
    error: ErrorEnvelope,
    versions: Versions,
}

pub fn contract_schema() -> serde_json::Value {
    serde_json::to_value(schemars::schema_for!(EngineContractV1)).expect("schema serializes")
}

fn error(status: StatusCode, code: &str, message: String, field: Option<String>) -> Response {
    (
        status,
        Json(ErrorEnvelope {
            error: ErrorBody {
                code: code.to_owned(),
                message,
                field,
            },
        }),
    )
        .into_response()
}

fn request_error(e: RequestError) -> Response {
    let code = serde_json::to_value(e.code)
        .ok()
        .and_then(|v| v.as_str().map(str::to_owned))
        .unwrap_or_else(|| "invalid_request".to_owned());
    error(
        StatusCode::UNPROCESSABLE_ENTITY,
        &code,
        e.message,
        Some(e.field),
    )
}

async fn evaluate_handler(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !state.authorized(&headers) {
        return error(
            StatusCode::UNAUTHORIZED,
            "unauthorized",
            "missing or invalid bearer token".into(),
            None,
        );
    }
    let request: EvaluationRequest = match serde_json::from_slice(&body) {
        Ok(request) => request,
        Err(e) => {
            return error(
                StatusCode::BAD_REQUEST,
                "malformed_request",
                e.to_string(),
                None,
            )
        }
    };
    match evaluate(&request) {
        Ok(result) => {
            let input_sha256 = hex::encode(Sha256::digest(&body));
            Json(EvaluateEnvelope {
                input_sha256,
                result,
            })
            .into_response()
        }
        Err(e) => request_error(e),
    }
}

async fn versions_handler() -> Json<Versions> {
    Json(Versions {
        contract_version: CONTRACT_VERSION.to_owned(),
        engine_version: ENGINE_VERSION.to_owned(),
        formula_version: FORMULA_VERSION.to_owned(),
        rounding_version: ROUNDING_VERSION.to_owned(),
    })
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/v1/evaluate", post(evaluate_handler))
        .route("/v1/versions", get(versions_handler))
        .route(
            "/healthz",
            get(|| async { Json(serde_json::json!({ "status": "ok" })) }),
        )
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES))
        .with_state(Arc::new(state))
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::Request;
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    const TOKEN: &str = "test-token-0123456789abcdef";

    fn golden(name: &str) -> serde_json::Value {
        let path = format!(
            "{}/../../../fixtures/golden/v1/{name}.json",
            env!("CARGO_MANIFEST_DIR")
        );
        serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap()
    }

    async fn call(auth: Option<&str>, body: Vec<u8>) -> (StatusCode, serde_json::Value) {
        let mut request = Request::post("/v1/evaluate").header("content-type", "application/json");
        if let Some(auth) = auth {
            request = request.header("authorization", auth);
        }
        let response = router(AppState::new(TOKEN).unwrap())
            .oneshot(request.body(Body::from(body)).unwrap())
            .await
            .unwrap();
        let status = response.status();
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        (
            status,
            serde_json::from_slice(&bytes).unwrap_or(serde_json::Value::Null),
        )
    }

    fn bearer() -> String {
        format!("Bearer {TOKEN}")
    }

    #[tokio::test]
    async fn rejects_missing_wrong_and_malformed_credentials() {
        let body = serde_json::to_vec(&golden("001-plan-example")["request"]).unwrap();
        for auth in [
            None,
            Some("Bearer wrong-token-0123456789abcdef"),
            Some(TOKEN),
            Some("Basic abc"),
        ] {
            let (status, json) = call(auth, body.clone()).await;
            assert_eq!(status, StatusCode::UNAUTHORIZED);
            assert_eq!(json["error"]["code"], "unauthorized");
        }
    }

    #[tokio::test]
    async fn evaluates_and_binds_result_to_input_hash() {
        let body = serde_json::to_vec(&golden("001-plan-example")["request"]).unwrap();
        let (status, json) = call(Some(&bearer()), body.clone()).await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(json["input_sha256"], hex::encode(Sha256::digest(&body)));
        assert_eq!(json["result"]["outcome"], "preferred");
        assert_eq!(json["result"]["preferred_option_id"], "card");
        assert_eq!(json["result"]["formula_version"], FORMULA_VERSION);
        assert_eq!(json["result"]["rounding_version"], ROUNDING_VERSION);
    }

    #[tokio::test]
    async fn malformed_json_is_a_400_and_invalid_input_a_422() {
        let (status, json) = call(Some(&bearer()), b"{not json".to_vec()).await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert_eq!(json["error"]["code"], "malformed_request");

        let numeric = serde_json::to_vec(&golden("902-amount-as-json-number")["request"]).unwrap();
        let (status, json) = call(Some(&bearer()), numeric).await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert_eq!(json["error"]["code"], "malformed_request");

        let zero = serde_json::to_vec(&golden("900-zero-delivered-price")["request"]).unwrap();
        let (status, json) = call(Some(&bearer()), zero).await;
        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(json["error"]["code"], "invalid_amount");
        assert_eq!(
            json["error"]["field"],
            "/options/0/quote/delivered_price/base_units"
        );
    }

    #[tokio::test]
    async fn oversized_bodies_are_refused() {
        let (status, _) = call(Some(&bearer()), vec![b' '; MAX_BODY_BYTES + 1]).await;
        assert_eq!(status, StatusCode::PAYLOAD_TOO_LARGE);
    }

    #[test]
    fn short_tokens_are_refused_at_startup() {
        assert!(AppState::new("short").is_err());
    }

    #[test]
    fn schema_export_covers_request_and_response() {
        let schema = contract_schema();
        let defs = schema["$defs"].as_object().unwrap();
        for name in [
            "EvaluationRequest",
            "EvaluationResponse",
            "Units",
            "MicroUsd",
            "ExclusionCode",
        ] {
            assert!(defs.contains_key(name), "{name} missing from schema");
        }
    }
}
