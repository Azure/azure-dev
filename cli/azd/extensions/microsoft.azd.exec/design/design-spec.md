# azd exec — Design Specification

## Problem Statement

Running scripts and commands that need Azure Developer CLI environment variables
today requires manual workarounds: sourcing `.env` files, shell-specific `export`
commands, or language-specific environment loaders. There is no first-class way
to execute an arbitrary program with full azd context across platforms.

This creates friction for common development workflows:

- `python script.py` needs Azure credentials → manually export from `azd env get-values`
- `npm run dev` needs connection strings → source a `.env` file (shell-specific)
- `docker compose up` needs infra outputs → write a loader per language
- CI scripts need azd variables → pipe through `eval` or `source` (brittle)

These workarounds are shell-specific, error-prone, and create inconsistency
across templates, samples, and team environments.

**Prior art**: Issues [#391], [#1697], [#2336], [#4067], [#4384], [#7423].

[#391]: https://github.com/Azure/azure-dev/issues/391
[#1697]: https://github.com/Azure/azure-dev/issues/1697
[#2336]: https://github.com/Azure/azure-dev/issues/2336
[#4067]: https://github.com/Azure/azure-dev/issues/4067
[#4384]: https://github.com/Azure/azure-dev/issues/4384
[#7423]: https://github.com/Azure/azure-dev/issues/7423

## Design Goals

1. **Run any program with azd env** — `azd exec python script.py` should just work,
   with the same simplicity as `env VAR=val command` on Unix.
2. **Exact argv by default** — Arguments pass through to the target program exactly
   as specified. No shell interpolation, no quoting surprises, no argument swallowing.
3. **Cross-platform consistency** — The same `azd exec` invocation should behave
   the same on Linux, macOS, and Windows.
4. **Shell access when needed** — When users explicitly want shell features
   (expansion, pipes, globbing), there is a clear and intentional path.
5. **Child-process-only environment** — azd variables are injected into the child
   process only. The caller's shell is never modified.
6. **Exit code fidelity** — The child process exit code propagates to the caller
   for CI/CD pipeline integration.
7. **Zero configuration for common cases** — No flags required for the 80% use case
   of "run this program with my azd env."

## Non-Goals

- Replacing azd hooks (hooks are project-defined; exec is ad hoc)
- Providing a REPL / interactive shell launcher (`azd shell`)
- Merging or syncing variables into app-specific config files
- Persisting environment variables in the user's current shell
- Service-scoped execution (running a specific azd service locally)

## Execution Model

`azd exec` supports three execution modes, selected automatically based on the
shape of the invocation. The user doesn't need to think about modes — the
heuristic does the right thing for each case.

### Mode 1: Direct Process Execution (default for multi-arg)

```bash
azd exec python script.py --port 8000
azd exec npm run dev
azd exec docker compose up --build
```

**Semantics**: `exec.Command("python", "script.py", "--port", "8000")`

The first argument is the program. Remaining arguments are its argv. No shell is
involved. Arguments are passed exactly as specified — no expansion, no quoting
reinterpretation, no metacharacter processing.

This is the **default mode** when multiple arguments are provided without
`--shell`. It follows OS process semantics: the program receives exactly the
arguments the user typed.

**Why this is the default**: The most common use case is "run my tool with azd env."
Users typing `azd exec python script.py` expect Python to receive `script.py` as
its first argument. Wrapping in a shell would change that contract — a user who
types `azd exec python script.py --port 8000` would be surprised if `--port 8000`
became shell positional parameters instead of Python arguments.

### Mode 2: Shell Inline Execution

```bash
azd exec 'echo $AZURE_ENV_NAME'
azd exec --shell pwsh "Write-Host $env:AZURE_STORAGE_ACCOUNT"
azd exec 'cat config.json | jq .connectionString'
```

**Semantics**: `bash -c "echo $AZURE_ENV_NAME"`

A single string argument (or any invocation with `--shell`) is treated as a shell
expression. The shell processes the string — expanding variables, evaluating pipes,
interpreting glob patterns.

This mode is selected when:
- A single argument is provided (without `--shell`), OR
- The `--shell` flag is explicitly set

**When to use**: When you need shell features — variable expansion, pipes,
redirection, command chaining, or glob patterns.

### Mode 3: Script File Execution

```bash
azd exec ./setup.sh
azd exec ./build.ps1 -- --verbose
```

**Semantics**: `bash ./setup.sh` (shell detected from file extension)

When the first argument is a path to an existing file, it is executed as a script.
The shell is auto-detected from the file extension (`.sh` → bash, `.ps1` → pwsh,
`.cmd` → cmd) or can be overridden with `--shell`.

Arguments after `--` are passed to the script.

### Selection Heuristic

```
Input is an existing file path?
  YES → Mode 3 (script file execution)
  NO  → Multiple arguments AND no --shell flag?
    YES → Mode 1 (direct process execution)
    NO  → Mode 2 (shell inline execution)
```

The `--shell` flag always forces shell-mediated execution (Mode 2 or Mode 3).

## Shell Selection

When shell-mediated execution is used (Modes 2 and 3), the shell is determined by:

1. **Explicit `--shell` flag** — highest priority. Accepts: `bash`, `sh`, `zsh`,
   `pwsh`, `powershell`, `cmd`.
2. **File extension** (Mode 3 only) — `.sh`/`.bash` → bash, `.zsh` → zsh,
   `.ps1` → pwsh, `.cmd`/`.bat` → cmd.
3. **Platform default** — `bash` on Linux/macOS, `powershell` on Windows.

The shell whitelist is fixed. Unknown shell names are rejected at startup with a
clear error listing valid options.

**On the choice of platform defaults**: `bash` is the default on Unix because it
is the most widely available POSIX-superset shell. In environments where only
`sh` is available (e.g., Alpine containers), users can specify `--shell sh`.
The direct exec mode (Mode 1) sidesteps this entirely — no shell is needed.

## Environment Handling

### What the child process receives

The child process inherits the full parent process environment (`os.Environ()`),
which includes:

1. **System environment variables** — PATH, HOME, TERM, etc.
2. **azd environment values** — AZURE_ENV_NAME, AZURE_SUBSCRIPTION_ID, and all
   user-defined values from `azd env set` or infrastructure outputs.
3. **Resolved secrets** — Any `azvs://` or `@Microsoft.KeyVault(...)` references
   in the azd environment are resolved to their plaintext values.
4. **azd context flags** — `AZD_DEBUG` and `AZD_NO_PROMPT` are propagated when set.

### How environment loading works

The azd host (not the extension) handles environment loading:

1. Host reads the selected azd environment (`azd env select` or `--environment`)
2. Host resolves Key Vault secret references via `KeyVaultService.SecretFromKeyVaultReference()`
3. Host passes resolved values to the extension subprocess via `InvokeOptions.Env`
4. Extension inherits these as `os.Environ()` and passes them through to child processes

The extension itself performs no secret resolution. It receives an already-resolved
environment from the host and forwards it. This is the same model used by azd hooks.

### Secret materialization

Key Vault secret references (`akvs://vault/secret` and `@Microsoft.KeyVault(SecretUri=...)`)
are resolved by the azd host before the extension runs. The child process receives
plaintext secret values in its environment.

This behavior is:

- **Consistent with azd hooks** — hooks receive the same resolved environment
- **Not controllable by the extension** — resolution happens at the host level
- **Implicit** — there is no opt-in/opt-out flag today

**Trade-off acknowledged**: Automatic secret materialization is convenient but
carries risk — any program launched via `azd exec` can read all resolved secrets.
This is documented in the README security considerations. If an opt-in mechanism
is added in the future, it should be at the azd host level (affecting all
extensions and hooks), not per-extension.

### What `azd exec` does NOT do

- Does not modify the caller's shell environment
- Does not write `.env` files
- Does not merge with app-specific configuration
- Does not filter which environment variables are passed through

## Argument Passing

### Direct exec (Mode 1)

Arguments are passed as-is via `exec.Command(program, args...)`. No quoting,
escaping, or transformation. The OS process creation API handles separation.

```bash
azd exec python script.py --port 8000 --reload
# → exec.Command("python", "script.py", "--port", "8000", "--reload")
```

### Shell inline (Mode 2)

The script string is passed as a single argument to the shell's evaluation flag:

| Shell | Invocation |
|-------|-----------|
| bash/sh/zsh | `bash -c "script" --` |
| pwsh/powershell | `pwsh -Command "script"` |
| cmd | `cmd /c "script"` |

The `--` sentinel after `-c` for bash/sh/zsh prevents extra arguments from being
treated as shell options.

### Script file (Mode 3)

Script arguments (after `--`) are appended to the shell invocation:

| Shell | Invocation |
|-------|-----------|
| bash/sh/zsh | `bash script.sh arg1 arg2` |
| pwsh/powershell | `pwsh -File script.ps1 'arg1' 'arg2'` |
| cmd | `cmd /c "script.cmd" "arg1" "arg2"` |

PowerShell arguments are single-quote escaped. cmd.exe arguments are quoted with
metacharacter handling. bash/sh/zsh arguments pass through directly (the shell
handles tokenization).

## Cross-Platform Behavior

### Shell-specific quoting

Each shell has different quoting rules. The command builder handles this per-shell:

- **bash/sh/zsh**: Arguments appended directly (shell handles tokenization)
- **PowerShell**: Arguments wrapped in single quotes; embedded `'` escaped by doubling (`''`)
- **cmd.exe**: Arguments wrapped in double quotes when they contain spaces or
  metacharacters (`&|<>^%`); embedded `"` escaped by doubling (`""`)

### cmd.exe considerations

cmd.exe has inherent limitations:

- **`%VAR%` expansion in double quotes**: cmd.exe expands environment variable
  references even inside double quotes. This is an OS-level behavior that cannot
  be mitigated programmatically.
- **Control character stripping**: The command builder strips `\n`, `\r`, `\x00`,
  `\x0B`, `\x0C`, `\x1A`, and `\x1B` from arguments before passing to cmd.exe.
  These characters act as command separators or have special meaning to the cmd.exe
  parser.
- **Raw command line override**: On Windows, the Go `exec.Cmd.SysProcAttr.CmdLine`
  field is used to bypass Go's default `CommandLineToArgvW` escaping, which is
  incompatible with cmd.exe's quoting conventions.

**Recommendation**: Prefer `--shell pwsh` over `--shell cmd` for shell-mediated
execution on Windows. PowerShell has consistent quoting semantics.

### Direct exec avoids all shell issues

Mode 1 (direct exec) bypasses all shell-specific behavior. No quoting, no
expansion, no platform-specific metacharacter handling. This is why it is the
default for multi-argument invocations.

## Error Handling

### Structured error types

| Type | When | Example |
|------|------|---------|
| `ValidationError` | Invalid input | Empty script path, empty command |
| `ScriptNotFoundError` | File doesn't exist | `azd exec nonexistent.sh` |
| `InvalidShellError` | Unknown shell name | `azd exec --shell fish ...` |
| `ExecutionError` | Non-zero exit code | Script returns exit code 1 |

### Exit code propagation

When the child process exits with a non-zero code, `azd exec` exits with the same
code. This enables CI/CD integration:

```bash
azd exec python -m pytest || echo "Tests failed"
```

The `main.go` entry point intercepts `ExecutionError` and calls `os.Exit(code)`
directly, ensuring the azd process reflects the child's exit status.

### Non-exit errors

Errors that are not exit codes (e.g., program not found, permission denied) are
wrapped with context and returned as Go errors. The error message identifies
whether the failure was in direct exec, shell inline, or script file mode.

## Security Considerations

### Command injection

- **Direct exec mode**: No shell involvement → no injection surface. Arguments
  are passed via OS process creation, not parsed by a shell.
- **Shell inline mode**: The user's input IS the shell command. This is by design —
  the user explicitly opted into shell execution. The extension does not attempt
  to sanitize shell expressions.
- **Script file mode**: The script path is validated (must exist, must be a file),
  but the script's contents are not inspected.

### Argument sanitization (cmd.exe)

The `quoteCmdArg` function strips control characters that cmd.exe interprets as
command separators (`\n`, `\r`, `\x00`, `\x0B`, `\x0C`, `\x1A`, `\x1B`) and
quotes arguments containing metacharacters. This prevents argument injection
through embedded newlines or metacharacters when using `--shell cmd`.

### Debug logging

Debug output (`AZD_DEBUG=true`) uses `%q` formatting for all paths and arguments,
ensuring control characters are rendered as escape sequences rather than
interpreted by the terminal.

### Trust model

`azd exec` executes arbitrary programs by design. The trust boundary is the same
as typing commands in a terminal — the user is responsible for what they execute.
The extension adds no elevation, no sandbox, and no restriction beyond what the
OS provides.

## CLI Interface

```
azd exec [command] [args...] | [script-file] [-- script-args...]

Flags:
  -s, --shell string       Shell to use (bash, sh, zsh, pwsh, powershell, cmd)
  -i, --interactive        Connect stdin for interactive scripts
      --help               Show help

Global flags (inherited from azd):
      --debug              Enable debug logging
      --no-prompt          Disable interactive prompts
```

### Flag parsing

- `--` separates azd exec flags from script/command arguments
- Unknown flags after the first positional argument are passed through
  (`UnknownFlags = true`, `SetInterspersed(false)`)
- This means `azd exec python --version` passes `--version` to Python,
  not to azd exec

## Architecture

```
main.go                                    Entry point (exit code propagation)
├── internal/cmd/root.go                   CLI command definition + mode heuristic
├── internal/executor/
│   ├── executor.go                        Three execution modes
│   ├── command_builder.go                 Shell-specific command construction
│   ├── command_builder_windows.go         Windows CmdLine override
│   ├── command_builder_notwindows.go      No-op on non-Windows
│   └── errors.go                          Structured error types
└── internal/shellutil/shellutil.go        Shell detection + validation
```

3 internal packages. No circular dependencies. No external dependencies beyond
the azd SDK (`pkg/azdext`) and cobra.

## Alternatives Considered

### Core command vs extension

A core `azd env exec` command was proposed in [#7423]. The extension approach
was chosen for:

- **Iteration speed** — Extensions ship independently from the azd release train
- **Binary size** — No impact on the core azd binary
- **Scope isolation** — Execution engine doesn't need to live in the core command tree

Trade-off: Extensions require `azd extension install` — less discoverable than
a built-in command. The capability can be promoted to core if adoption justifies it.

### Shell-only model

An earlier design used shell wrapping for all invocations. This caused a subtle
but critical bug: `azd exec python script.py` would produce
`bash -c "python" -- script.py`, where `script.py` becomes `$1` in bash (a
positional parameter to bash, not an argument to Python). Python would run with
zero arguments.

Direct exec (Mode 1) was added specifically to fix this. The current model gives
users OS process semantics by default, with shell access available when requested.

### Separate commands (exec vs shell)

The idea of splitting into two commands — `azd exec` for OS-level process
execution and `azd shell` for shell-mediated execution — was considered. A single
command with an automatic heuristic was chosen because:

- Users shouldn't need to think about the distinction for common cases
- The heuristic is deterministic and predictable
- The `--shell` flag provides an explicit escape hatch

### Opt-in secret resolution

Making Key Vault secret resolution opt-in (via a flag) was considered. This
would need to be implemented at the azd host level, not in the extension, because
the host resolves secrets before extensions start. The current behavior is
consistent with azd hooks. This remains open for future host-level changes.

## Testing

94.3% statement coverage across all packages:

| Package | Coverage | Test count |
|---------|----------|------------|
| cmd | 91.4% | 7 |
| executor | 96.5% | 11 |
| shellutil | 93.3% | 5 |

Tests use real command execution (not mocks) for fidelity. Platform-specific
behavior is handled via build tags and runtime detection. Table-driven test
patterns throughout.

## References

- [PR #7400](https://github.com/Azure/azure-dev/pull/7400) — Implementation
- [Issue #7520](https://github.com/Azure/azure-dev/issues/7520) — Tracking issue
- [Issue #7423](https://github.com/Azure/azure-dev/issues/7423) — Core command proposal
- [Issue #4384](https://github.com/Azure/azure-dev/issues/4384) — Original env loading request
