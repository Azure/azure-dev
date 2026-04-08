# microsoft.azd.exec

Execute scripts and commands with Azure Developer CLI context and environment variables.

## Installation

```bash
azd extension install microsoft.azd.exec
```

## Usage

```bash
# Run a command directly with azd environment (exact argv, no shell wrapping)
azd exec python script.py
azd exec npm run dev
azd exec -- python app.py --port 8000 --reload
azd exec docker compose up --build

# Execute a script file with azd environment
azd exec ./setup.sh
azd exec ./build.sh -- --verbose

# Inline shell command (single quoted argument uses shell)
azd exec 'echo $AZURE_ENV_NAME'
azd exec --shell pwsh "Write-Host $env:AZURE_ENV_NAME"

# Interactive mode
azd exec -i ./interactive-setup.sh
```

## Execution Modes

| Invocation | Mode | How it works |
|---|---|---|
| `azd exec python script.py` | **Direct exec** | `exec.Command("python", "script.py")` — exact argv, no shell |
| `azd exec 'echo $VAR'` | **Shell inline** | `bash -c "echo $VAR"` — shell expansion available |
| `azd exec ./setup.sh` | **Script file** | `bash ./setup.sh` — shell detected from extension |
| `azd exec --shell pwsh "cmd"` | **Shell inline** | `pwsh -Command "cmd"` — explicit shell |

**Heuristic**: Multiple arguments without `--shell` → direct process exec.
Single quoted argument or explicit `--shell` → shell inline execution.
File path → script file execution with auto-detected or explicit shell.

## Features

- **Direct process execution**: Run programs with exact argv semantics (no shell wrapping)
- **Shell auto-detection**: Detects shell from file extension for script files
- **Cross-platform**: Supports bash, sh, zsh, pwsh, powershell, and cmd
- **Interactive mode**: Connect stdin for scripts requiring user input (`-i`)
- **Environment loading**: Inherits azd environment variables, including any Key Vault secrets resolved by azd core
- **Exit code propagation**: Child process exit codes forwarded for CI/CD pipelines

## Security Considerations

- **Environment inheritance**: Child processes receive all parent environment variables,
  including Azure tokens and any Key Vault secrets resolved by azd. Be cautious when
  executing untrusted scripts.
- **cmd.exe quoting**: On Windows, `cmd.exe` expands `%VAR%` patterns even inside double
  quotes. This is an inherent cmd.exe behavior that cannot be fully mitigated. Prefer
  PowerShell (`--shell pwsh`) for untrusted arguments on Windows.
- **Script execution**: This extension runs arbitrary scripts by design. Only execute
  scripts you trust.

## Development

```bash
cd cli/azd/extensions/microsoft.azd.exec

# Build
go build ./...

# Test
go test ./...

# Build for all platforms
EXTENSION_ID=microsoft.azd.exec EXTENSION_VERSION=0.5.0 ./build.sh
```
