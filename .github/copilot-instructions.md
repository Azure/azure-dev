# Copilot instructions — `azure.ai.agents` extension

I work almost exclusively in **`cli/azd/extensions/azure.ai.agents/`**, and most of that
in **`internal/cmd/`**. Scope guidance and commands here to that area unless the task
explicitly says otherwise.

## Required reading

Before changing or reviewing code, read these in full:

1. [`cli/azd/extensions/azure.ai.agents/AGENTS.md`](../cli/azd/extensions/azure.ai.agents/AGENTS.md)
   — extension-specific conventions (errors, release flow, `log` vs `fmt`, etc.).
2. [`cli/azd/AGENTS.md`](../cli/azd/AGENTS.md) — applies whenever you touch Go code under
   `cli/azd`. Modern Go 1.26 patterns, lint rules, and IoC/Action conventions.

These files are authoritative — do not summarize from memory when reviewing PRs.

## What this extension is

`azure.ai.agents` is a first-party azd extension that ships as a **separate Go binary**
and talks to the azd host over **gRPC**. It depends on `cli/azd` via a semver tag in its
own `go.mod` (`github.com/azure/azure-dev/cli/azd vX.Y.Z`) — it is **not** part of the
main azd Go module.

Top-level layout under `cli/azd/extensions/azure.ai.agents/`:

```
main.go                # Entry point — wires Cobra root and runs it
internal/cmd/          # All Cobra commands + shared command-layer helpers (this is where I work)
internal/project/      # Project/service-target integration and deployment flows
internal/pkg/          # Lower-level helpers, parsers, API clients (agent_api, agent_yaml, registry_api, azure)
internal/exterrors/    # Structured error factories + stable codes (codes.go)
internal/tools/        # MCP tool implementations
internal/version/      # Build-time version info (Version, Commit, BuildDate)
schemas/               # JSON schemas (e.g., agent.yaml validation)
tests/                 # Test fixtures
extension.yaml         # Extension manifest (capabilities, version)
version.txt            # Single source of semver string
CHANGELOG.md           # Release notes
.golangci.yaml         # Extension-local lint config (use this, NOT cli/azd's)
cspell.yaml            # Extension-local spell config (use this, NOT cli/azd's)
```

## Build, test, lint

All commands run from `cli/azd/extensions/azure.ai.agents/`.

```bash
# Build
azd x build           # via the developer extension (recommended for local dev)
go build              # plain Go build

# Tests
go test ./...                                 # full extension test suite
go test ./internal/cmd/...                    # all cmd tests (matches what I usually want)
go test ./internal/cmd/ -run TestInvoke       # one test
go test ./internal/cmd/ -run TestInvoke -v    # verbose
go test -race ./internal/cmd/...              # race detector — use when touching concurrent code

# Lint / spell check (matches the extension's CI)
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./cspell.yaml --no-progress

# CI scripts (run the full pre-publish checks)
./ci-build.ps1
./ci-test.ps1
```

The extension has its **own** `.golangci.yaml` and `cspell.yaml` at its root — use those,
not the ones under `cli/azd/`.

### Local dev against an unmerged azd core change

Two-PR flow when an extension change depends on a new core change:

1. Land the core change in `cli/azd/` first.
2. Then update this extension with `go get github.com/azure/azure-dev/cli/azd && go mod tidy`.

For local validation only, you may temporarily add a replace directive:

```go
replace github.com/azure/azure-dev/cli/azd => ../../
```

**Do not merge** the extension PR with that `replace` still present.

---

# Deep dive: `internal/cmd/`

This is the Cobra command layer. The package name is `cmd`. Files come in three flavors:

- **Command files** — one per top-level command (`init.go`, `invoke.go`, `run.go`,
  `show.go`, `monitor.go`, `files.go`, `session.go`, `listen.go`, `mcp.go`,
  `metadata.go`, `version.go`).
