# Azure Developer CLI (azd) - Agent Instructions

Instructions for AI coding agents working with the Azure Developer CLI.

## Overview

Azure Developer CLI (azd) is a Go-based CLI for Azure application development and deployment. It supports provisioning infrastructure (Bicep/Terraform), deploying apps, and managing environments. The project follows Microsoft coding standards and uses a layered architecture with dependency injection, structured command patterns, and comprehensive testing.

## Development

### Build

```bash
cd cli/azd
go build
```

### Test

```bash
# Specific test
go test ./pkg/project/... -run TestProjectConfig

# Update command snapshots (whenever command help text changes or new commands are added)
UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'

# Unit tests only (can take up to 10 min)
go test ./... -short

# Full suite including E2E (can take 10+ min)
go test ./...

```

When writing tests, prefer table-driven tests. Use testify/mock for mocking (see `cli/azd/test/mocks/`).

## Pre-Commit Checklist

Run from `cli/azd` before committing:

```bash
gofmt -s -w .
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
../../eng/scripts/copyright-check.sh . --fix
```

- **Line length**: 125 chars max for Go (enforced by `lll` linter); no limit for Markdown
- **Spelling**: Add technical terms to `cli/azd/.vscode/cspell.yaml` overrides
- **Copyright**: All Go files need the Microsoft header (handled by copyright-check.sh)

## Directory Structure

```
cli/azd/
├── main.go              # Entry point
├── cmd/                 # Commands (ActionDescriptor pattern)
│   ├── root.go          # Command tree registration
│   ├── actions/         # Action framework
│   └── middleware/      # Cross-cutting concerns (telemetry, hooks, extensions)
├── pkg/                 # Reusable public packages
│   ├── ioc/             # Dependency injection container
│   ├── project/         # Project configuration (azure.yaml)
│   └── infra/           # Infrastructure providers (Bicep, Terraform)
├── internal/            # Internal packages (telemetry, tracing)
├── test/                # Test utilities, mocks
├── extensions/          # First-party extensions
└── docs/                # Documentation
```

## Key Patterns

### IoC Container (Dependency Injection)

Always use IoC for service registration—never instantiate services directly (see `cli/azd/cmd/container.go`):

```go
ioc.RegisterSingleton(container, func() *MyService {
    return &MyService{dep: ioc.Get[*Dependency](container)}
})
```

### Action-Based Commands

Commands implement the `actions.Action` interface, not traditional Cobra handlers:

```go
type myAction struct {
    svc *SomeService
}

func newMyAction(svc *SomeService) actions.Action {
    return &myAction{svc: svc}
}

func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    return &actions.ActionResult{
        Message: &actions.ResultMessage{Header: "Success"},
    }, nil
}
```

Commands should support `--output` flag for output formats: `json`, `table`.

### Code Organization

- **Import order**: stdlib → external → azure/azd internal → local
- **`internal/` vs `pkg/`**: `internal/` for implementation details, `pkg/` for reusable packages
- **Complex packages**: Use `types.go` for shared type definitions (3+ types)
- **Interface implementations**: Group methods with comment `// For XXX interface`

### Error Handling

- **Wrap errors** with `fmt.Errorf("context: %w", err)` to preserve the error chain
- **User-facing errors**: Use `internal.ErrorWithSuggestion` for errors with actionable solutions

## Modern Go

This project uses Go 1.25. Prefer modern standard library features:

- **`slices`, `maps`, `cmp` packages**: Use for searching, sorting, cloning, and iterating—avoid manual loops
- **Iterators**: Use `range` over functions/iterators (e.g., `maps.Keys()`, `slices.All()`)
- **Built-ins**: Use `min()`, `max()`, `clear()` directly
- **Range over integers**: `for i := range 10 { }`

## MCP Tools

Tools follow `server.ServerTool` interface from `github.com/mark3labs/mcp-go/server`:
- Constructor: `NewXXXTool() server.ServerTool`
- Handler: `handleXXX(ctx, request) (*mcp.CallToolResult, error)`
- Snake_case names (e.g., `azd_plan_init`)

## Extensions

Extensions live in `cli/azd/extensions/`. To build:

```bash
cd extensions/<extension-name>

# Build using developer extension (for local development)
azd x build

# Or build using Go directly
go build
```

## Documentation

Feature-specific docs are in `docs/` — refer to them as needed. Some key docs include:

- `docs/new-azd-command.md` - Adding new commands
- `docs/extension-framework.md` - Extension development using azd's gRPC-based extension framework
- `docs/guiding-principles.md` - Design principles
- `docs/tracing-in-azd.md` - Tracing/telemetry guidelines
