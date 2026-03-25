# Spec: Global Flags and Extension Flag Dispatch

## Status

**Documenting existing behavior** — this spec formalizes the flag contract that already exists in the azd + extension SDK implementation, and adds enforcement that was previously missing.

## Goal

Define how azd handles global flags when dispatching to extensions, including the pre-parsing pipeline, environment variable propagation, and the reserved flag registry that prevents namespace collisions.

## Background

azd extensions are standalone binaries that azd discovers, installs, and invokes as subcommands. When a user runs `azd model custom create --endpoint https://...`, azd:

1. Pre-parses its own global flags from the full argument list
2. Launches the extension binary as a child process
3. Passes the raw arguments **and** global flag values (via environment variables) to the extension

This creates a **shared flag namespace** — both azd and the extension parse the same `argv`. If an extension registers a flag that collides with an azd global flag (e.g., both use `-e`), azd's pre-parser consumes the value for its own purpose, and the extension either gets the wrong value or causes azd to error.

Issue [#7271](https://github.com/Azure/azure-dev/issues/7271) demonstrated this: the `azd model` extension used `-e` for `--endpoint` (a URL), but azd's pre-parser treated the URL as an environment name and failed validation.

## Architecture

### Flag Flow Diagram

```
User runs: azd model custom create -e https://example.com/api

   ┌─────────────────────────────────────────────────────┐
   │ azd host process                                    │
   │                                                     │
   │  1. ParseGlobalFlags(args)                          │
   │     - Reads: -e/--environment, --debug, --cwd, etc. │
   │     - UnknownFlags: true (ignores extension flags)  │
   │     - Populates GlobalCommandOptions                │
   │                                                     │
   │  2. extensions.go: DisableFlagParsing: true          │
   │     - Cobra does NOT parse extension-specific flags  │
   │     - Raw args passed through to extension           │
   │                                                     │
   │  3. runner.go: Invoke()                              │
   │     - Converts GlobalCommandOptions → AZD_* env vars │
   │     - Launches extension binary with:                │
   │       - Args: original argv (including -e value)     │
   │       - Env: AZD_ENVIRONMENT, AZD_DEBUG, etc.        │
   └──────────────────────┬──────────────────────────────┘
                          │
                          ▼
   ┌─────────────────────────────────────────────────────┐
   │ Extension binary (child process)                    │
   │                                                     │
   │  1. NewExtensionRootCommand() [SDK]                 │
   │     - Registers SAME global flags:                  │
   │       --environment/-e, --debug, --cwd/-C, etc.     │
   │     - Falls back to AZD_* env vars if not on CLI    │
   │                                                     │
   │  2. Extension-specific subcommands                  │
   │     - Register their OWN flags (--model, --version) │
   │     - Must NOT collide with reserved flags          │
   └─────────────────────────────────────────────────────┘
```

### Key Insight

Both azd and the extension parse the **same arguments**. azd does not strip its global flags before passing args to extensions. This means:

- If an extension reuses `-e` for `--endpoint`, azd's pre-parser sees `-e https://example.com/api` and tries to use the URL as an environment name
- The extension then also receives `-e https://example.com/api` in its args, but the SDK's root command binds `-e` to `--environment`, so the extension's own `-e` flag on a subcommand creates a conflict

This is not a new restriction — it has been true since the extension system was designed.

## Global Flags (Host Side)

These flags are registered in `CreateGlobalFlagSet()` (`cmd/auto_install.go`) and pre-parsed by `ParseGlobalFlags()` before command dispatch:

| Long Name | Short | Type | Default | Hidden | Description |
|-----------|-------|------|---------|--------|-------------|
| `environment` | `e` | string | `$AZURE_ENV_NAME` | No | The name of the environment to use |
| `cwd` | `C` | string | `""` | No | Sets the current working directory |
| `debug` | — | bool | `false` | No | Enables debugging and diagnostics logging |
| `no-prompt` | — | bool | `false` | No | Accepts default value instead of prompting |
| `output` | `o` | string | `"default"` | No | The output format (json, table, none) |
| `help` | `h` | — | — | No | Help for the current command (cobra built-in) |
| `docs` | — | — | — | No | Opens documentation for the current command |
| `trace-log-file` | — | string | `""` | Yes | Write a diagnostics trace to a file |
| `trace-log-url` | — | string | `""` | Yes | Send traces to an OpenTelemetry endpoint |

### Pre-parsing Behavior

`ParseGlobalFlags` uses `pflag.ParseErrorsAllowlist{UnknownFlags: true}` to silently ignore flags it doesn't recognize. This allows extension-specific flags (like `--model`, `--version`) to pass through without error. However, any flag that matches an azd global flag **will be consumed** by the pre-parser.

## Global Flags (Extension SDK Side)

The extension SDK's `NewExtensionRootCommand()` (`pkg/azdext/extension_command.go`) registers these persistent flags on every extension's root command:

| Long Name | Short | Type | Default | Env Var Fallback |
|-----------|-------|------|---------|-----------------|
| `environment` | `e` | string | `""` | `AZD_ENVIRONMENT` |
| `cwd` | `C` | string | `""` | `AZD_CWD` |
| `debug` | — | bool | `false` | `AZD_DEBUG` |
| `no-prompt` | — | bool | `false` | `AZD_NO_PROMPT` |
| `output` | `o` | string | `"default"` | — |
| `trace-log-file` | — | string | `""` | — |
| `trace-log-url` | — | string | `""` | — |

### Env Var Propagation

azd passes global flag values to extensions via two mechanisms:

1. **Environment variables** (`runner.go`): `AZD_DEBUG`, `AZD_NO_PROMPT`, `AZD_CWD`, `AZD_ENVIRONMENT`
2. **Raw args**: The original command-line arguments are passed through unchanged

The SDK's `PersistentPreRunE` checks if each flag was explicitly set on the command line; if not, it falls back to the corresponding `AZD_*` environment variable. This dual-path design ensures global values are available whether the extension is invoked via azd or directly during development.

## Reserved Flags

### Definition

A **reserved flag** is any flag that azd pre-parses from the command line before dispatching to extensions, or that the extension SDK registers on the root command. Extensions must not register flags with the same long name or short name on their subcommands.

### The Reserved List

The canonical list is maintained in two locations that are kept in sync by a test:

- **Host side**: `internal/reserved_flags.go` — `ReservedFlags` slice with lookup helpers
- **SDK side**: `pkg/azdext/reserved_flags.go` — `reservedGlobalFlags` slice with validation

| Long Name | Short | Reason Reserved |
|-----------|-------|----------------|
| `environment` | `e` | azd pre-parses for env selection; SDK registers on root |
| `cwd` | `C` | azd pre-parses for working directory; SDK registers on root |
| `debug` | — | azd pre-parses for debug mode; SDK registers on root |
| `no-prompt` | — | azd pre-parses for non-interactive mode; SDK registers on root |
| `output` | `o` | SDK registers on root for output format |
| `help` | `h` | cobra built-in; universal across all commands |
| `docs` | — | azd root command flag |
| `trace-log-file` | — | azd pre-parses for telemetry; SDK registers on root |
| `trace-log-url` | — | azd pre-parses for telemetry; SDK registers on root |

## Enforcement

### SDK-Level Validation

`ValidateNoReservedFlagConflicts(root)` is called in `azdext.Run()` before command execution. It:

1. Walks the entire command tree
2. For each command's flags, checks both long and short names against the reserved list
3. Skips flags on the root command's persistent flag set (those are the SDK-provided azd-compatible flags)
4. Returns a detailed error listing all conflicts with remediation guidance

Any extension built with the azd SDK gets this check automatically — no opt-in required.

### Sync Test

A test (`TestReservedFlagNames_SyncWithInternal`) ensures the SDK-side and host-side reserved flag lists stay in sync. If a developer adds a new global flag to one list but not the other, the test fails.

## Adding a New Global Flag

When azd needs a new global flag:

1. Add the flag to `CreateGlobalFlagSet()` in `cmd/auto_install.go`
2. Add parsing logic to `ParseGlobalFlags()` in `cmd/auto_install.go`
3. Add to `ReservedFlags` in `internal/reserved_flags.go`
4. Add to `reservedGlobalFlags` in `pkg/azdext/reserved_flags.go`
5. If it should be available to extensions:
   - Register it in `NewExtensionRootCommand()` in `pkg/azdext/extension_command.go`
   - Add env var propagation in `runner.go`
   - Add env var fallback in `NewExtensionRootCommand`'s `PersistentPreRunE`
6. Run tests — the sync test will catch mismatches between steps 3 and 4

The reserved flag registry makes this process explicit and safe: any extension that happens to use the new flag name will get a clear error at startup instead of a mysterious runtime failure.

## Implementation References

| Component | File |
|-----------|------|
| Global flag registration | `cmd/auto_install.go` — `CreateGlobalFlagSet()` |
| Global flag pre-parsing | `cmd/auto_install.go` — `ParseGlobalFlags()` |
| Extension dispatch | `cmd/extensions.go` — `DisableFlagParsing: true` |
| InvokeOptions construction | `cmd/extensions.go` — global options propagation |
| Env var propagation | `pkg/extensions/runner.go` — `Invoke()` |
| SDK flag registration | `pkg/azdext/extension_command.go` — `NewExtensionRootCommand()` |
| SDK env var fallback | `pkg/azdext/extension_command.go` — `PersistentPreRunE` |
| Reserved flags (host) | `internal/reserved_flags.go` |
| Reserved flags (SDK) | `pkg/azdext/reserved_flags.go` |
| SDK enforcement | `pkg/azdext/reserved_flags.go` — `ValidateNoReservedFlagConflicts()` |
| Enforcement hook | `pkg/azdext/run.go` |
