# Azure Developer CLI (azd) - Agent Instructions

<!-- cspell:ignore Errorf Chdir azapi gofmt golangci stdlib strconv Readdirnames -->

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

> **Tip**: The `/azd-preflight` Copilot skill runs all these checks and auto-fixes issues. See `.github/skills/azd-preflight/`.

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
- **Shell-safe output**: When emitting shell commands in user-facing messages (e.g., `cd <path>`), quote paths that may contain spaces. Use `fmt.Sprintf("cd %q", path)` or conditionally wrap in quotes
- **Consistent JSON types**: When adding fields to JSON output (`--output json`), match the types used by similar fields across commands. Don't mix `*time.Time` and custom timestamp wrappers (e.g., `*RFC3339Time`) in the same API surface

### CLI UX & Style

When working on CLI output, terminal UX, spinners, progress states, colors, or prompts for **core azd flows**, you **MUST read** the style guide before making any changes or recommendations:

📄 **`cli/azd/docs/style-guidelines/azd-style-guide.md`** (full path from repo root)

This file is the authoritative reference for core azd terminal UX patterns including:
- Progress report states (`(✓) Done`, `(x) Failed`, `(!) Warning`, `(-) Skipped`)
- Spinner type (bar-fill `|=======|`)
- Color conventions (`WithSuccessFormat`, `WithErrorFormat`, `WithHighLightFormat`, etc.)
- User input patterns (text input, list select, yes/no confirm)
- Prompt styling (`?` marker in bold blue, `[Type ? for hint]`, post-submit states)

> **Note**: This style guide covers **core azd flows only**. Separate guidelines for agentic flows and extension-specific UX will be provided in dedicated files in the future. Do not apply core azd patterns to agentic or extension flows without a dedicated style reference.

### Code Organization

- **Import order**: stdlib → external → azure/azd internal → local
- **Complex packages**: Consider using `types.go` for shared type definitions (3+ types)
- **Context propagation**: Pass `ctx context.Context` as the first parameter to functions that do I/O or may need cancellation
- **Don't duplicate logic across scopes**: When similar logic exists for multiple deployment scopes (e.g., resource group + subscription), extract shared helpers (e.g., `filterActiveDeployments()`) instead of copying code between scope implementations

### Error Handling

- Wrap errors with `fmt.Errorf("context: %w", err)` to preserve the error chain
- Consider using `internal.ErrorWithSuggestion` for straightforward, deterministic user-fixable issues
- Handle context cancellations appropriately
- **`ErrorWithSuggestion` completeness**: When returning `ErrorWithSuggestion`, populate **all** relevant fields (`Err`, `Suggestion`, `Message`, `Links`). `ErrorMiddleware` skips the YAML error-suggestion pipeline for errors already typed as `ErrorWithSuggestion`, so leaving fields empty means the user misses guidance that the YAML rule would have provided
- **Telemetry service attribution**: Only set `error.service.name` (e.g., `"aad"`) when an external service actually returned the error. For locally-generated errors (e.g., "not logged in" state checks), don't attribute to an external service — use a local classification instead
- **Scope-agnostic error messages**: Error messages and suggestions in `error_suggestions.yaml` should work across all deployment scopes (subscription, resource group, etc.). Use "target scope" or "deployment scope" instead of hardcoding "resource group"
- **Match links to suggestion text**: If a suggestion mentions multiple tools (e.g., "Docker or Podman"), the `links:` list must include URLs for all of them. Don't mention options you don't link to
- **Stale data in polling loops**: When polling for state changes (e.g., waiting for active deployments), refresh display data (names, counts) from each poll result rather than capturing it once at the start

### Architecture Boundaries

- **`ProjectManager` is target-agnostic**: `project_manager.go` should not contain service-target-specific logic (e.g., Container Apps details, Docker checks). Target-specific behavior belongs in the target implementations (e.g., `service_target_containerapp.go`) or in the `error_suggestions.yaml` pipeline. The project manager is an interface for service management and should not make assumptions about which target is running
- **Extension-specific documentation**: Keep extension-specific environment variables and configuration documented in the extension's own docs, not in core azd reference docs, unless they are consumed by the core CLI itself
- **Verify env vars against source**: When documenting environment variables, verify the actual parsing method in code — `os.LookupEnv` (presence-only) vs `strconv.ParseBool` (true/false) vs `time.ParseDuration` vs integer seconds. Document the expected format and default value accurately

