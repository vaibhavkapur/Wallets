//! asa-gateway library: x402 challenge handling, SSRF-guarded fetching,
//! execution-grant verification and the paid-request flow.
//! See `main.rs` for the HTTP server binary.

pub mod amount;
pub mod client;
pub mod control_plane;
pub mod flow;
pub mod grant;
pub mod signer;
pub mod urlguard;
pub mod x402;
