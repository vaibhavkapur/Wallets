//! Bounded outbound HTTP to resource servers.
//!
//! - Redirects are never followed (a 3xx is returned to the caller as-is).
//! - Connections go only to the DNS-pinned, SSRF-checked addresses.
//! - Response bodies are capped at a byte limit and the whole exchange at a
//!   wall-clock deadline; truncation and timeouts are reported explicitly.
//! - Agent-supplied headers pass through except for hop-by-hop, framing, and
//!   payment headers, which the gateway owns.

use bytes::Bytes;
use futures_util::StreamExt;
use http::header::{HeaderMap, HeaderName, HeaderValue};
use sha2::{Digest, Sha256};
use std::net::SocketAddr;
use std::time::{Duration, Instant};
use url::Url;

#[derive(Debug, thiserror::Error)]
pub enum FetchError {
    #[error("request could not be built: {0}")]
    Build(String),
    /// Failed before any bytes were sent (connect/TLS). Safe to treat as not delivered.
    #[error("connection failed: {0}")]
    Connect(String),
    /// Failed after the request was (possibly) sent. Outcome is unknown.
    #[error("request failed after send: {0}")]
    AfterSend(String),
    #[error("deadline of {0:?} exceeded")]
    Deadline(Duration),
}

#[derive(Debug, Clone)]
pub struct Limits {
    pub max_body_bytes: usize,
    pub deadline: Duration,
}

#[derive(Debug)]
pub struct Fetched {
    pub status: u16,
    pub headers: HeaderMap,
    pub body: Bytes,
    pub truncated: bool,
    pub elapsed: Duration,
}

impl Fetched {
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers.get(name).and_then(|v| v.to_str().ok())
    }
    pub fn content_type(&self) -> String {
        self.header("content-type").unwrap_or("").to_string()
    }
    pub fn body_sha256(&self) -> String {
        hex::encode(Sha256::digest(&self.body))
    }
}

const BLOCKED_REQUEST_HEADERS: &[&str] = &[
    "host", "content-length", "transfer-encoding", "connection", "keep-alive", "upgrade", "te", "trailer",
    "proxy-authorization", "proxy-connection", "x-payment", "x-payment-response", "expect",
];

/// Build the header map from agent-supplied pairs, dropping gateway-owned headers.
pub fn sanitize_headers(pairs: &[(String, String)]) -> Result<HeaderMap, FetchError> {
    let mut map = HeaderMap::new();
    for (k, v) in pairs {
        let lk = k.to_ascii_lowercase();
        if BLOCKED_REQUEST_HEADERS.contains(&lk.as_str()) || lk.starts_with("sec-") {
            continue;
        }
        let name = HeaderName::from_bytes(lk.as_bytes()).map_err(|e| FetchError::Build(format!("header {k:?}: {e}")))?;
        let value = HeaderValue::from_str(v).map_err(|e| FetchError::Build(format!("header {k:?}: {e}")))?;
        map.append(name, value);
    }
    Ok(map)
}

pub struct Fetcher {
    pub limits: Limits,
}

impl Fetcher {
    /// Perform one request to `url` connecting only to `pinned`.
    pub async fn fetch(
        &self,
        method: &str,
        url: &Url,
        pinned: &[SocketAddr],
        headers: HeaderMap,
        body: Option<Bytes>,
    ) -> Result<Fetched, FetchError> {
        let start = Instant::now();
        let host = url.host_str().ok_or_else(|| FetchError::Build("missing host".into()))?.to_string();
        let mut builder = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(self.limits.deadline)
            .connect_timeout(Duration::from_secs(10))
            .no_proxy()
            .user_agent("asa-gateway/0.1");
        // Pin every validated address; reqwest tries them in order.
        builder = builder.resolve_to_addrs(&host, pinned);
        let client = builder.build().map_err(|e| FetchError::Build(e.to_string()))?;

        let m = http::Method::from_bytes(method.as_bytes()).map_err(|e| FetchError::Build(e.to_string()))?;
        let mut req = client.request(m, url.clone()).headers(headers);
        if let Some(b) = body {
            req = req.body(b);
        }
        let resp = match req.send().await {
            Ok(r) => r,
            Err(e) => {
                if e.is_connect() || e.is_builder() {
                    return Err(FetchError::Connect(e.to_string()));
                }
                if e.is_timeout() {
                    return Err(FetchError::Deadline(self.limits.deadline));
                }
                return Err(FetchError::AfterSend(e.to_string()));
            }
        };
        let status = resp.status().as_u16();
        let headers = resp.headers().clone();
        let mut stream = resp.bytes_stream();
        let mut buf: Vec<u8> = Vec::new();
        let mut truncated = false;
        while let Some(chunk) = stream.next().await {
            if start.elapsed() > self.limits.deadline {
                return Err(FetchError::Deadline(self.limits.deadline));
            }
            let chunk = match chunk {
                Ok(c) => c,
                Err(e) => {
                    if e.is_timeout() {
                        return Err(FetchError::Deadline(self.limits.deadline));
                    }
                    return Err(FetchError::AfterSend(e.to_string()));
                }
            };
            let room = self.limits.max_body_bytes.saturating_sub(buf.len());
            if chunk.len() > room {
                buf.extend_from_slice(&chunk[..room]);
                truncated = true;
                break; // drop the connection; we will not read more
            }
            buf.extend_from_slice(&chunk);
        }
        Ok(Fetched { status, headers, body: Bytes::from(buf), truncated, elapsed: start.elapsed() })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strips_gateway_owned_headers() {
        let h = sanitize_headers(&[
            ("X-PAYMENT".into(), "forged".into()),
            ("Host".into(), "evil".into()),
            ("Content-Length".into(), "1".into()),
            ("Sec-Fetch-Mode".into(), "x".into()),
            ("X-Api-Key".into(), "k".into()),
            ("Accept".into(), "application/json".into()),
        ])
        .unwrap();
        assert!(h.get("x-payment").is_none());
        assert!(h.get("host").is_none());
        assert!(h.get("content-length").is_none());
        assert!(h.get("sec-fetch-mode").is_none());
        assert_eq!(h.get("x-api-key").unwrap(), "k");
        assert_eq!(h.get("accept").unwrap(), "application/json");
        assert!(sanitize_headers(&[("bad header".into(), "x".into())]).is_err());
        assert!(sanitize_headers(&[("ok".into(), "bad\nvalue".into())]).is_err());
    }
}
