# Azure Developer CLI (azd) - Agent Instructions

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
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
../../eng/scripts/copyright-check.sh . --fix
```

- **Line length**: 125 chars max for Go (enforced by `lll` linter); no limit for Markdown
- **Spelling**: Add technical terms to `cli/azd/.vscode/cspell.yaml` overrides
- **Copyright**: All Go files need the Microsoft header (handled by copyright-check.sh)

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

#### Environment Variables Documentation

When adding or modifying environment variable usage in azd:

1. **Update the documentation**: Add or update the variable in `docs/environment-variables.md`
2. **Choose the appropriate section**: Place the variable in the correct category:
   - **Core Azure Variables** - `AZURE_*` variables for Azure resources
   - **Dev Center Variables** - `AZURE_DEVCENTER_*` variables
   - **General Configuration** - User-facing configuration like `AZD_CONFIG_DIR`, `AZD_DEMO_MODE`
   - **Alpha Features** - `AZD_ALPHA_ENABLE_*` feature flags
   - **External Authentication** - `AZD_AUTH_*` variables
   - **Tool Configuration** - Tool path overrides and `AZD_BUILDER_IMAGE`
   - **Extension Variables** - `AZD_EXT_*` and extension-related variables
   - **Telemetry & Tracing** - `AZURE_DEV_COLLECT_TELEMETRY`, `TRACEPARENT`, etc.
   - **CI/CD Variables** - Pipeline-specific variables
   - **Terraform Provider Variables** - `ARM_*` variables
   - **Console & Terminal** - `NO_COLOR`, `TERM`, `BROWSER`, etc.
   - **Debug Variables** - `AZD_DEBUG_*` variables (mark as internal use)
   - **Test Variables** - `AZD_TEST_*` variables (mark as test-only)
3. **Include a description**: Clearly explain what the variable does and when to use it
4. **Note the variable type**: Mention if it's boolean, path, URL, duration, etc.
5. **Mark internal variables**: Clearly indicate variables intended for debug/test purposes with a warning

**Example**:
```markdown
- `AZD_CONFIG_DIR`: The file path of the user-level configuration directory. Overrides the default configuration location.
```

### Modern Go

This project uses Go 1.25. Use modern standard library features:

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
