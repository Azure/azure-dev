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

Additional mage targets:

- `mage record` — re-record functional test cassettes against a live Azure subscription. Accepts an optional `-filter=TestName` flag to re-record specific tests. Typically only core maintainers need to run this; external contributors can rely on playback mode (the default) which requires no Azure access. Requires `azd auth login` and a configured test subscription (see `docs/recording-functional-tests-guide.md`).
- `mage coverage:pr` — preview the CI PR coverage gate locally before pushing. Resolves PR-touched `.go` files via `git merge-base origin/main HEAD` for the per-package summary, runs the diff against the latest `main` baseline, and fails (exit 2) on **either** breach type: any PR-touched package drops more than 0.5 pp, or overall coverage falls below 69% (defaults match CI; override via `COVERAGE_MAX_PACKAGE_DECREASE`, `COVERAGE_MIN_OVERALL`). See `docs/code-coverage-guide.md` for details.
- `mage updateGoVersion <version>` — bump the pinned Go toolchain version everywhere it is referenced (every `cli/azd` `go.mod`, the ADO `setup-go` template, Dockerfiles, and the devcontainer Go feature). `cli/azd/go.mod` is the source of truth enforced by the `validate-go-version` workflow. This is the single source of truth for the sync logic. Example: `mage updateGoVersion 1.26.4`.

```bash
gofmt -s -w .
go fix ./...
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
../../eng/scripts/copyright-check.sh . --fix

# From repo root — spell check docs/misc files (mirrors CI cspell-misc.yml)
cd ../..
cspell lint "**/*" --relative --config ./.vscode/cspell.misc.yaml --no-progress
cd cli/azd
```

- **Line length**: 125 chars max for Go (enforced by `lll` linter); no limit for Markdown
- **Spelling (Go)**: Add technical terms to `cli/azd/.vscode/cspell.yaml` overrides
- **Spelling (docs/misc)**: Add terms to `.vscode/cspell.misc.yaml` overrides or `.vscode/cspell.global.yaml`
  - Use file-scoped `overrides` entries (not the global `words` list) for terms specific to one file
- **Copyright**: All Go files need the Microsoft header (handled by copyright-check.sh)
- **Code modernization**: `go fix ./...` applies automatic modernizations (e.g. `interface{}` → `any`,
  loop simplifications, use of `slices`/`maps` packages). CI enforces this check.

### Linting Details

