use std::env;
use std::fs;
use std::process;

use rewards_reconciler::{format_report_json, parse_batch_input, reconcile_statement_batch};

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: rewards-reconciler <statement-batch-input.json>");
        process::exit(2);
    }
    let raw = match fs::read_to_string(&args[1]) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("failed to read {}: {}", args[1], e);
            process::exit(1);
        }
    };
    let batch = match parse_batch_input(&raw) {
        Ok(b) => b,
        Err(e) => {
            eprintln!("{}", e);
            process::exit(1);
        }
    };
    let report = reconcile_statement_batch(&batch);
    println!("{}", format_report_json(&report));
}
