# Local dev. Requires Go; `site` and `experiments` also need Rust + the wasm32
# target + trunk (and a C toolchain for build scripts).
.PHONY: run build vet site site-watch experiments docker

run: ## run the server on :8080 (admin login admin/dev)
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

vet:
	gofmt -l . && go vet ./...

site: ## build the wasm site into site/dist (restart `make run` to pick up a new version)
	cd site && trunk build --release

site-watch: ## rebuild the wasm site on change
	cd site && trunk watch

experiments: ## build each experiment for serving under /experiments/<slug>/
	cd experiments/particle-stream && trunk build --release --public-url /experiments/particle-stream/

docker:
	docker build -t personal-site .
