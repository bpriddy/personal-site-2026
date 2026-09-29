# Local dev. Requires Go; `experiments` also needs Rust + wasm32 target + trunk.
.PHONY: run build vet experiments docker

run: ## run the server on :8080 (admin login admin/dev)
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

vet:
	gofmt -l . && go vet ./...

experiments: ## build each experiment for serving under /experiments/<slug>/
	cd experiments/particle-stream && trunk build --release --public-url /experiments/particle-stream/

docker:
	docker build -t personal-site .
