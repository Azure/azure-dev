# Extension Authoring Models: SDK-Managed vs Self-Managed

## Overview

When building an azd extension, there are two authoring models that determine how
the extension handles command-line flags, global state propagation, and lifecycle
boilerplate. Each model has distinct tradeoffs around convenience, control, and
language support.

This document names the two models, explains what each provides, and helps
extension authors choose the right one.

## Background: How azd Invokes Extensions

Before choosing a model, it helps to understand what azd does when it invokes an
extension:

1. **Pre-parses selected global flags** — `ParseGlobalFlags()` extracts `--debug`,
   `--cwd`, and `--no-prompt` from the full argument list and binds them into
   `GlobalCommandOptions`. Trace-related flags are parsed separately by the telemetry
   system. Note: `-e/--environment` is **not** pre-parsed — it is a per-command flag.

2. **Passes raw args through** — The extension command has
   `DisableFlagParsing: true`, so Cobra treats all remaining arguments
   (including flags like `--debug`, `-e myenv`) as raw args and passes them
   to the extension binary unchanged.

3. **Sets environment variables** — azd propagates global flag values via
   `AZD_DEBUG`, `AZD_NO_PROMPT`, `AZD_CWD`, `AZD_ENVIRONMENT`,
   `AZD_SERVER`, and `AZD_ACCESS_TOKEN`.

4. **Execs the binary** — No handshake, no flag validation, no protocol check.
   The extension receives raw args and env vars, and azd reads the exit code.

This means every extension receives the **same raw argv** and **same env vars**
regardless of which model it uses. The difference is how the extension
*interprets* them.

## Model 1: SDK-Managed

**Language support:** Go only

**Entry point:** `azdext.NewExtensionRootCommand()` + optionally `azdext.Run()`

