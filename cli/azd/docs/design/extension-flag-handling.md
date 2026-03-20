# Extension Flag Handling Design

> **Status**: Design summary of current implementation (not a prescriptive spec).
> Extracted from the codebase as of March 2026 to document the existing behavior and
> call out undefined or ambiguous scenarios.

## Overview

When a user runs an extension command (e.g. `azd myext --my-flag value -e dev`), flags
must be split between two consumers:

| Consumer | Needs | Examples |
|----------|-------|---------|
| **azd host** | Global flags that control host behavior | `--debug`, `--cwd`, `-e`, `--no-prompt` |
| **Extension process** | Extension-specific flags + positional args | `--my-flag value`, `--model gpt-4` |

Today there is **no explicit separator** (like `--`) between the two. Instead, azd uses
a combination of early pre-parsing and Cobra's `DisableFlagParsing` to handle this.

## Current Implementation

### Phase 1: Early Pre-Parsing (`ParseGlobalFlags`)

**File**: `cmd/auto_install.go` — `ParseGlobalFlags()`

Before Cobra builds the command tree, `ParseGlobalFlags()` extracts known azd global
flags from the raw `os.Args` using a pflag FlagSet with `ParseErrorsAllowlist{UnknownFlags: true}`.
Unknown flags (extension flags) are silently ignored.

**Flags parsed in this phase:**

| Flag | Short | Stored in `GlobalCommandOptions` |
|------|-------|----------------------------------|
| `--cwd` | `-C` | `Cwd` |
| `--debug` | — | `EnableDebugLogging` |
| `--no-prompt` | — | `NoPrompt` |
| `--trace-log-file` | — | *(read by telemetry system)* |
| `--trace-log-url` | — | *(read by telemetry system)* |

