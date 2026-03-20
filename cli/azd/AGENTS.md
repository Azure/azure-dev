# Azure Developer CLI (azd) - Agent Instructions

<!-- cspell:ignore Errorf Chdir azapi gofmt golangci stdlib -->

Instructions for AI coding agents working with the Azure Developer CLI.

## Overview

Azure Developer CLI (azd) is a Go-based CLI for Azure application development and deployment. It handles infrastructure provisioning with Bicep/Terraform, app deployment, environment management, project and service lifecycle hooks, and features a gRPC-based extension framework.

## Directory Structure

```
cli/azd/
├── main.go              # Entry point
├── cmd/                 # Commands (ActionDescriptor pattern)
│   ├── root.go          # Command tree registration
│   ├── container.go     # IoC service registration
│   ├── actions/         # Action framework
│   └── middleware/      # Cross-cutting concerns (telemetry, hooks, extensions)
├── pkg/                 # Reusable public packages
│   ├── ioc/             # Dependency injection container
│   ├── project/         # Project configuration (azure.yaml), service targets, framework services
│   └── infra/           # Infrastructure providers (Bicep, Terraform)
│   ├── azapi/           # Azure APIs
│   └── tools/           # External tools
├── internal/            # Internal packages (telemetry, tracing)
├── test/                # Test utilities
├── extensions/          # First-party extensions
└── docs/                # Documentation
```

**Entry points**: `main.go` → `cmd/root.go` (command tree) → `cmd/container.go` (IoC registration)

**Tip**: Service registration in `cmd/container.go` shows all major components. To find where a feature is implemented, start with the command in `cmd/`, follow to the action, then trace service dependencies.


## Development

Commands assume you are in `cli/azd`.

### Build

```bash
go build
```

### Test

**Note**: In CI environments like inside a GitHub coding agent session, run `go build` first as the automatic build is skipped and the azd binary must exist for tests that spawn the CLI process. This applies to snapshot tests and functional tests in `test/functional/`.

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

Test file patterns:
- Unit tests: `*_test.go` alongside source files
- Functional tests: `test/functional/`
- Shared mocks: `test/mocks/`

When writing tests, prefer table-driven tests. Use testify/mock for mocking.

### Pre-Commit Checklist

```bash
gofmt -s -w .
go fix ./...
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
../../eng/scripts/copyright-check.sh . --fix
```

- **Line length**: 125 chars max for Go (enforced by `lll` linter); no limit for Markdown
- **Spelling**: Add technical terms to `cli/azd/.vscode/cspell.yaml` overrides
  - Use file-scoped `overrides` entries (not the global `words` list) for terms specific to one file
- **Copyright**: All Go files need the Microsoft header (handled by copyright-check.sh)
- **Code modernization**: `go fix ./...` applies automatic modernizations (e.g. `interface{}` → `any`,
  loop simplifications, use of `slices`/`maps` packages). CI enforces this check.

### Linting Details

The project uses `golangci-lint` with these key linters enabled (see `.golangci.yaml`):
- **`lll`** — max line length 125 characters (tab width 4). Break long lines with continuation.
- **`gofmt`** — standard Go formatting
- **`gosec`** — security checks
- **`errorlint`** — correct `errors.Is`/`errors.As`/`errors.AsType` usage
- **`unused`** — detect unused code
- **`staticcheck`** — comprehensive static analysis

Common line-length issues and fixes:
```go
// BAD: 135 chars — too long for lll
if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok && respErr.StatusCode == 404 { // already deleted

// GOOD: break the condition across lines
// Resource group is already deleted
if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
    respErr.StatusCode == 404 {
```

### Avoiding Unrelated go.mod/go.sum Changes

When running tools like CodeQL or `go mod tidy`, `go.mod` and `go.sum` files may be modified across multiple Go modules. **Only commit `go.mod`/`go.sum` changes that are relevant to the task.**

- **azd core changes** (`cli/azd/` excluding `extensions/`): Only commit `cli/azd/go.mod` and `cli/azd/go.sum`. Do NOT commit any `go.mod`/`go.sum` files in `cli/azd/extensions/`.
- **Extension changes** (`cli/azd/extensions/<extension-name>/`): Only commit `go.mod`/`go.sum` for the specific extension being modified.

If unrelated `go.mod`/`go.sum` files are staged, unstage them before committing.

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

### Output Formatting

- Commands can support multiple output formats via `--output` flag like `json` and`table`
- Use structured output for machine consumption

### Code Organization

- **Import order**: stdlib → external → azure/azd internal → local
- **Complex packages**: Consider using `types.go` for shared type definitions (3+ types)
- **Context propagation**: Pass `ctx context.Context` as the first parameter to functions that do I/O or may need cancellation

### Error Handling

- Wrap errors with `fmt.Errorf("context: %w", err)` to preserve the error chain
- Consider using `internal.ErrorWithSuggestion` for straightforward, deterministic user-fixable issues
- Handle context cancellations appropriately

### Documentation Standards

- Public functions and types must have Go doc comments
- Comments should start with the function/type name
- Document non-obvious dependencies or assumptions

### Modern Go

This project uses Go 1.26. Use modern standard library features:

- **`slices`, `maps`, `cmp` packages**: Use for searching, sorting, cloning, and iterating—avoid manual loops
- **Iterators**: Use `range` over functions/iterators (e.g., `maps.Keys()`, `slices.All()`)
- **Built-ins**: Use `min()`, `max()`, `clear()` directly
- **Range over integers**: `for i := range 10 { }`

### Modern Go Patterns (Go 1.26+)

- Use `errors.AsType[*MyError](err)` instead of `var e *MyError; errors.As(err, &e)`
- Use `slices.SortFunc(items, func(a, b T) int { return cmp.Compare(a.Name, b.Name) })` instead of `sort.Slice`
- Use `slices.Clone(s)` instead of `append([]T{}, s...)`
- Use `slices.Sorted(maps.Keys(m))` instead of collecting keys and sorting them separately
- Use `http.NewRequestWithContext(ctx, method, url, body)` instead of `http.NewRequest(...)`
- Use `new(expr)` instead of `to.Ptr(expr)`; `go fix ./...` applies this automatically
- Use `wg.Go(func() { ... })` instead of `wg.Add(1); go func() { defer wg.Done(); ... }()`
- Use `for i := range n` instead of `for i := 0; i < n; i++` for simple counted loops
- Use `t.Context()` instead of `context.Background()` in tests
- Use `t.Chdir(dir)` instead of `os.Chdir` plus a deferred restore in tests
- Run `go fix ./...` before committing; CI enforces these modernizations

## MCP Tools

Tools follow `server.ServerTool` interface from `github.com/mark3labs/mcp-go/server`:
- Constructor: `NewXXXTool() server.ServerTool`
- Handler: `handleXXX(ctx, request) (*mcp.CallToolResult, error)`
- Snake_case names (e.g., `azd_plan_init`)

## Extensions

First-party azd extensions live in `cli/azd/extensions/`.

To build:

```bash
cd cli/azd/extensions/<extension-name>

# Build using developer extension (for local development)
azd x build

# Or build using Go directly
go build
```

## Documentation

Feature-specific docs are in `docs/` — refer to them as needed. Some key docs include:

- `docs/style-guidelines/new-azd-command.md` - Adding new commands
- `docs/extensions/extension-framework.md` - Extension development using gRPC extension framework
- `docs/style-guidelines/guiding-principles.md` - Design principles
- `docs/tracing-in-azd.md` - Tracing/telemetry guidelines