- **Shared infrastructure** — `root.go` (command tree + global flags), `agent_context.go`
  (endpoint/credential/client construction), `helpers.go` (the big shared utility file),
  `config_store.go` (UserConfig-backed session/conversation persistence), `debug.go`
  (logging setup), `banner.go` (FOUNDRY ASCII art), `init_*.go` (init helpers split out
  for size — copy/foundry/templates/locations/models/from-code), `monitor_format.go`.
- **Tests** — `*_test.go` alongside each source file.

## Command catalog

All commands surface as `azd ai agent <command>` to end users (the host wraps the
extension's binary). The user-visible names below match `Use:` in each Cobra command.

| Command         | File           | Visible? | Purpose |
| --------------- | -------------- | -------- | ------- |
| `init`          | `init.go` (+ `init_*.go`) | yes | Scaffold a new agent project from a manifest, template, or existing source. Largest command in the package — split across multiple files. |
| `run`           | `run.go`       | yes      | Run the agent locally for development. Auto-detects Python / .NET / Node project and starts a foreground server (default port `DefaultPort = 8088`). |
| `invoke`        | `invoke.go`    | yes      | Send a message (or file body via `-f`) to a remote Foundry agent or local server (`--local`). Persists session per-agent so consecutive invokes reuse the same session unless `--new-session`. |
| `show`          | `show.go`      | yes      | Show status of a hosted agent. Supports `--output json|table`. |
| `monitor`       | `monitor.go` (+ `monitor_format.go`) | yes | Stream/fetch agent session logs (`stdout/stderr` or system events). Supports `--follow`, `--tail`, `--type`, `--utc`, `--raw`. |
| `files`         | `files.go`     | yes      | Sub-tree: `upload`, `download`, `list`, `remove` (`rm`), `mkdir`, `stat` for a session-scoped filesystem on a hosted agent. |
| `sessions`      | `session.go`   | yes      | Sub-tree: `create`, `show`, `delete`, `list` for hosted agent sessions. **Note**: `Use: "sessions"` (plural) even though the file is `session.go`. |
| `listen`        | `listen.go`    | hidden   | Long-running gRPC listener — registered automatically by azd via the extension framework. Wires `ServiceTarget` and `preprovision`/`postprovision`/`predeploy`/`postdeploy`/`postdown` event handlers. |
| `mcp start`     | `mcp.go`       | hidden   | Start a stdio MCP server exposing this extension's tools (`tools.NewAddAgentTool()`, etc.). |
| `metadata`      | `metadata.go`  | hidden   | Emit extension metadata JSON for the host registry — produced via `azdext.GenerateExtensionMetadata`. |
| `version`       | `version.go`   | yes      | Print `Version`, `Commit`, `BuildDate` from `internal/version`. |

Hidden commands (`Hidden: true`) are infrastructure that azd or tools call — **don't
remove or rename** without checking host code that talks to them.

## Root command (`root.go`)

- Root is `agent <command>`. `Short` includes a yellow "(Preview)" suffix; preserve that
  styling on new top-level user-facing commands.
- Global flags live on `rootFlags` (singleton `rootFlagsDefinition{Debug, NoPrompt}`).
  They are persistent flags, so child commands read them via the package-level
  `rootFlags.NoPrompt` / `rootFlags.Debug` (e.g., `resolveAgentServiceFromProject(...,
  rootFlags.NoPrompt)`).
- `--no-prompt` is **propagated from the host via the `AZD_NO_PROMPT` env var** in
  `PersistentPreRunE`. If you add a new place that needs no-prompt behavior, read
  `rootFlags.NoPrompt` — don't read `AZD_NO_PROMPT` directly.
- Help: the root `HelpFunc` is overridden so `printBanner` runs above the default help.
  The Cobra `help` command itself is hidden (`SetHelpCommand(&cobra.Command{Hidden:
  true})`).
- `SilenceUsage: true` and `SilenceErrors: true` are set — errors are formatted by the
  caller, not by Cobra.

When **adding a new top-level command**:
1. Create `internal/cmd/<name>.go` with `func new<Name>Command() *cobra.Command`.
2. Wire it into `NewRootCommand()` via `rootCmd.AddCommand(...)`.
3. Add a `<name>_test.go` next to it.
4. If the command takes user-tunable flags, mirror the existing struct pattern (see
   below).

## Standard command boilerplate

Almost every visible command follows this exact shape — match it:

```go
type fooFlags struct {
    name   string
    output string
    // ...
}

func newFooCommand() *cobra.Command {
    flags := &fooFlags{}

    cmd := &cobra.Command{
        Use:     "foo [name]",
        Short:   "One-line description (ends with no period in tests).",
        Long:    `Multi-paragraph description...`,
        Example: `  # Auto-resolve from azure.yaml
  azd ai agent foo

  # Specific service
  azd ai agent foo my-agent`,
        Args: cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            if len(args) > 0 {
                flags.name = args[0]
            }

            // 1) Inject host access token into ctx so gRPC calls authenticate.
            ctx := azdext.WithAccessToken(cmd.Context())

            // 2) Silence stdlib log unless --debug; clean up via defer.
            logCleanup := setupDebugLogging(cmd.Flags())
            defer logCleanup()

            // 3) Open an azd host client.
            azdClient, err := azdext.NewAzdClient()
            if err != nil {
                return fmt.Errorf("failed to create azd client: %w", err)
            }
            defer azdClient.Close()

            // 4) Resolve service/agent details from azure.yaml + env.
            info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.name, rootFlags.NoPrompt)
            if err != nil {
                return err
            }
            // ... real work ...
        },
    }

    cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")
    return cmd
}
```

Things this pattern **deliberately** does, and you should keep doing:

- `azdext.WithAccessToken(cmd.Context())` is required before any gRPC call back to the
  host — it threads the host-issued token onto the context.
- `setupDebugLogging` must run early (and defer-cleaned) so stray `log.Printf`
  output is silenced unless `--debug`. See `debug.go`.
- `azdext.NewAzdClient()` is **always paired with `defer azdClient.Close()`**. Never
  leak the gRPC connection.
- Flags are bound to a struct, not standalone vars. Subcommand groups (e.g., `files`,
  `sessions`) often share an `add<Group>Flags(cmd, flags)` helper instead of repeating
  flag declarations across each sub-leaf.

## Subcommand groups (`files`, `sessions`)

`files` and `sessions` use a parent command with `PersistentPreRunE` that **chains to
the root** rather than to the immediate parent. The reason is documented in both
`files.go` and `session.go`:

> `cmd.Parent()` would return this command itself when a subcommand runs, causing
> infinite recursion. Use `cmd.Root()` (or capture the outer `cmd` in the closure).

When adding a new subcommand group, copy this pattern verbatim — it's a known foot-gun.

## Banner (`banner.go`)

`printBanner(w io.Writer)` writes the FOUNDRY ASCII art in purple bold (RGB
`109,53,255`), followed by `v<version>` (gray) and a doc link. It is called by:
- The root command's overridden `HelpFunc` (so `--help` shows the banner once).
- The `init` command's `RunE` at the start of execution.

Don't add banner prints to other commands — this would make scripted invocations
unwieldy. The banner art width is 61 visible columns; tests use rune-aware width
measurement, so any change to `bannerArt` must keep all rows aligned and run tests.

## `agent_context.go` — endpoint, credential, client

Three small but central helpers here:

- `newAgentContext(ctx, accountName, projectName, name, version)` — builds an
  `AgentContext` whose `ProjectEndpoint` is resolved by `resolveAgentEndpoint`.
- `resolveAgentEndpoint(ctx, accountName, projectName)` — if both account and
  project flags are passed, builds the endpoint directly; otherwise falls back to the
  `AZURE_AI_PROJECT_ENDPOINT` value from the **current azd environment** (via
  `azdext.AzdClient.Environment().GetCurrent` + `GetValue`). Returns clear errors
  pointing the user at `--account-name`/`--project-name` flags or `azd init`.
- `newAgentCredential()` — wraps `azidentity.NewAzureDeveloperCLICredential`. Because
  this is `AzureDeveloperCLICredential` (NOT `AzureCLICredential`), user-facing
  messages for auth failures should say **`azd auth login`**, not `az login`.
- `(*AgentContext).NewClient()` — the canonical way to construct an
  `agent_api.AgentClient`. Don't `agent_api.NewAgentClient(...)` directly from a
  command; go through `AgentContext`.

API-version constants live here and are consumed throughout the package:
- `DefaultAgentAPIVersion = "2025-11-15-preview"`
- `ConversationsAPIVersion = "v1"`

## `helpers.go` — the shared toolbox

Nearly 700 lines of helpers consumed by every command. The most important groups:

- **Service/agent resolution** — `resolveAgentService`, `resolveAgentServiceFromProject`,
  `promptForAgentService`. Given an optional explicit name plus the azd project config,
  these locate the matching `azure.ai.agent`-host service in `azure.yaml`, prompt to
  pick one if multiple exist (or return an error in `--no-prompt` mode), and read the
  deployed `AGENT_{SVC}_NAME` / `_VERSION` / `_ENDPOINT` env vars from the current
  environment. **Always go through these helpers** rather than poking
  `azure.yaml`/env vars directly — they handle the multi-service prompt and validation
  flow consistently.
- **Local agent key construction** — `resolveLocalAgentKey`,
  `resolveLocalAgentKeyWithPort`, `buildLocalAgentKey`. The local key format is
  `localhost:<port>/<projectHash>/agents/<name>/versions/latest/local`. Used by the
  config store to namespace per-project sessions.
- **Session/conversation resolution** — `resolveStoredID`, `resolveConversationID`,
  `saveContextValue`, `captureResponseSession`. These call into `config_store.go` to
  read/write the persisted IDs. `captureResponseSession` reads the
  `x-agent-session-id` response header and persists it when the caller didn't
  pre-supply a session.
- **Project type detection** — `detectProjectType` / `detectStartupCommand` map
  `pyproject.toml`/`requirements.txt`/`*.csproj`/`package.json` to `python`/`dotnet`/
  `node` plus a default start command. Used by `run.go` and `init` flows.
- **`fetchOpenAPISpec`** — best-effort cache of the agent's `/invocations/docs/openapi.json`
  on disk under `.azure/<env>/openapi-<safeName>-<local|remote>.json`. Failures are
  intentionally silent. The agent name is sanitized for path traversal (`..`, `/`, `\`).
- **Output helpers** — `printSessionStatus` formats the `Session: ...` banner that
  `invoke` prints.

If you need a new utility used by 2+ commands, put it in `helpers.go` and unit-test it
in `helpers_test.go`.

## `config_store.go` — UserConfig-backed persistence

- All session/conversation state lives in **azd's UserConfig** under the
  `extensions.ai-agents` namespace. The legacy `.foundry-agent.json` file is
  **migration-only** (`migrateFromLegacyFile` runs once and deletes the file on
  success).
- Allowed store fields are gated by an explicit allow-list:
  `validStoreFields = {"sessions", "conversations"}`. `validateStoreField` rejects
  anything else. **Do not bypass this** — if you need a new field, add it to the map
  AND add a top-level helper for it.
- Key segments are validated by `validateKeySegment` — they must not contain `/` or
  `\` (the agent key already includes `/`-separated structure, so segments are
  individual values like the agent name).
- Read/write entry points: `getAgentSpecificContextValue`,
  `setAgentSpecificContextValue`, `getContextValueWithFallback` (checks legacy keys),
  and `setContextValueSafe` (logs and swallows errors — used from non-critical paths).
  Use `getContextValueWithFallback` whenever reading a stored ID so legacy keys
  continue to work for users mid-migration.

## `debug.go` — logging discipline

- `setupDebugLogging(cmd.Flags()) func()` — call **at the top of every `RunE`** and
  defer the returned cleanup. Without `--debug` (and absent `AZD_EXT_DEBUG=true`),
  Go's stdlib `log` and the Azure SDK's `azcorelog` are routed to `io.Discard`.
- With debug enabled, logs go to a date-stamped file `azd-ai-agents-YYYY-MM-DD.log` in
  the working directory, falling back to stderr if the file can't be opened.
- Connection strings in Azure SDK log messages are redacted via
  `connectionStringJSONRegex` — preserve this pattern when adding new sensitive log
  paths.
- The `--debug` flag and `AZD_EXT_DEBUG` env var are equivalent toggles. Don't add
  alternative debug flags.

## Init command specifics (`init.go` + `init_*.go`)

This is by far the largest command and the one most likely to be touched. Key
behaviors to preserve:

- Calls `printBanner` at the start of `RunE` (only the root help and `init` do this).
- `checkAiModelServiceAvailable(ctx, azdClient)` is a **temporary host-version probe**
  to fail fast on hosts that don't expose the AI gRPC service. Remove only when azd
  core enforces `requiredAzdVersion`.
- `azdext.WaitForDebugger(ctx, azdClient)` is honored when `AZD_EXT_DEBUG` is set —
  cancellation and `azdext.ErrDebuggerAborted` are treated as a clean exit (return
  `nil`), not an error.
- `ensureLoggedIn(ctx, authStatusFromCLI)` shells out to `azd auth status --output
  json --no-prompt`. The function takes the auth-status fetcher as a parameter so
  tests can inject a stub — keep that injection seam if you refactor.
- Manifest auto-detect (`detectLocalManifest`) runs when `--manifest` is not set and
  prompts via `azdClient.Prompt().Confirm(...)` to use the existing one. In
  `--no-prompt` mode, the existing manifest is used implicitly. The `Confirm` call
  uses `DefaultValue: new(true)` — **note the `new(true)` Go 1.26 idiom**; do not
  rewrite as `boolPtr(true)` or `&t`.
- Cancellation is handled via `exterrors.IsCancellation(err)` which converts to
  `exterrors.Cancelled("initialization was cancelled")`.
- Init helpers are split by concern across files — keep that split:
  - `init_copy.go` — copying template files
  - `init_foundry_resources_helpers.go` — Foundry resource creation/lookup
  - `init_from_code.go` — initializing from existing source
  - `init_from_templates_helpers.go` — template selection/download
  - `init_locations.go` — region selection (`hosted-agent-regions.json` is the data
    file)
  - `init_models.go` — model selection / `modelSelector` struct on `InitAction`

## `listen` command and the host event flow

`listen` is the gRPC server lifecycle. It registers with `azdext.NewExtensionHost`:

- `WithServiceTarget(AiAgentHost, ...)` — `AiAgentHost = "azure.ai.agent"`. **This
  string MUST match the service host name in `extension.yaml`.** If you rename one,
  rename both.
- `WithProjectEventHandler("preprovision" | "postprovision" | "predeploy" |
  "postdeploy" | "postdown", ...)` — these handlers live in `internal/project/` (look
  for `preprovisionHandler`, `postprovisionHandler`, etc.). Adding a new event hook
  means: register it here AND implement the handler in `internal/project/`.
- `host.Run(ctx)` blocks until the host disconnects.

`listen` is `Hidden: true` — it's invoked by the host, not by users.

## Test patterns

- Each command file has a sibling `_test.go` (e.g., `invoke.go` ↔ `invoke_test.go`).
- Tests use `t.Context()` (NOT `context.Background()`) and `t.Chdir(dir)` per the
  Go 1.26 rules in `cli/azd/AGENTS.md`. The codebase is consistent on this — match it.
- For `ensureLoggedIn` and similar shell-out helpers, tests inject a stub fetcher
  rather than mocking `os/exec` — preserve that injection seam when refactoring.
- Table-driven tests are the norm. Use `testify` for assertions/mocks (already pinned
  in `go.mod`).

---

## Conventions cheat sheet (extension-wide)

(Full details in the extension's `AGENTS.md` — these are the ones that bite most often.)

- **Errors — use plain `fmt.Errorf("context: %w", err)` by default.** Only switch to
  `internal/exterrors` when you can answer all three: telemetry category, stable error
  code, and user suggestion. Create the structured error **once**, as close to the
  source as possible. Never `fmt.Errorf` a structured error — gRPC serialization drops
  the outer wrapper. Pick the right factory: `Validation`, `Dependency`, `Auth`,
  `Compatibility`, `Cancelled`, `ServiceFromAzure`, `FromAiService`, `FromPrompt`,
  `Internal`. New codes go in `internal/exterrors/codes.go` as lowercase `snake_case`.
- **Output — `fmt` for users, `log` for diagnostics.** `fmt.Print*` to stdout for
  user-facing output (pair with `output.With*Format` for color); `log.Print*` to
  stderr, hidden unless `--debug`. Never `log.Fatal`/`log.Panic` for expected failures.
- **Auth — use `Subscription.UserTenantId`, NOT `Subscription.TenantId`** when creating
  credentials from `PromptSubscription()`. The credential helper for command auth is
  `azidentity.NewAzureDeveloperCLICredential`, so user-facing guidance for auth issues
  should mention `azd auth login`, not `az login`.
- **Reading the current environment** — `azdext.GetEnvRequest.EnvName` is optional;
  leave it empty to use the current azd environment. The pattern in
  `agent_context.go` (`GetCurrent` then `GetValue` with the returned name) is more
  explicit when the env name is needed for error messages.
- **Standard `log` is silenced** unless `--debug` is set (see `setupDebugLogging` in
  `debug.go`). Don't rely on stray `log.Printf` showing up in normal runs.
- **Agent name validation regex**:
  `^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$` (1–63 chars, alphanumeric with
  internal hyphens). Enforced in `internal/pkg/agents/agent_yaml/parse.go`.
- **`AgentClient.ListSessionFiles`** treats an empty `remotePath` as "session root" and
  omits the `path` query parameter. Don't pass `"/"` expecting the same behavior.
- **Modern Go 1.26 patterns are required** (CI enforces `go fix ./...`):
  `new(val)` (not `x := val; &x`), `errors.AsType[T](err)`, `slices.SortFunc` /
  `slices.Clone` / `slices.Sorted(maps.Keys(m))`, `wg.Go(...)`, `t.Context()`,
  `t.Chdir(dir)`, `for i := range n`, `http.NewRequestWithContext`.
- **Line length — 125 chars max** for Go (extension's own `.golangci.yaml` enables
  `lll`).
- **Copyright header** required on every Go source file:
  `// Copyright (c) Microsoft Corporation. All rights reserved.` /
  `// Licensed under the MIT License.`
- **`go.mod`/`go.sum` discipline** — only commit changes to *this* extension's
  `go.mod`/`go.sum`. Never co-commit changes to `cli/azd/go.mod` or other extensions'
  module files.

## Releasing

Two PRs, in this order (from the extension's `AGENTS.md`):

1. **Version bump PR** — touches only `version.txt`, `extension.yaml` (`version:`), and
   `CHANGELOG.md` (new section at top). Merging this triggers the CI release pipeline,
   which builds, signs, and publishes the GitHub release.
2. **Registry update PR** — after the GitHub release is live, regenerate the
   `cli/azd/extensions/registry.json` entry by running `azd x publish` against the
   published artifacts. PR contains only that regenerated entry (and possibly updated
   test snapshots).
