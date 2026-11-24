//! Bounded, read-only batch settlement reconciliation utility.
//!
//! Parses normalized ledger and provider settlement statement JSON files and produces
//! deterministic discrepancy reports that match the Go reference path (`ReconcileStatementBatch`).
//! By design (per plan.md), this utility NEVER mutates balances or initiates payouts.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StatementRecord {
    pub reference_id: String,
    pub asset: String,
    pub network: String,
    pub decimals: i32,
    pub amount_base: i64,
    pub fee_base: i64,
    pub status: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StatementBatchInput {
    pub batch_id: String,
    pub ledger_records: Vec<StatementRecord>,
    pub provider_records: Vec<StatementRecord>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StatementDiscrepancy {
    pub kind: String,
    pub reference_id: String,
    pub asset: String,
    pub network: String,
    pub ledger_amount: i64,
    pub provider_amount: i64,
    pub delta_amount: i64,
    pub detail: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StatementReconciliationReport {
    pub batch_id: String,
    pub ledger_count: usize,
    pub provider_count: usize,
    pub matched_count: usize,
    pub discrepancy_count: usize,
    pub discrepancies: Vec<StatementDiscrepancy>,
}

/// Pure, deterministic batch reconciler matching Go's `ReconcileStatementBatch`.
pub fn reconcile_statement_batch(input: &StatementBatchInput) -> StatementReconciliationReport {
    let mut ledger_map: BTreeMap<&str, &StatementRecord> = BTreeMap::new();
    for rec in &input.ledger_records {
        ledger_map.insert(rec.reference_id.as_str(), rec);
    }

    let mut provider_map: BTreeMap<&str, &StatementRecord> = BTreeMap::new();
    for rec in &input.provider_records {
        provider_map.insert(rec.reference_id.as_str(), rec);
    }

    let mut all_refs: BTreeSet<&str> = BTreeSet::new();
    for &k in ledger_map.keys() {
        all_refs.insert(k);
    }
    for &k in provider_map.keys() {
        all_refs.insert(k);
    }

    let mut discrepancies = Vec::new();
    let mut matched_count = 0usize;

    for ref_id in all_refs {
        match (ledger_map.get(ref_id), provider_map.get(ref_id)) {
            (Some(l), None) => {
                discrepancies.push(StatementDiscrepancy {
                    kind: "missing_in_provider".to_string(),
                    reference_id: ref_id.to_string(),
                    asset: l.asset.clone(),
                    network: l.network.clone(),
                    ledger_amount: l.amount_base,
                    provider_amount: 0,
                    delta_amount: -l.amount_base,
                    detail: format!(
                        "reference {} present in ledger ({}) but missing in provider statement",
                        ref_id, l.amount_base
                    ),
                });
            }
            (None, Some(p)) => {
                discrepancies.push(StatementDiscrepancy {
                    kind: "missing_in_ledger".to_string(),
                    reference_id: ref_id.to_string(),
                    asset: p.asset.clone(),
                    network: p.network.clone(),
                    ledger_amount: 0,
                    provider_amount: p.amount_base,
                    delta_amount: p.amount_base,
                    detail: format!(
                        "reference {} present in provider statement ({}) but missing in ledger",
                        ref_id, p.amount_base
                    ),
                });
            }
            (Some(l), Some(p)) => {
                let mut row_has_issue = false;
                if l.asset != p.asset || l.network != p.network {
                    row_has_issue = true;
                    discrepancies.push(StatementDiscrepancy {
                        kind: "asset_mismatch".to_string(),
                        reference_id: ref_id.to_string(),
                        asset: l.asset.clone(),
                        network: l.network.clone(),
                        ledger_amount: l.amount_base,
                        provider_amount: p.amount_base,
                        delta_amount: p.amount_base - l.amount_base,
                        detail: format!(
                            "asset/network mismatch: ledger={}/{} provider={}/{}",
                            l.asset, l.network, p.asset, p.network
                        ),
                    });
                }
                if l.amount_base != p.amount_base {
                    row_has_issue = true;
                    discrepancies.push(StatementDiscrepancy {
                        kind: "amount_mismatch".to_string(),
                        reference_id: ref_id.to_string(),
                        asset: l.asset.clone(),
                        network: l.network.clone(),
                        ledger_amount: l.amount_base,
                        provider_amount: p.amount_base,
                        delta_amount: p.amount_base - l.amount_base,
                        detail: format!(
                            "amount mismatch: ledger={} provider={}",
                            l.amount_base, p.amount_base
                        ),
                    });
                }
                if l.fee_base != p.fee_base {
                    row_has_issue = true;
                    discrepancies.push(StatementDiscrepancy {
                        kind: "fee_mismatch".to_string(),
                        reference_id: ref_id.to_string(),
                        asset: l.asset.clone(),
                        network: l.network.clone(),
                        ledger_amount: l.fee_base,
                        provider_amount: p.fee_base,
                        delta_amount: p.fee_base - l.fee_base,
                        detail: format!(
                            "fee mismatch: ledger={} provider={}",
                            l.fee_base, p.fee_base
                        ),
                    });
                }
                if l.status != p.status {
                    row_has_issue = true;
                    discrepancies.push(StatementDiscrepancy {
                        kind: "status_mismatch".to_string(),
                        reference_id: ref_id.to_string(),
                        asset: l.asset.clone(),
                        network: l.network.clone(),
                        ledger_amount: l.amount_base,
                        provider_amount: p.amount_base,
                        delta_amount: p.amount_base - l.amount_base,
                        detail: format!(
                            "status mismatch: ledger={} provider={}",
                            l.status, p.status
                        ),
                    });
                }
                if !row_has_issue {
                    matched_count += 1;
                }
            }
            (None, None) => {}
        }
    }

    StatementReconciliationReport {
        batch_id: input.batch_id.clone(),
        ledger_count: input.ledger_records.len(),
        provider_count: input.provider_records.len(),
        matched_count,
        discrepancy_count: discrepancies.len(),
        discrepancies,
    }
}

// --- Minimal zero-dependency JSON parser & deterministic formatter ---

#[derive(Debug)]
pub struct ParseError(pub String);

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "json parse error: {}", self.0)
    }
}

