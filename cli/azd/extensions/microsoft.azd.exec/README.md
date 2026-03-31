# microsoft.azd.exec

Execute scripts and commands with Azure Developer CLI context and environment variables.

## Installation

```bash
azd extension install microsoft.azd.exec
```

## Usage

```bash
# Execute a script file with azd environment
azd exec ./setup.sh

# Inline command with environment variables
azd exec 'echo $AZURE_ENV_NAME'

# Specify shell explicitly
azd exec --shell pwsh "Write-Host 'Hello'"

# Pass arguments to script
azd exec ./build.sh -- --verbose

# Interactive mode
azd exec -i ./interactive-setup.sh
```

## Features

- **Shell auto-detection**: Detects shell from file extension
- **Cross-platform**: Supports bash, sh, zsh, pwsh, powershell, and cmd
- **Interactive mode**: Connect stdin for scripts requiring user input
- **Environment loading**: Inherits azd environment variables, including any Key Vault secrets resolved by azd core
- **Exit code propagation**: Child process exit codes forwarded for CI/CD pipelines

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
