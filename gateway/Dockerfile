FROM rust:1-bookworm AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock* ./
COPY src ./src
RUN cargo build --release

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /src/target/release/asa-gateway /asa-gateway
USER nonroot
ENTRYPOINT ["/asa-gateway"]