**Not parsed here**: `-e` / `--environment` is currently absent from `CreateGlobalFlagSet()`.
It is registered as a per-command flag on commands that need it (via `envFlag` binding),
not as a global flag. This is the root cause of [#7034](https://github.com/Azure/azure-dev/issues/7034).

The result is stored in a `GlobalCommandOptions` singleton registered in the IoC container.

**Agent detection**: If `--no-prompt` was not explicitly set, `ParseGlobalFlags` also
checks `agentdetect.IsRunningInAgent()` and auto-enables `NoPrompt` for AI coding agents.

### Phase 2: Cobra Command Registration

**File**: `cmd/extensions.go` — `bindExtension()`

Extension commands are registered with:

```go
cmd := &cobra.Command{
    Use:                lastPart,
    DisableFlagParsing: true,
}
```

`DisableFlagParsing: true` means Cobra will **not** parse any flags for these commands.
All tokens after the command name are passed to the action handler as raw `args`.

**Consequence**: Cobra's persistent flags (including `-e`/`--environment` if registered)
are not parsed for extension commands. Calls to `cmd.Flags().GetString("environment")`
silently return `""`.

### Phase 3: Extension Action Execution

**File**: `cmd/extensions.go` — `extensionAction.Run()`

The action handler builds `InvokeOptions` from two sources:

1. **Cobra persistent flags** (`cmd.Flags().Get*`) — for `debug`, `cwd`, `environment`
2. **`GlobalCommandOptions`** — for `NoPrompt` (because it includes agent detection)

```go
debugEnabled, _ := a.cmd.Flags().GetBool("debug")
cwd, _           := a.cmd.Flags().GetString("cwd")
envName, _       := a.cmd.Flags().GetString("environment")

options := &extensions.InvokeOptions{
    Args:        a.args,          // ALL raw args (extension + global flags)
    Debug:       debugEnabled,
    NoPrompt:    a.globalOptions.NoPrompt,
    Cwd:         cwd,
    Environment: envName,
}
```

**Problem**: Because `DisableFlagParsing: true`, `cmd.Flags().Get*` calls for `debug`,
`cwd`, and `environment` return zero values. Only `NoPrompt` works correctly because it
reads from `globalOptions` (populated in Phase 1).

### Phase 4: Environment Variable Propagation

**File**: `pkg/extensions/runner.go` — `Runner.Invoke()`

Global flag values from `InvokeOptions` are converted to environment variables before
spawning the extension process:

| `InvokeOptions` field | Environment variable | Condition |
|-----------------------|---------------------|-----------|
| `Debug` | `AZD_DEBUG=true` | if true |
| `NoPrompt` | `AZD_NO_PROMPT=true` | if true |
| `Cwd` | `AZD_CWD=<value>` | if non-empty |
| `Environment` | `AZD_ENVIRONMENT=<value>` | if non-empty |

The extension process also receives:
- `AZD_SERVER=<host>:<port>` — gRPC server address
- `AZD_ACCESS_TOKEN=<jwt>` — Authentication token
- `TRACEPARENT=<value>` — W3C trace context (if tracing is active)
- Environment variables from the resolved azd environment (`env.Environ()`)

### Phase 5: Middleware Extensions (Listener Path)

**File**: `cmd/middleware/extensions.go` — `ExtensionsMiddleware.Run()`

Lifecycle extensions (those with `lifecycle-events`, `service-target-provider`, or
`framework-service-provider` capabilities) use a different execution path:

- Started with fixed args `["listen"]`
- Read global flags from `m.options.Flags` (Cobra persistent flags from the *parent* command)
- Also have access to `m.globalOptions`

This path works for standard azd commands (like `azd deploy`) where Cobra *does* parse
persistent flags. The middleware invocation constructs `InvokeOptions` similarly.

## What Extensions Receive

When a user runs: `azd myext --my-flag value -e dev --debug`

The extension process gets:

| Channel | Content |
|---------|---------|
| **Command-line args** | `["--my-flag", "value", "-e", "dev", "--debug"]` — ALL tokens after the command name, including azd global flags |
| **Environment variables** | `AZD_DEBUG`, `AZD_NO_PROMPT`, `AZD_CWD`, `AZD_ENVIRONMENT` — set only if their `InvokeOptions` field is non-empty |
| **gRPC services** | Extension can call `EnvironmentService.GetCurrentEnvironment()` to get the resolved environment |

**Key observation**: Global azd flags appear in **both** the raw args and environment
variables. Extensions must decide which source to trust.

## Extension-Side Flag Parsing

Extensions are expected to:

1. **Read global azd flags from environment variables** (`AZD_DEBUG`, `AZD_NO_PROMPT`,
   `AZD_CWD`, `AZD_ENVIRONMENT`), not from their command-line args.
2. **Parse their own flags** from the command-line args they receive. The azd SDK provides
   helpers (`azdext` package) for this.
3. **Ignore azd global flags in their args** — these are artifacts of `DisableFlagParsing`
   and should not be interpreted by the extension.

However, this contract is **not formally documented or enforced**. Extensions using the
Go SDK with Cobra will likely re-parse all args including the azd global flags.

## Undefined Scenarios and Edge Cases

### 1. Flag Name Collisions

**Scenario**: Extension defines a flag with the same name as an azd global flag
(e.g., `--debug` meaning "debug output format" in the extension).

**Current behavior**: `ParseGlobalFlags` will consume `--debug` and set
`GlobalCommandOptions.EnableDebugLogging = true`. The extension will also see `--debug`
in its raw args and may interpret it differently.

**Impact**: Both azd and the extension act on the same flag with different semantics.
There is no way to say "this `--debug` is for the extension, not azd".

### 2. Global Flags Not Stripped From Extension Args

**Scenario**: User runs `azd myext -e dev --debug --my-flag value`.

**Current behavior**: Extension receives args `["-e", "dev", "--debug", "--my-flag", "value"]`.
The azd global flags are **not** removed from the args before passing to the extension.

**Impact**: If the extension uses a strict flag parser, unknown flags like `--debug` may
cause parse errors. Extensions must use permissive parsing or explicitly handle azd flags.

### 3. No `--` Separator Convention

**Scenario**: User wants to pass `--debug` to the extension but not to azd.

**Current behavior**: No mechanism exists. `ParseGlobalFlags` will always consume
recognized flags regardless of position.

**Potential design**: A `--` separator could be introduced:
`azd --debug myext -- --debug` (first `--debug` for azd, second for extension).
This is not implemented.

### 4. `-e` / `--environment` Not Pre-Parsed

**Scenario**: User runs `azd myext -e dev`.

**Current behavior** (main branch): `-e` is NOT in `CreateGlobalFlagSet()`, so
`ParseGlobalFlags` ignores it. `cmd.Flags().GetString("environment")` returns `""`
due to `DisableFlagParsing`. Result: `AZD_ENVIRONMENT` is never set, and `lazyEnv`
resolves to the default environment.

**Impact**: This is the bug described in [#7034](https://github.com/Azure/azure-dev/issues/7034).
The default environment's variables leak into the extension process.

### 5. Environment Variable Injection From Default Environment

**Scenario**: Extension commands inject `lazyEnv.Environ()` (the resolved azd
environment's key-value pairs) into the extension process's environment.

**Question**: Should extensions receive environment variables from the azd environment
at all, or should they exclusively use the gRPC `EnvironmentService`?

**Current behavior**: `extensionAction.Run()` calls `a.lazyEnv.GetValue()` and appends
all key-value pairs as process environment variables. This means the extension inherits
every value from the resolved azd environment.

**Trade-off**: Injecting env vars is convenient (extensions "just work" with standard
`os.Getenv`), but it creates an implicit coupling and makes it hard to distinguish
azd-injected variables from system environment variables.

### 6. `--cwd` Semantics

**Scenario**: User runs `azd myext -C ./subdir --my-flag value`.

**Current behavior**: `ParseGlobalFlags` captures `Cwd = "./subdir"`. The root command
changes the process working directory before the extension runs. `AZD_CWD=./subdir` is
set in the extension's environment.

**Question**: Should the extension receive the *original* cwd or the *resolved* cwd?
Currently it receives the raw flag value (potentially relative), but the process has
already `cd`'d into that directory.

### 7. Middleware vs Command Extension Flag Parity

**Scenario**: A lifecycle extension (middleware path) needs the `-e` environment name.

**Current behavior**: Middleware reads `m.options.Flags.GetString("environment")` from
Cobra persistent flags of the parent command (e.g., `azd deploy`). This works because
the parent command *does* parse flags.

**Gap**: The two execution paths (command extensions vs middleware extensions) have
different flag resolution strategies. Command extensions cannot rely on Cobra flags;
middleware extensions can.

### 8. Help Text for Extension Commands

**Scenario**: User runs `azd myext --help`.

**Current behavior**: Because `DisableFlagParsing: true`, Cobra does not intercept
`--help`. The `--help` flag is passed as an arg to the extension. The extension must
handle it. Cobra's auto-generated help (which would show global flags) is not displayed.

**Impact**: Users don't see a consistent help experience between azd commands and
extension commands. Global flags like `-e`, `--debug` are not listed in extension help.

## Recommendations

These are observations about gaps in the current design, not prescriptive changes:

1. **Document the flag contract**: Extensions should know definitively whether to read
   global flags from env vars or args. Today this is implicit.

2. **Consider stripping global flags from extension args**: If azd globals are communicated
   via env vars, they arguably should not appear in the extension's command-line args.
   This would eliminate collision issues. However, this would be a breaking change for
   extensions that currently parse `-e` from their args.

3. **Consider a `--` separator**: `azd [azd-flags] myext -- [ext-flags]` would make the
   boundary explicit. This is a convention used by many CLI tools (npm, cargo, kubectl).

4. **Unify flag resolution**: The divergence between command extensions (can't use Cobra
   flags) and middleware extensions (can use Cobra flags) is a source of bugs. A single
   resolution path through `GlobalCommandOptions` would be more robust.

5. **Document `AZD_ENVIRONMENT`, `AZD_DEBUG`, `AZD_NO_PROMPT`, `AZD_CWD`**: These
   environment variables are set for extension processes but not listed in
   `docs/environment-variables.md`.

## File Reference

| File | Role |
|------|------|
| `cmd/auto_install.go` | `CreateGlobalFlagSet()`, `ParseGlobalFlags()` |
| `cmd/extensions.go` | `bindExtension()` (DisableFlagParsing), `extensionAction.Run()` |
| `cmd/container.go` | `EnvFlag` DI resolver (environment name resolution chain) |
| `cmd/middleware/extensions.go` | Lifecycle extension invocation (middleware path) |
| `cmd/root.go` | Command tree construction, global flag registration |
| `pkg/extensions/runner.go` | `InvokeOptions`, env var propagation, process spawning |
| `internal/global_command_options.go` | `GlobalCommandOptions` struct |
| `internal/env_flag.go` | `EnvFlag` type, `EnvironmentNameFlagName` constant |