### Path Safety

- **Validate derived paths**: When deriving directory names from user input or template paths, always validate the result is not `.`, `..`, empty, or contains path separators. These can cause path traversal outside the working directory
- **Quote paths in user-facing output**: Shell commands in suggestions, follow-up messages, and error hints should quote file paths that may contain spaces

### Documentation Standards

- Public functions and types must have Go doc comments
- Comments should start with the function/type name
- Document non-obvious dependencies or assumptions
- **Help text consistency**: When changing command behavior, update **all** related help text — `Short`, `Long`, custom description functions used by help generators, and usage snapshot files. Stale help text that contradicts the actual behavior is a common review finding
- **No dead references**: Don't reference files, scripts, directories, or workflows that don't exist in the PR. If a README lists `scripts/generate-report.ts`, it must exist. If a CI table lists `eval-human.yml`, it must be included
- **PR description accuracy**: Keep the PR description in sync with the actual implementation. If the description says "server-side filtering" but the code does client-side filtering, update the description

#### Environment Variables Documentation

The file `cli/azd/docs/environment-variables.md` is the single source of truth for every environment
variable `azd` reads. When adding or modifying an `os.Getenv` / `os.LookupEnv` call:

1. Add the variable to the appropriate section in `environment-variables.md`.
2. Include a one-line description that explains what it controls and its default if non-obvious.
3. Place debug/internal variables under **Debug Variables** with the unsupported warning.

### Modern Go

This project uses Go 1.26. Use modern standard library features:

- **`slices`, `maps`, `cmp` packages**: Use for searching, sorting, cloning, and iterating—avoid manual loops
- **Iterators**: Use `range` over functions/iterators (e.g., `maps.Keys()`, `slices.All()`)
- **Built-ins**: Use `min()`, `max()`, `clear()` directly
- **Range over integers**: `for i := range 10 { }`

### Modern Go Patterns (Go 1.26+)
- Use `new(val)` not `x := val; &x` - returns pointer to any value.  
    Go 1.26 extends `new()` to accept expressions, not just types.  
    Type is inferred: `new(0) → *int`, `new("s") → *string`, `new(T{}) → *T`.  
    DO NOT use `x := val; &x` pattern — always use `new(val)` directly.  
    DO NOT use redundant casts like `new(int(0))` — just write `new(0)`.  
    Before:
    ```go
    timeout := 30
    debug := true
    cfg := Config{
        Timeout: &timeout,
        Debug:   &debug,
    }
    ```

    After:
    ```go
    cfg := Config{
        Timeout: new(30),   // *int
        Debug:   new(true), // *bool
    }
    ```

- Use `errors.AsType[T](err)` not `errors.As(err, &target)`.
    ALWAYS use errors.AsType when checking if error matches a specific type.

    Before:
    ```go
    var pathErr *os.PathError
    if errors.As(err, &pathErr) {
        handle(pathErr)
    }
    ```

    After:
    ```go
    if pathErr, ok := errors.AsType[*os.PathError](err); ok {
        handle(pathErr)
    }
    ```

- Use `slices.SortFunc(items, func(a, b T) int { return cmp.Compare(a.Name, b.Name) })` instead of `sort.Slice`
- Use `slices.Clone(s)` instead of `append([]T{}, s...)`
- Use `slices.Sorted(maps.Keys(m))` instead of collecting keys and sorting them separately
- Use `http.NewRequestWithContext(ctx, method, url, body)` instead of `http.NewRequest(...)`
- Use `wg.Go(func() { ... })` instead of `wg.Add(1); go func() { defer wg.Done(); ... }()`
- Use `for i := range n` instead of `for i := 0; i < n; i++` for simple counted loops
- Use `t.Context()` instead of `context.Background()` in tests
- Use `t.Chdir(dir)` instead of `os.Chdir` plus a deferred restore in tests
- Run `go fix ./...` before committing; CI enforces these modernizations

