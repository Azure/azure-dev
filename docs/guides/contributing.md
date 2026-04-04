# Contributing to Azure Developer CLI

This guide covers how to build, test, lint, and submit changes to the Azure Developer CLI.

## Prerequisites

- Go 1.26 or later
- [golangci-lint](https://golangci-lint.run/)
- [cspell](https://cspell.org/) (for spell checking)

## Build

From `cli/azd/`:

```bash
go build
```

## Test

```bash
# Run a specific test
go test ./pkg/project/... -run TestProjectConfig

# Run unit tests only (may take up to 10 minutes)
go test ./... -short

# Run full suite including end-to-end tests
go test ./...
```

> **Note:** In CI environments, run `go build` first — the automatic build is skipped and the azd binary must exist for tests that spawn the CLI process.

### Snapshot Tests

When command help text changes or new commands are added, update snapshots:

```bash
UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'
```

## Pre-Commit Checklist

Run all checks from `cli/azd/`:

```bash
gofmt -s -w .
go fix ./...
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
../../eng/scripts/copyright-check.sh . --fix
```

### What each check does

| Check | Purpose |
|---|---|
| `gofmt -s -w .` | Formats Go code to standard style |
| `go fix ./...` | Applies automatic modernizations (e.g., `interface{}` → `any`) |
| `golangci-lint run ./...` | Runs linters including line length, security, and static analysis |
| `cspell lint` | Checks spelling; add new terms to `.vscode/cspell.yaml` overrides |
| `copyright-check.sh` | Ensures all Go files have the Microsoft copyright header |

## Key Lint Rules

- **Line length:** 125 characters max (enforced by `lll` linter)
- **Formatting:** Standard Go formatting via `gofmt`
- **Security:** `gosec` checks for common security issues
- **Error handling:** `errorlint` enforces correct `errors.Is`/`errors.As`/`errors.AsType` usage
- **Unused code:** `unused` detects dead code
- **Static analysis:** `staticcheck` provides comprehensive checks

## Code Style

- **Import order:** stdlib → external → azure/azd internal → local
- **Error handling:** Wrap errors with `fmt.Errorf("context: %w", err)`
- **Context propagation:** Pass `ctx context.Context` as the first parameter to I/O functions
- **Modern Go:** Use Go 1.26+ features — see [AGENTS.md](../../AGENTS.md) for the full list
- **Testing:** Prefer table-driven tests; use testify/mock for mocking

For comprehensive style guidance, see:

- [azd Style Guide](../../cli/azd/docs/style-guidelines/azd-style-guide.md)
- [Guiding Principles](../../cli/azd/docs/style-guidelines/guiding-principles.md)

## Submitting Changes

1. Fork the repository and create a feature branch
2. Make your changes following the code style guidelines
3. Run the pre-commit checklist
4. Run relevant tests
5. Open a pull request with a clear description of your changes

Most contributions require a [Contributor License Agreement (CLA)](https://cla.microsoft.com). A bot will guide you through this when you open a PR.
