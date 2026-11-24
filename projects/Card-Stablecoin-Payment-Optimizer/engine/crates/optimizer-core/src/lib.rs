//! Authoritative card-versus-stablecoin calculation engine.
//!
//! `evaluate` is a pure function of its request: no clock, no I/O, no
//! floating point. For each payment option it computes
//!
//! ```text
//! estimated net cost = delivered price
//!                    + incremental payment / FX / funding / network fees
//!                    - immediately usable discounts
//!                    - conservatively valued eligible rewards
//! ```
//!
//! in micro-USD (1 USD = 1,000,000) with checked `i128` arithmetic. Costs round
//! up and benefits round down. See `docs/calculation.md` for the full rules.

pub mod amount;
pub mod contract;
pub mod engine;
pub mod error;
mod validate;

pub use contract::{EvaluationRequest, EvaluationResponse};
pub use engine::evaluate;
pub use error::{RequestError, RequestErrorCode};

/// Version of the request/response contract.
pub const CONTRACT_VERSION: &str = "1";
/// Version of the net cost formula and decision rules.
pub const FORMULA_VERSION: &str = "net-cost-v1";
/// Version of the rounding rules (micro-USD, costs up, benefits down).
pub const ROUNDING_VERSION: &str = "micro-usd-directed-v1";
/// Version of this crate.
pub const ENGINE_VERSION: &str = env!("CARGO_PKG_VERSION");
