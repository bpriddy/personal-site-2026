# Local dev. Requires Go; `frontends` also needs Rust + the wasm32 target + trunk
# and a C toolchain (scripts/dev-setup.sh installs all of it without root).
.PHONY: dev run run-usercontent build vet test frontends e2e docker

export PATH := $(HOME)/.local/bin:$(HOME)/.cargo/bin:$(HOME)/.local/go/bin:$(PATH)

dev: ## main site on :8080 + user-content service on :8081 (admin login admin/dev)
	@trap 'kill 0' INT TERM EXIT; \
	PORT=8081 go run ./cmd/usercontent & \
	PORT=8080 go run ./cmd/server & \
	wait

run: ## main site only, :8080
	PORT=8080 go run ./cmd/server

run-usercontent: ## user-content service only, :8081
	PORT=8081 go run ./cmd/usercontent

build:
	go build -o bin/server ./cmd/server
	go build -o bin/usercontent ./cmd/usercontent

vet:
	test -z "$$(gofmt -l .)" && go vet ./...

test: vet
	go test ./...

frontends: ## build the built-in front ends into build/frontends/builtin/<name>/
	scripts/build-frontends.sh

e2e: ## headless-browser end-to-end tests (builds everything first)
	e2e/run.sh

docker: ## both images: personal-site (main) and personal-site-usercontent
	docker build --target server -t personal-site .
	docker build --target usercontent -t personal-site-usercontent .
