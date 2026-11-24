//! Runs every shared golden vector through the engine. The same files are
//! checked by `fixtures/tools/independent_check.py` and by the Go and
//! TypeScript suites, so all runtimes agree on one set of results.

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use optimizer_core::contract::{EvaluationRequest, EvaluationResponse, OptionStatus};
use optimizer_core::evaluate;
use serde_json::{json, Value};

fn golden_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../fixtures/golden/v1")
}

fn vectors() -> Vec<(String, Value)> {
    let mut paths: Vec<PathBuf> = fs::read_dir(golden_dir())
        .expect("golden directory exists")
        .map(|entry| entry.unwrap().path())
        .filter(|path| path.extension().is_some_and(|ext| ext == "json"))
        .collect();
    paths.sort();
    assert!(
        paths.len() >= 40,
        "expected the full golden set, found {}",
        paths.len()
    );
    paths
        .into_iter()
        .map(|path| {
            let name = path.file_name().unwrap().to_string_lossy().into_owned();
            (
                name,
                serde_json::from_str(&fs::read_to_string(&path).unwrap()).unwrap(),
            )
        })
        .collect()
}

fn wire<T: serde::Serialize>(value: &T) -> Value {
    serde_json::to_value(value).unwrap()
}

/// Projects a response onto the fields a golden vector pins down.
fn project(resp: &EvaluationResponse) -> Value {
    let mut options = BTreeMap::new();
    for opt in &resp.options {
        let mut codes: Vec<String> = opt
            .exclusions
            .iter()
            .map(|x| wire(&x.code).as_str().unwrap().to_owned())
            .collect();
        codes.sort();
        codes.dedup();
        let mut entry = json!({ "status": wire(&opt.status), "exclusion_codes": codes });
        if let Some(b) = &opt.breakdown {
            let map = entry.as_object_mut().unwrap();
            map.insert(
                "immediate_cost_micro_usd".into(),
                wire(&b.immediate_cost_micro_usd),
            );
            map.insert(
                "estimated_rewards_micro_usd".into(),
                wire(&b.estimated_rewards_total_micro_usd),
            );
            map.insert(
                "estimated_net_cost_micro_usd".into(),
                wire(&b.estimated_net_cost_micro_usd),
            );
            map.insert(
                "amount_due_base_units".into(),
                wire(&b.amount_due.base_units),
            );
        }
        options.insert(opt.option_id.clone(), entry);
    }
    let mut sensitivity_codes: Vec<String> = resp
        .sensitivities
        .iter()
        .map(|s| wire(&s.code).as_str().unwrap().to_owned())
        .collect();
    sensitivity_codes.sort();
    sensitivity_codes.dedup();
    json!({
        "outcome": wire(&resp.outcome),
        "rationale": wire(&resp.rationale),
        "preferred_option_id": resp.preferred_option_id,
        "tied_option_ids": resp.tied_option_ids,
        "lowest_cost_option_ids": resp.lowest_cost_option_ids,
        "savings_estimated_total_micro_usd":
            resp.savings_vs_baseline.as_ref().map(|s| s.estimated_total_micro_usd.to_string()),
        "expires_at": resp.expires_at.map(|t| t.to_string()),
        "sensitivity_codes": sensitivity_codes,
        "options": options,
    })
}

#[test]
fn golden_vectors() {
    let mut failures = Vec::new();
    for (name, vector) in vectors() {
        let parsed: Result<EvaluationRequest, _> =
            serde_json::from_value(vector["request"].clone());
        let actual = match parsed {
            Err(_) => Err("malformed_request".to_owned()),
            Ok(req) => evaluate(&req).map_err(|e| wire(&e.code).as_str().unwrap().to_owned()),
        };
        match (vector.get("expected_error"), actual) {
            (Some(expected), Err(code)) => {
                if expected["code"] != Value::String(code.clone()) {
                    failures.push(format!(
                        "{name}: expected error {}, got {code}",
                        expected["code"]
                    ));
                }
            }
            (Some(expected), Ok(_)) => {
                failures.push(format!("{name}: expected error {}", expected["code"]))
            }
            (None, Err(code)) => failures.push(format!("{name}: unexpected error {code}")),
            (None, Ok(resp)) => {
                let got = project(&resp);
                if got != vector["expected"] {
                    failures.push(format!(
                        "{name}:\n  expected {}\n  actual   {}",
                        vector["expected"], got
                    ));
                }
                check_invariants(&name, &resp, &mut failures);
            }
        }
    }
    assert!(failures.is_empty(), "\n{}", failures.join("\n"));
}