impl std::error::Error for ParseError {}

#[derive(Debug, Clone, PartialEq)]
enum JsonVal {
    Str(String),
    Int(i64),
    Arr(Vec<JsonVal>),
    Obj(BTreeMap<String, JsonVal>),
    Bool(bool),
    Null,
}

struct Parser<'a> {
    bytes: &'a [u8],
    pos: usize,
}

impl<'a> Parser<'a> {
    fn new(src: &'a str) -> Self {
        Self {
            bytes: src.as_bytes(),
            pos: 0,
        }
    }

    fn skip_ws(&mut self) {
        while self.pos < self.bytes.len() && self.bytes[self.pos].is_ascii_whitespace() {
            self.pos += 1;
        }
    }

    fn peek(&mut self) -> Option<u8> {
        self.skip_ws();
        self.bytes.get(self.pos).copied()
    }

    fn expect(&mut self, ch: u8) -> Result<(), ParseError> {
        self.skip_ws();
        match self.bytes.get(self.pos) {
            Some(&b) if b == ch => {
                self.pos += 1;
                Ok(())
            }
            _ => Err(ParseError(format!(
                "expected '{}' at byte {}",
                ch as char, self.pos
            ))),
        }
    }

    fn parse_value(&mut self) -> Result<JsonVal, ParseError> {
        match self.peek() {
            Some(b'{') => self.parse_object(),
            Some(b'[') => self.parse_array(),
            Some(b'"') => self.parse_string().map(JsonVal::Str),
            Some(b'-' | b'0'..=b'9') => self.parse_int().map(JsonVal::Int),
            Some(b't') => {
                self.consume_lit(b"true")?;
                Ok(JsonVal::Bool(true))
            }
            Some(b'f') => {
                self.consume_lit(b"false")?;
                Ok(JsonVal::Bool(false))
            }
            Some(b'n') => {
                self.consume_lit(b"null")?;
                Ok(JsonVal::Null)
            }
            _ => Err(ParseError(format!("unexpected token at byte {}", self.pos))),
        }
    }