The project uses `golangci-lint` with these key linters enabled (see `.golangci.yaml`):
- **`lll`** — max line length 125 characters (tab width 4). Break long lines with continuation.
- **`gofmt`** — standard Go formatting
- **`forbidigo`** — forbids `fmt.Print*` and `os.Stdout` in test files to prevent phantom CI failures (see [#8385](https://github.com/Azure/azure-dev/issues/8385)). Scoped to `*_test.go` only; allowlisted files that intentionally test stdout are excluded.
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

When working on CLI output, terminal UX, spinners, progress states, colors, or prompts, you **MUST read** the relevant style guide before making any changes or recommendations. Reference the right file for the flow you're working on:

📄 **`cli/azd/docs/style-guidelines/azd-style-guide.md`** — **core azd design patterns** (full path from repo root)

This file is the authoritative reference for core azd terminal UX patterns including:
- Progress report states (`(✓) Done`, `(x) Failed`, `(!) Warning`, `(-) Skipped`)
- Spinner type (bar-fill `|=======|`)
- Color conventions (`WithSuccessFormat`, `WithErrorFormat`, `WithHighLightFormat`, etc.)
- User input patterns (text input, list select, yes/no confirm)
- Prompt styling (`?` marker in bold blue, `[Type ? for hint]`, post-submit states)

📄 **`cli/azd/docs/style-guidelines/agentic-ux-style-guide.md`** — **agentic (AI / GitHub Copilot) UX patterns**

Read this file instead when working on the AI-driven / agentic experience (the "Set up with GitHub Copilot (Preview)" flow, `azd agent`, the copilot session runtime, and the `AgentDisplay` renderer). It documents the distinct magenta + glyph visual language for agent identity, tool activity, subagents, and thinking states.

> **Note**: These are two distinct visual systems. The core guide's patterns are **not enforced on extensions**, but extension developers are encouraged to follow them where applicable for consistency. Do **not** apply core azd status patterns to agentic flows (or vice versa) — pick the guide that matches the flow. Extension-specific UX is documented in `cli/azd/docs/extensions/extensions-style-guide.md`.

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

### Concurrency

The graph-driven `up`/`provision`/`deploy` engine runs multiple service steps and (when `infra.layers[]` is configured) multiple layer provision steps in parallel. Several long-lived types now have explicit locking contracts that you MUST honor when adding new methods or write paths.

See [docs/concurrency-model.md](./docs/concurrency-model.md) for the full list — `environment.Environment`, `environment.Manager`, `kubectl.Cli`, `containerAppTarget`/`aksTarget`, and `serviceManager` — and the rules for adding new concurrent state.

When adding a method that mutates one of these types: take the documented lock, hold it across the full read-modify-write, and run `go test -race` to catch missed locks (single-goroutine tests will not).

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

#### Adding or Changing Telemetry (Events & Fields)

`azd` telemetry coverage is tracked by an ongoing metrics audit whose authoritative spec lives in
`docs/specs/metrics-audit/`. When you add or change a telemetry event or field you MUST update the
code, **all** telemetry docs, and the coverage test in the same change — otherwise the audit, the
public reference, and downstream Kusto/LENS consumers drift out of sync. Verify every claim against
**actual code behavior**, not comments.

**1. Code**

- **Field** — define an `AttributeKey` in `cli/azd/internal/tracing/fields/fields.go` (this file
  holds the field/key definitions; within the same package `features.go` holds feature-name
  attribute values and `domains.go` the Azure host-domain table). Every field MUST set a
  `Classification` (e.g. `SystemMetadata`, `OrganizationalIdentifiableInformation`,
  `EndUserPseudonymizedInformation`; never emit `CustomerContent`) and a `Purpose`
  (`FeatureInsight` / `BusinessInsight` / `PerformanceAndHealth`).
- **Event** — define a constant in `cli/azd/internal/tracing/events/events.go` following the
  `prefix.noun.verb` naming convention.
- **Emit** at the call site via `tracing.Start` (spans/events) plus `tracing.SetUsageAttributes`
  or `span.SetAttributes` (attributes).
- **Hash user-derived values** with `fields.StringHashed` / `fields.StringSliceHashed`
  (`cli/azd/internal/tracing/fields/key.go`). Hash anything that embeds a user-chosen name, path,
  repo URL, or project / env / service / layer identifier (e.g. `exegraph.step.name`, `hooks.name`).
  Emit raw only for fixed enums or compile-time literals.

**2. Documentation — keep all of these in sync**

- `docs/reference/telemetry-data.md` — public, user-facing reference for every event/field. Add to
  **Events Reference** / **Fields Reference** with a one-line description, value type
  (string/bool/number), and applicable events/commands; note conditional or feature-gated behavior
  under **Data Nuances & Gotchas** and/or **Feature → Telemetry Mapping**.
- `docs/specs/metrics-audit/telemetry-schema.md` — authoritative schema: add a row with the OTel
  key, classification, purpose, whether it is hashed, whether it is a measurement, and the allowed
  enum values.
- `docs/specs/metrics-audit/feature-telemetry-matrix.md` — command→telemetry inventory: update the
  command's row (and/or the Cross-Cutting Subsystems table) and the ✅/⚠️/❌ coverage flags.
- `docs/specs/metrics-audit/privacy-review-checklist.md` — if the field is hashed (always or
  conditionally), add it to the matching hashing table, and copy the **PR Checklist Template** into
  your PR description.

**3. Tests**

- `cli/azd/cmd/telemetry_test.go` — classify the command in exactly one of
  `commandsWithSpecificTelemetry` / `commandsWithOnlyGlobalTelemetry` (both lists are kept sorted),
  and add field-constant assertions where applicable.

**4. Privacy & downstream**

- A privacy review is required for any new field/event, any classification/purpose change, or any
  removal of hashing — see the triggers in `privacy-review-checklist.md`.
- If the field is queried downstream, coordinate Kusto-function / cooked-table / dashboard updates
  per the recurring process in `docs/specs/metrics-audit/audit-process.md`.

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
- **Never write to `os.Stdout` in tests**: Tests that write directly to `os.Stdout` (via `fmt.Print*`, `os.Stdout`, or UX components like `ux.NewSpinner` without a `Writer` option) corrupt the `go test -json` event stream under parallel execution, causing phantom test failures in CI. Use `t.Log`/`t.Logf` for diagnostic output, `io.Discard` or `&bytes.Buffer{}` for UX component writers, and `SkipLoadingSpinner: true` for prompt tests that don't need spinner behavior. Enforced by the `forbidigo` linter in `.golangci.yaml`. See [#8385](https://github.com/Azure/azure-dev/issues/8385) for details.

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

- `docs/style-guidelines/azd-style-guide.md` - Core azd CLI style guide (colors, spinners, progress states, terminal UX)
- `docs/style-guidelines/agentic-ux-style-guide.md` - Agentic (AI / GitHub Copilot) UX patterns (magenta + glyph vocabulary)
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

## Copilot Code Review

When reviewing code in Copilot Code Review, also use the azd-code-reviewer skill to look for azd-specific criteria.
