STATICCHECK ?= honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK ?= golang.org/x/vuln/cmd/govulncheck@v1.9.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build install test fmt lint vuln check

build: ## Build ./lazymobile
	go build -ldflags "-X main.version=$(VERSION)" -o lazymobile ./cmd/lazymobile

install: ## Install into GOBIN
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/lazymobile

test: ## Unit tests with the race detector. They use fake tools and touch no real device
	go test -race ./...

fmt: ## Format the code
	gofmt -w cmd internal

lint: ## Formatting, vet and staticcheck, as CI runs them
	@test -z "$$(gofmt -l cmd internal)" || { echo "Not formatted (run make fmt):"; gofmt -l cmd internal; exit 1; }
	go vet ./...
	go run $(STATICCHECK) ./...

vuln: ## Known vulnerabilities in the code and the dependencies it calls. Needs the network
	go run $(GOVULNCHECK) ./...

check: lint test ## Everything CI runs
