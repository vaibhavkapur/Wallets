use std::env;
use std::process::ExitCode;

use optimizer_service::{contract_schema, router, AppState};

#[tokio::main]
async fn main() -> ExitCode {
    // `optimizer-engine schema` prints the JSON Schema of the engine contract.
    if env::args().nth(1).as_deref() == Some("schema") {
        println!(
            "{}",
            serde_json::to_string_pretty(&contract_schema()).expect("schema serializes")
        );
        return ExitCode::SUCCESS;
    }

    let token = env::var("ENGINE_AUTH_TOKEN").unwrap_or_default();
    let state = match AppState::new(&token) {
        Ok(state) => state,
        Err(message) => {
            eprintln!("optimizer-engine: {message}");
            return ExitCode::FAILURE;
        }
    };
    let addr = env::var("ENGINE_LISTEN_ADDR").unwrap_or_else(|_| "127.0.0.1:8081".to_owned());
    let listener = match tokio::net::TcpListener::bind(&addr).await {
        Ok(listener) => listener,
        Err(e) => {
            eprintln!("optimizer-engine: cannot listen on {addr}: {e}");
            return ExitCode::FAILURE;
        }
    };
    eprintln!("optimizer-engine: listening on {addr}");

    let shutdown = async {
        let _ = tokio::signal::ctrl_c().await;
    };
    match axum::serve(listener, router(state))
        .with_graceful_shutdown(shutdown)
        .await
    {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("optimizer-engine: {e}");
            ExitCode::FAILURE
        }
    }
}
