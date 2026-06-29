# Development Guide

This guide will help you set up your local development environment for contributing to OpenNSW Core.

## Prerequisites

Before you begin, ensure you have the following installed:

-   [Go](https://go.dev/dl/) (1.26 or later)
-   [Docker Daemon](https://docs.docker.com/get-docker/) and Docker Compose
-   [Make](https://www.gnu.org/software/make/)
-   [Git](https://git-scm.com/downloads)
-   [Gitleaks](https://github.com/gitleaks/gitleaks) (required for pre-commit secret scanning)

## Initial Setup

1.  **Fork and clone the repository:**
    ```bash
    git clone https://github.com/YOUR_USERNAME/core.git
    cd core
    ```

2.  **Add the upstream remote:**
    ```bash
    git remote add upstream https://github.com/OpenNSW/core.git
    ```

3.  **Run the setup script:**
    ```bash
    make setup
    ```
    
    This will:
    - Install Git hooks (pre-commit checks)
    - Install dev tools (`golangci-lint` and `addlicense`)

## Development Workflow

### Creating a Branch

Always create a new branch from `main` for your changes:

```bash
git checkout main
git pull upstream main
git checkout -b feature/your-feature-name
# or
git checkout -b fix/issue-number
```

### Running Tests

**Unit Tests:**
```bash
# Run all unit tests with the race detector
go test -race ./...

# Run tests for a specific package (e.g. workflow)
go test -race ./workflow/...
```

**Build Validation:**
```bash
# Build all packages
make build

# Build a specific package (e.g. workflow)
go build ./workflow/...
```

## Code Style and Standards

### Go Code Style

-   Follow standard Go idioms and conventions.
-   Run format checks using `make fmt` before committing.
-   Run linters and static analysis using `make lint`.
-   Verify license headers on Go files using `make license-check` (run `make license` to automatically add missing headers).

### Commit Messages

Write clear, descriptive commit messages:

-   Use the imperative mood ("Add feature" not "Added feature")
-   Keep the first line under 50 characters
-   Add a blank line and detailed explanation if needed
-   We follow [Conventional Commits](https://www.conventionalcommits.org/) (e.g., `feat(payment): add retry logic` or `fix(workflow): solve execution deadlock`).

### Code Review Checklist

Before submitting a pull request, ensure:

-   [ ] Code follows project style guidelines.
-   [ ] All tests pass locally.
-   [ ] New code includes appropriate tests.
-   [ ] Documentation is updated if needed.
-   [ ] Commit messages are clear and descriptive.
-   [ ] No merge conflicts with `main` branch.

## Project Structure

The `core` repository structure contains:

```
core/
├── artifact/              # Versioned configuration registry
├── artifactadapter/       # Bridge adapters to load domain types
├── authn/                 # JWT validation and identity context injection
├── authz/                 # Scope-based authorization middleware
├── cors/                  # CORS HTTP middleware
├── database/              # GORM/PostgreSQL connection factory
├── notification/          # Multi-channel notification router
├── pagination/            # Standard pagination querying
├── payment/               # Pluggable payment gateway orchestration
├── remote/                # Registry-based outbound HTTP client
├── storage/               # File storage abstraction (local & AWS S3)
├── taskflow/              # Micro-interactive human-in-the-loop task orchestration
├── temporal/              # Temporal client factory
├── uiprojector/           # Zone-based UI rendering
├── workflow/              # JSON DSL-driven Temporal workflow graph interpreter
└── docs/                  # Documentation
```

## Getting Help

-   Check existing [Issues](https://github.com/OpenNSW/core/issues)
-   See [Reporting Issues](reporting-issues.md) for how to submit bug reports and feature requests
-   Review the main [CONTRIBUTING.md](../../CONTRIBUTING.md)