### Testing Best Practices

- **Test the actual rules, not just the framework**: When adding YAML-based error suggestion rules, write tests that exercise the rules end-to-end through the pipeline, not just tests that validate the framework's generic matching behavior
- **Extract shared test helpers**: Don't duplicate test utilities across files. Extract common helpers (e.g., shell wrappers, auth token fetchers, CLI runners) into shared `test_utils` packages. Duplication across 3+ files should always be refactored
- **Use correct env vars for testing**:
  - Non-interactive mode: `AZD_FORCE_TTY=false` (not `AZD_DEBUG_FORCE_NO_TTY`, which doesn't exist)
  - No-prompt mode: use the `--no-prompt` flag for core azd commands; `AZD_NO_PROMPT=true` is only used for propagating no-prompt into extension subprocesses
  - Suppress color: `NO_COLOR=1` — always set in test environments to prevent ANSI escape codes from breaking assertions
- **TypeScript test patterns**: Use `catch (e: unknown)` with type assertions, not `catch (e: any)` which bypasses strict mode
- **Reasonable timeouts**: Set test timeouts proportional to expected execution time. Don't use 5-minute timeouts for tests that shell out to `azd --help` (which completes in seconds)
- **Efficient directory checks**: To check if a directory is empty, use `os.Open` + `f.Readdirnames(1)` instead of `os.ReadDir` which reads the entire listing into memory
- **Cross-platform paths**: When resolving binary paths in tests, handle `.exe` suffix on Windows (e.g., `azd` vs `azd.exe` via `process.platform === "win32"`)
- **Test new JSON fields**: When adding fields to JSON command output (e.g., `expiresOn` in `azd auth status --output json`), add a test asserting the field's presence and format
- **No unused dependencies**: Don't add npm/pip packages that aren't imported anywhere. Remove dead `devDependencies` before submitting

## MCP Tools

Tools follow `server.ServerTool` interface from `github.com/mark3labs/mcp-go/server`:
- Constructor: `NewXXXTool() server.ServerTool`
- Handler: `handleXXX(ctx, request) (*mcp.CallToolResult, error)`
- Snake_case names (e.g., `azd_plan_init`)

### Go Module Versioning

Go module version tags (`cli/azd/vX.Y.Z`) are created alongside each CLI release tag (`azure-dev-cli_X.Y.Z`). Extension developers should use semver references in `go.mod` instead of pseudo-versions. See `docs/sdk-versioning.md` for details.

When cutting a release, `eng/scripts/Update-CliVersion.ps1` automatically updates both `cli/version.txt` and `pkg/azdext/version.go` to keep versions in sync.

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

- `docs/style-guidelines/azd-style-guide.md` - CLI style guide (colors, spinners, progress states, terminal UX)
- `docs/style-guidelines/new-azd-command.md` - Adding new commands
- `docs/extensions/extension-framework.md` - Extension development using gRPC extension framework
- `docs/style-guidelines/guiding-principles.md` - Design principles
- `docs/tracing-in-azd.md` - Tracing/telemetry guidelines
- `docs/sdk-versioning.md` - Go module versioning for extension developers

## CI / GitHub Actions

When creating or modifying GitHub Actions workflows:

- **Always declare `permissions:`** explicitly with least-privilege (e.g., `contents: read`). All workflows in the repo should have this block for consistency
- **Don't overwrite `PATH`** using `${{ env.PATH }}` — it's not defined in GitHub Actions expressions and will wipe the real PATH. Use `echo "$DIR" >> $GITHUB_PATH` instead
- **Cross-workflow artifacts**: `actions/download-artifact@v4` without `run-id` only downloads artifacts from the *current* workflow run. Cross-workflow artifact sharing requires `run-id` and `repository` parameters
- **Prefer Azure DevOps pipelines** for jobs that need secrets or Azure credentials — the team uses internal ADO pipelines for authenticated workloads in this public repo
- **No placeholder steps**: Don't add workflow steps that echo "TODO" or list directories without producing output. If downstream steps depend on generated files, implement the generation or remove the dependency