/// Properties every response must satisfy regardless of the scenario.
fn check_invariants(name: &str, resp: &EvaluationResponse, failures: &mut Vec<String>) {
    for opt in &resp.options {
        if (opt.status == OptionStatus::Eligible) != opt.exclusions.is_empty() {
            failures.push(format!(
                "{name}/{}: status disagrees with exclusions",
                opt.option_id
            ));
        }
        if opt.status == OptionStatus::Eligible && opt.breakdown.is_none() {
            failures.push(format!(
                "{name}/{}: eligible without a breakdown",
                opt.option_id
            ));
        }
        let Some(b) = &opt.breakdown else { continue };
        let fees: i128 = b.fees.iter().map(|f| f.micro_usd.0).sum();
        let discounts: i128 = b.discounts.iter().map(|d| d.micro_usd.0).sum();
        let rewards: i128 = b.rewards.iter().map(|r| r.micro_usd.0).sum();
        let ok = fees == b.fees_total_micro_usd.0
            && discounts == b.discounts_applied_total_micro_usd.0
            && rewards == b.estimated_rewards_total_micro_usd.0
            && discounts <= b.delivered_price_micro_usd.0
            && b.immediate_cost_micro_usd.0 == b.delivered_price_micro_usd.0 + fees - discounts
            && b.estimated_net_cost_micro_usd.0 == b.immediate_cost_micro_usd.0 - rewards;
        if !ok {
            failures.push(format!(
                "{name}/{}: breakdown lines do not add up",
                opt.option_id
            ));
        }
    }
    if let Some(pref) = &resp.preferred_option_id {
        let eligible = resp
            .options
            .iter()
            .any(|o| &o.option_id == pref && o.status == OptionStatus::Eligible);
        if !eligible {
            failures.push(format!("{name}: preferred option is not eligible"));
        }
    }
}

#[test]
fn evaluation_is_deterministic_and_round_trips() {
    for (name, vector) in vectors() {
        let Ok(req) = serde_json::from_value::<EvaluationRequest>(vector["request"].clone()) else {
            continue;
        };
        let Ok(first) = evaluate(&req) else { continue };
        let second = evaluate(&req).unwrap();
        assert_eq!(first, second, "{name}: evaluation must be deterministic");

        let json = serde_json::to_string(&first).unwrap();
        let back: EvaluationResponse = serde_json::from_str(&json).unwrap();
        assert_eq!(first, back, "{name}: response must round-trip through JSON");

        let req_json = serde_json::to_string(&req).unwrap();
        let req_back: EvaluationRequest = serde_json::from_str(&req_json).unwrap();
        assert_eq!(
            req, req_back,
            "{name}: request must round-trip through JSON"
        );
    }
}

#[test]
fn responses_never_contain_json_numbers_for_amounts() {
    fn walk(path: &str, value: &Value, failures: &mut Vec<String>) {
        match value {
            Value::Number(_) if !path.ends_with("/decimals") => failures.push(path.to_owned()),
            Value::Array(items) => {
                for (i, item) in items.iter().enumerate() {
                    walk(&format!("{path}/{i}"), item, failures);
                }
            }
            Value::Object(map) => {
                for (key, item) in map {
                    walk(&format!("{path}/{key}"), item, failures);
                }
            }
            _ => {}
        }
    }
    for (name, vector) in vectors() {
        let Ok(req) = serde_json::from_value::<EvaluationRequest>(vector["request"].clone()) else {
            continue;
        };
        let Ok(resp) = evaluate(&req) else { continue };
        let mut failures = Vec::new();
        walk("", &serde_json::to_value(&resp).unwrap(), &mut failures);
        assert!(
            failures.is_empty(),
            "{name}: numeric JSON values at {failures:?}"
        );
    }
}