**Introduced in:** [PR #6856](https://github.com/Azure/azure-dev/pull/6856)
(Feb 2026), ~13 months after the extension framework was created.

### What it provides

`NewExtensionRootCommand()` creates a Cobra root command that automatically:

- Registers azd's global flags as persistent flags on the root command:
  `--debug`, `--no-prompt`, `--cwd/-C`, `--environment/-e`, `--output/-o`,
  `--trace-log-file`, `--trace-log-url`
- Falls back to `AZD_*` environment variables when flags are not explicitly set
- Applies `--cwd` (changes working directory)
- Extracts OpenTelemetry trace context from `TRACEPARENT`/`TRACESTATE`
- Injects the gRPC access token into the command context

`azdext.Run()` adds:

- `FORCE_COLOR` environment variable handling
- Structured error reporting back to azd via gRPC (`ReportError`)
- User-friendly error and suggestion display

### What this model makes easier

- **Global flag compatibility** — The extension's Cobra parser won't fail on
  `--debug`, `-e`, or other azd flags because they are pre-registered.
  Without this, receiving azd flags in the raw args causes "unknown flag" errors.
- **Consistent behavior** — The extension honors the same global flags as core
  azd commands, providing a seamless user experience.
- **Error reporting** — `azdext.Run()` sends structured errors back to the azd
  host via gRPC, enabling richer error display and troubleshooting.

### What can be hard to anticipate

- **Opaque flag registration** — `NewExtensionRootCommand()` registers flags
  that are not visible in the extension's own code. An extension author looking
  at their `main.go` sees a single function call but gets 9+ persistent flags
  implicitly added to their root command.
- **Reserved flag collisions** — If a subcommand defines a flag that collides
  with these invisible registrations (e.g., `-o` for `--organization`), Cobra
  may silently shadow the root flag or produce confusing behavior.
- **Future breakage** — The reserved flag list could grow in a new framework
  version, breaking an extension that was previously fine.

### Example

```go
func main() {
    rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
        Name:    "myext",
        Version: "0.1.0",
        Short:   "My extension",
    })

    rootCmd.AddCommand(newMySubcommand(extCtx))

    azdext.Run(rootCmd)
}
```

### What the extension author is responsible for

- Subcommand and subcommand-flag definitions only — global flags are handled
  by the framework
- Avoiding flag names that collide with reserved global flags

### Flags registered by the framework

These flags are registered by `NewExtensionRootCommand()` as persistent flags.
Extension subcommands **must not** reuse these names:

| Long Name | Short | Purpose |
|-----------|-------|---------|
| `environment` | `e` | Selects the azd environment |
| `cwd` | `C` | Sets the working directory |
| `debug` | — | Enables debug logging |
| `no-prompt` | — | Non-interactive mode |
| `output` | `o` | Output format (json, table, none) |
| `help` | `h` | Command help (Cobra built-in) |
| `trace-log-file` | — | Diagnostics trace file |
| `trace-log-url` | — | OpenTelemetry trace endpoint |

### Summary of tradeoffs

| Advantage | Consideration |
|-----------|---------------|
| Zero boilerplate for global flags, OTel, error reporting | Opaque dependency — the framework registers flags not visible in the extension's own code |
| Consistent behavior across azd and extension | Reserved flag list can grow in future framework versions, potentially breaking the extension |
| Structured error reporting via gRPC | Coupled to the `azdext` package version in `cli/azd` |

## Model 2: Self-Managed

**Language support:** Any (Go, Python, JavaScript, .NET, or any language)

**Entry point:** The extension creates its own CLI root command using any
framework (Cobra, Typer, Commander, System.CommandLine, etc.) and optionally
calls `azdext.NewContext()` (Go) for gRPC access.

### What it provides

- Full control over the flag namespace — the extension defines exactly which
  flags exist
- Freedom to use any CLI framework in any language
- No implicit dependencies on framework-registered flags

### Example (Go)

```go
func main() {
    ctx := azdext.NewContext()
    rootCmd := &cobra.Command{Use: "myext"}
    rootCmd.AddCommand(newMySubcommand())

    if err := rootCmd.ExecuteContext(ctx); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}
```

### Example (Python)

```python
import typer
app = typer.Typer()

@app.command()
def create(name: str = typer.Option(...)):
    # Extension logic
    pass

if __name__ == "__main__":
    app()
```

### What the extension author is responsible for

- **Handling azd global flags in raw args:** azd passes raw args including any
  global flags the user specified (e.g., `--debug`, `-e myenv`). If the
  extension's parser doesn't register those flags, it will fail with
  "unknown flag" errors. Options:
  - Register the same global flags the extension expects to receive
  - Configure the parser to ignore unknown flags
  - Use `cobra.ArbitraryArgs` or equivalent
- **Reading `AZD_*` environment variables:** Global flag values are available
  as `AZD_DEBUG`, `AZD_NO_PROMPT`, `AZD_CWD`, `AZD_ENVIRONMENT`. The
  extension must read these manually if it needs them.
- **gRPC access token injection:** Call `azdext.NewContext()` (Go) or read
  `AZD_ACCESS_TOKEN` and `AZD_SERVER` directly
- **Error reporting:** Handle and display errors locally — no automatic gRPC
  error reporting to azd
- **OTel trace propagation:** Read `TRACEPARENT`/`TRACESTATE` env vars
  manually if tracing is needed

### Summary of tradeoffs

| Advantage | Consideration |
|-----------|---------------|
| Full control over flag namespace | Must handle unknown flags from azd (or they cause parse errors) |
| Works with any language | Must implement `AZD_*` env var reading manually |
| No opaque framework dependencies | No structured error reporting to azd |
| Immune to reserved flag list changes | No automatic OTel or access token setup |

## Decision Guide

### Use SDK-Managed when:

- The extension is written in **Go**
- The extension benefits from automatic handling of `--debug`, `--cwd`,
  `-e`, `--output`, `--no-prompt`
- Structured error reporting to the azd host is important
- You want the framework to handle OTel trace propagation

### Use Self-Managed when:

- The extension is written in **Python, JavaScript, .NET, or another
  non-Go language** (this is the only option)
- The extension needs **full control** over its flag namespace
- The extension does not use azd environments and wants to avoid
  implicit flag registrations
- The extension is a lightweight wrapper or script that doesn't need
  the full azd SDK surface

### Hybrid approach

It is possible to use `azdext.NewContext()` for gRPC access while wiring
your own Cobra root command. This gives you gRPC connectivity without
the implicit flag registrations. This is the pattern used by most existing
in-repo extensions today.

## Current state (March 2026)

| Aspect | SDK-Managed | Self-Managed |
|--------|-------------|--------------|
| In-repo extensions using this model | 3 of 9 | 6 of 9 |
| Go scaffold template (`azd x init`) | Not used | Used |
| Non-Go templates | N/A | Used |
| Language support | Go only | Any |

## Related

- [Extension Framework](extension-framework.md) — full extension development
  guide
- [Extensions Style Guide](extensions-style-guide.md) — design guidelines
- [PR #6856](https://github.com/Azure/azure-dev/pull/6856) — introduced
  `NewExtensionRootCommand()` and `azdext.Run()`