    fn consume_lit(&mut self, lit: &[u8]) -> Result<(), ParseError> {
        self.skip_ws();
        if self.bytes[self.pos..].starts_with(lit) {
            self.pos += lit.len();
            Ok(())
        } else {
            Err(ParseError(format!("invalid literal at byte {}", self.pos)))
        }
    }

    fn parse_string(&mut self) -> Result<String, ParseError> {
        self.expect(b'"')?;
        let mut out = String::new();
        while self.pos < self.bytes.len() {
            let b = self.bytes[self.pos];
            self.pos += 1;
            if b == b'"' {
                return Ok(out);
            }
            if b == b'\\' {
                if self.pos >= self.bytes.len() {
                    return Err(ParseError("unterminated escape".to_string()));
                }
                let esc = self.bytes[self.pos];
                self.pos += 1;
                match esc {
                    b'"' => out.push('"'),
                    b'\\' => out.push('\\'),
                    b'/' => out.push('/'),
                    b'n' => out.push('\n'),
                    b'r' => out.push('\r'),
                    b't' => out.push('\t'),
                    _ => return Err(ParseError(format!("unsupported escape \\{}", esc as char))),
                }
            } else {
                out.push(b as char);
            }
        }
        Err(ParseError("unterminated string".to_string()))
    }

    fn parse_int(&mut self) -> Result<i64, ParseError> {
        self.skip_ws();
        let start = self.pos;
        if self.bytes.get(self.pos) == Some(&b'-') {
            self.pos += 1;
        }
        while self.pos < self.bytes.len() && self.bytes[self.pos].is_ascii_digit() {
            self.pos += 1;
        }
        let s = std::str::from_utf8(&self.bytes[start..self.pos])
            .map_err(|e| ParseError(e.to_string()))?;
        s.parse::<i64>()
            .map_err(|e| ParseError(format!("invalid integer '{}': {}", s, e)))
    }

    fn parse_array(&mut self) -> Result<JsonVal, ParseError> {
        self.expect(b'[')?;
        let mut items = Vec::new();
        if self.peek() == Some(b']') {
            self.pos += 1;
            return Ok(JsonVal::Arr(items));
        }
        loop {
            items.push(self.parse_value()?);
            match self.peek() {
                Some(b',') => {
                    self.pos += 1;
                }
                Some(b']') => {
                    self.pos += 1;
                    return Ok(JsonVal::Arr(items));
                }
                _ => return Err(ParseError(format!("expected ',' or ']' at {}", self.pos))),
            }
        }
    }

    fn parse_object(&mut self) -> Result<JsonVal, ParseError> {
        self.expect(b'{')?;
        let mut map = BTreeMap::new();
        if self.peek() == Some(b'}') {
            self.pos += 1;
            return Ok(JsonVal::Obj(map));
        }
        loop {
            let key = self.parse_string()?;
            self.expect(b':')?;
            let val = self.parse_value()?;
            map.insert(key, val);
            match self.peek() {
                Some(b',') => {
                    self.pos += 1;
                }
                Some(b'}') => {
                    self.pos += 1;
                    return Ok(JsonVal::Obj(map));
                }
                _ => return Err(ParseError(format!("expected ',' or '}}' at {}", self.pos))),
            }
        }
    }
}

fn get_str(obj: &BTreeMap<String, JsonVal>, key: &str) -> Result<String, ParseError> {
    match obj.get(key) {
        Some(JsonVal::Str(s)) => Ok(s.clone()),
        _ => Err(ParseError(format!("missing string field '{}'", key))),
    }
}

fn get_amount(obj: &BTreeMap<String, JsonVal>, key: &str) -> Result<i64, ParseError> {
    match obj.get(key) {
        Some(JsonVal::Str(s)) => s
            .parse::<i64>()
            .map_err(|e| ParseError(format!("invalid decimal string '{}': {}", key, e))),
        Some(JsonVal::Int(n)) => Ok(*n),
        _ => Err(ParseError(format!("missing integer/string amount '{}'", key))),
    }
}

