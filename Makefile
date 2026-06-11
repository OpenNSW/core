GOLANGCI_VERSION := v2.11.2
LICENSE_TMPL     := .github/license-header.tmpl
GO_FILES         := $(shell find . -type f -name '*.go' -not -path './vendor/*')

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: setup
setup: tools hooks ## Install dev tools and enable git hooks

.PHONY: tools
tools: ## Install golangci-lint and addlicense
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install github.com/google/addlicense@latest

.PHONY: hooks
hooks: ## Enable native git hooks (once per clone)
	git config core.hooksPath .githooks
	@echo "  ok  hooks enabled (.githooks/pre-commit + pre-push)"

.PHONY: fmt
fmt: ## Format code (gofmt + goimports via golangci-lint)
	golangci-lint fmt -c .golangci.yml ./...

.PHONY: lint
lint: ## Run linters
	golangci-lint run -c .golangci.yml --timeout=5m ./...

.PHONY: license
license: ## Add missing SPDX license headers to all Go files
	addlicense -f $(LICENSE_TMPL) $(GO_FILES)

.PHONY: license-check
license-check: ## Verify SPDX headers exist on all Go files (as CI does)
	addlicense -check -f $(LICENSE_TMPL) $(GO_FILES)

.PHONY: tidy
tidy: ## Verify go.mod/go.sum are tidy
	go mod tidy
	git diff --exit-code go.mod go.sum

.PHONY: build
build: ## Build all packages
	go build ./...

.PHONY: test
test: ## Run tests with the race detector (as CI does)
	go test -race ./...

.PHONY: vuln
vuln: ## Scan dependencies for known vulnerabilities (govulncheck)
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: secrets
secrets: ## Scan working tree for secrets (gitleaks)
	gitleaks dir . --redact --no-banner

.PHONY: check
check: secrets license-check lint tidy build ## Run all pre-commit checks (no tests; use 'make test' separately)
