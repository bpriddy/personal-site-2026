# Cloud Run image: Go server + prebuilt experiments.
# Build from the repo root: docker build -t personal-site .

# ── experiments (Rust → wasm via trunk) ──────────────────────────────────────
FROM rust:1-slim AS experiments
ARG TRUNK_VERSION=v0.21.14
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && rustup target add wasm32-unknown-unknown \
 && curl -fsSL "https://github.com/trunk-rs/trunk/releases/download/${TRUNK_VERSION}/trunk-x86_64-unknown-linux-musl.tar.gz" \
    | tar -xz -C /usr/local/bin
WORKDIR /src/experiments/particle-stream
COPY experiments/particle-stream/ .
RUN trunk build --release --public-url /experiments/particle-stream/

# ── server ───────────────────────────────────────────────────────────────────
FROM golang:1.27 AS server
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ── runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=server /out/server /app/server
COPY --from=experiments /src/experiments/particle-stream/dist /app/experiments/particle-stream/dist
ENV APP_ENV=prod EXPERIMENTS_DIR=/app/experiments
EXPOSE 8080
ENTRYPOINT ["/app/server"]