fn parse_records(val: Option<&JsonVal>) -> Result<Vec<StatementRecord>, ParseError> {
    let arr = match val {
        Some(JsonVal::Arr(a)) => a,
        _ => return Err(ParseError("expected array of statement records".to_string())),
    };
    let mut out = Vec::with_capacity(arr.len());
    for item in arr {
        let obj = match item {
            JsonVal::Obj(m) => m,
            _ => return Err(ParseError("record must be JSON object".to_string())),
        };
        out.push(StatementRecord {
            reference_id: get_str(obj, "reference_id")?,
            asset: get_str(obj, "asset")?,
            network: get_str(obj, "network")?,
            decimals: get_amount(obj, "decimals")? as i32,
            amount_base: get_amount(obj, "amount_base")?,
            fee_base: get_amount(obj, "fee_base")?,
            status: get_str(obj, "status")?,
        });
    }
    Ok(out)
}

pub fn parse_batch_input(src: &str) -> Result<StatementBatchInput, ParseError> {
    let mut parser = Parser::new(src);
    let root = match parser.parse_value()? {
        JsonVal::Obj(m) => m,
        _ => return Err(ParseError("root must be JSON object".to_string())),
    };
    Ok(StatementBatchInput {
        batch_id: get_str(&root, "batch_id")?,
        ledger_records: parse_records(root.get("ledger_records"))?,
        provider_records: parse_records(root.get("provider_records"))?,
    })
}

fn escape_json(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for ch in s.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c => out.push(c),
        }
    }
    out
}

/// Formats report using identical key order and decimal-string integer encoding as Go.
pub fn format_report_json(rep: &StatementReconciliationReport) -> String {
    let mut out = String::new();
    out.push_str("{\n");
    out.push_str(&format!(
        "  \"batch_id\": \"{}\",\n",
        escape_json(&rep.batch_id)
    ));
    out.push_str(&format!("  \"ledger_count\": {},\n", rep.ledger_count));
    out.push_str(&format!("  \"provider_count\": {},\n", rep.provider_count));
    out.push_str(&format!("  \"matched_count\": {},\n", rep.matched_count));
    out.push_str(&format!(
        "  \"discrepancy_count\": {},\n",
        rep.discrepancy_count
    ));
    if rep.discrepancies.is_empty() {
        out.push_str("  \"discrepancies\": []\n");
    } else {
        out.push_str("  \"discrepancies\": [\n");
        for (idx, d) in rep.discrepancies.iter().enumerate() {
            out.push_str("    {\n");
            out.push_str(&format!("      \"kind\": \"{}\",\n", escape_json(&d.kind)));
            out.push_str(&format!(
                "      \"reference_id\": \"{}\",\n",
                escape_json(&d.reference_id)
            ));
            out.push_str(&format!(
                "      \"asset\": \"{}\",\n",
                escape_json(&d.asset)
            ));
            out.push_str(&format!(
                "      \"network\": \"{}\",\n",
                escape_json(&d.network)
            ));
            out.push_str(&format!("      \"ledger_amount\": \"{}\",\n", d.ledger_amount));
            out.push_str(&format!(
                "      \"provider_amount\": \"{}\",\n",
                d.provider_amount
            ));
            out.push_str(&format!("      \"delta_amount\": \"{}\",\n", d.delta_amount));
            out.push_str(&format!(
                "      \"detail\": \"{}\"\n",
                escape_json(&d.detail)
            ));
            if idx + 1 < rep.discrepancies.len() {
                out.push_str("    },\n");
            } else {
                out.push_str("    }\n");
            }
        }
        out.push_str("  ]\n");
    }
    out.push('}');
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::path::PathBuf;

    #[test]
    fn shared_fixture_parity_with_go_reference() {
        let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let input_path = manifest_dir.join("../fixtures/statement_batch_input.json");
        let expected_path = manifest_dir.join("../fixtures/statement_batch_expected.json");

        let input_raw = fs::read_to_string(&input_path).expect("read shared input fixture");
        let expected_raw =
            fs::read_to_string(&expected_path).expect("read shared expected fixture");

        let batch = parse_batch_input(&input_raw).expect("parse batch fixture");
        let report = reconcile_statement_batch(&batch);
        let got_json = format_report_json(&report);

        assert_eq!(got_json.trim(), expected_raw.trim());
    }
}
