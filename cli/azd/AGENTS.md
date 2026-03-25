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
- **Shell-safe output**: When printing shell commands (e.g., `cd <path>`), quote or escape paths that may contain spaces. Use `fmt.Sprintf("cd %q", path)` or conditionally quote
- **Consistent JSON contracts**: When adding new fields to JSON output (e.g., `--output json`), ensure the field type and format are consistent with similar fields across commands. For example, don't mix `*time.Time` and `*RFC3339Time` for timestamp fields in the same API surface

### Path Safety

- **Never use user-derived path components without validation**: When deriving directory names from user input or template paths, always validate the result is not `.`, `..`, empty, or contains path separators. These can cause path traversal
- **Quote paths in user-facing messages**: File paths in error messages, suggestions, and follow-up hints should be quoted when they may contain spaces

### Code Organization

- **Import order**: stdlib → external → azure/azd internal → local
- **Complex packages**: Consider using `types.go` for shared type definitions (3+ types)
- **Context propagation**: Pass `ctx context.Context` as the first parameter to functions that do I/O or may need cancellation

### Error Handling

- Wrap errors with `fmt.Errorf("context: %w", err)` to preserve the error chain
- Consider using `internal.ErrorWithSuggestion` for straightforward, deterministic user-fixable issues
- When using `ErrorWithSuggestion`, always populate **all** relevant fields (`Err`, `Suggestion`, and `Links` if applicable). Don't leave `Message` or `Links` empty if the YAML pipeline rule would have provided them — `ErrorMiddleware` skips the YAML pipeline for errors that are already `ErrorWithSuggestion`
- Handle context cancellations appropriately
- **Telemetry service attribution**: Only set `error.service.name` (e.g., `"aad"`) when an external service actually returned the error. For locally-generated errors (e.g., "not logged in" state checks), don't attribute them to an external service
- **Scope-agnostic messages**: Error messages and suggestions in `error_suggestions.yaml` should be scope-agnostic when the error can occur at multiple scopes (subscription, resource group, etc.). Use "target scope" instead of hardcoding "resource group"

### Documentation Standards

- Public functions and types must have Go doc comments
- Comments should start with the function/type name
- Document non-obvious dependencies or assumptions
- **Help text consistency**: When changing command behavior, update **all** help text that references the old behavior — including `Short`, `Long`, command descriptions used by help generators, and snapshot files. Stale help text is a common review finding
- **Environment variable docs**: When documenting env vars, verify against the actual implementation — check the parsing method (`os.LookupEnv` presence-check vs `strconv.ParseBool`, `time.ParseDuration` vs integer seconds), default values, and which component reads them. Don't assume behavior from the name alone

### Architecture Boundaries

- **`pkg/project/` is target-agnostic**: The project manager and service manager should not contain service-target-specific logic (e.g., Container Apps, App Service). Service-target-specific behavior belongs in the target implementations under `pkg/project/` service target files or in the YAML error suggestions pipeline
- **Extension-specific concerns**: Keep extension-specific environment variables and configuration documented in the extension's own docs, not in core azd documentation, unless they are consumed by the core CLI

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

### Testing Best Practices

- **Test the actual rules, not just the framework**: When adding YAML-based error suggestion rules, write tests that exercise the YAML rules end-to-end through the pipeline, not just tests that validate the framework's generic matching behavior
- **Extract shared test helpers**: Don't duplicate test utilities across files. Extract common helpers (e.g., shell wrappers, auth token fetchers, CLI runners) into shared `test_utils` or `test/mocks` packages
- **Use correct env vars**: For forcing non-interactive mode in tests, use `AZD_FORCE_TTY=false` (not `AZD_DEBUG_FORCE_NO_TTY`). For no-prompt, use `AZD_NO_PROMPT=true`
- **TypeScript tests**: Use `catch (e: unknown)` with type assertions, not `catch (e: any)`. Set `NO_COLOR=1` in test env to prevent ANSI escape codes from breaking regex assertions
- **Reasonable timeouts**: Set Jest/test timeouts proportional to expected execution time. Don't use 5-minute timeouts for tests that should complete in seconds
- **Efficient directory checks**: To check if a directory is empty, use `os.Open` + `f.Readdirnames(1)` instead of `os.ReadDir` which reads the entire listing into memory

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

## CI / GitHub Actions Patterns

When creating or modifying GitHub Actions workflows:

- **Always declare `permissions:`** explicitly with least-privilege (e.g., `contents: read`)
- **Don't overwrite `PATH`** using `${{ env.PATH }}` — it's not defined in GitHub Actions expressions. Use `echo "$DIR" >> $GITHUB_PATH` instead
- **`actions/download-artifact@v4`** without `run-id` only downloads artifacts from the current run, not from other workflows. Cross-workflow artifact sharing requires `run-id` and `repository` parameters
- **Prefer Azure DevOps internal pipelines** for jobs that need secrets or Azure credentials — the team prefers `azdo` over GitHub Actions for authenticated workloads in this public repo
- **No placeholder steps**: Don't add workflow steps that echo "TODO" or are no-ops. If downstream steps depend on generated files, implement the generation or remove the dependency
