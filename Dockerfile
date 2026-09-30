# Two Cloud Run images from one Dockerfile (build from the repo root):
#   docker build --target server      -t personal-site .
#   docker build --target usercontent -t personal-site-usercontent .
# The main site holds no front-end code; the user-content service holds the
# built-in front ends (until they move to Cloud Storage).

# ── built-in front ends (Rust → wasm via trunk) ──────────────────────────────
FROM rust:1-slim AS frontends
ARG TRUNK_VERSION=v0.21.14
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && rustup target add wasm32-unknown-unknown \
 && curl -fsSL "https://github.com/trunk-rs/trunk/releases/download/${TRUNK_VERSION}/trunk-x86_64-unknown-linux-musl.tar.gz" \
    | tar -xz -C /usr/local/bin
WORKDIR /src
COPY scripts/build-frontends.sh scripts/
COPY site/ site/
COPY experiments/ experiments/
RUN bash scripts/build-frontends.sh

# ── Go binaries ──────────────────────────────────────────────────────────────
FROM golang:1.27 AS gobuild
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/usercontent ./cmd/usercontent

# ── main site ────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=gobuild /out/server /app/server
ENV APP_ENV=prod
EXPOSE 8080
ENTRYPOINT ["/app/server"]

# ── user-content service ─────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS usercontent
COPY --from=gobuild /out/usercontent /app/usercontent
COPY --from=frontends /src/build/frontends /app/frontends
ENV APP_ENV=prod FRONTENDS_DIR=/app/frontends
EXPOSE 8080
ENTRYPOINT ["/app/usercontent"]
