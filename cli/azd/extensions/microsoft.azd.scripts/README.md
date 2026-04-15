# Script Provisioning Provider (`microsoft.azd.scripts`)

A first-party azd extension that enables shell script-based provisioning and teardown workflows.

## Overview

The Script Provisioning Provider allows teams with existing script-based infrastructure workflows to adopt azd without rewriting their provisioning logic. Configure bash or PowerShell scripts as your provisioning provider in `azure.yaml`, and azd will execute them during `azd provision` and `azd down`.

## Installation

```bash
azd ext install microsoft.azd.scripts
```

## Configuration

Configure the extension in your `azure.yaml`:

```yaml
infra:
  provider: scripts
  config:
    provision:
      - kind: sh
        run: scripts/setup.sh
        name: Setup Infrastructure
        env:
          AZURE_LOCATION: ${AZURE_LOCATION}
          RESOURCE_GROUP: rg-${AZURE_ENV_NAME}
      - kind: pwsh
        run: scripts/configure-app.ps1
        env:
          APP_NAME: myapp-${AZURE_ENV_NAME}
    destroy:
      - kind: sh
        run: scripts/teardown.sh
        env:
          RESOURCE_GROUP: rg-${AZURE_ENV_NAME}
```

### Script Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `run` | string | Yes | Relative path to the script file |
| `kind` | string | No | Script type: `sh` or `pwsh` (auto-detected from extension) |
| `name` | string | No | Display name for progress reporting |
| `env` | map | No | Environment variables with `${EXPRESSION}` substitution |
| `secrets` | map | No | Secret references (`akvs://vault/secret`) |
| `continueOnError` | bool | No | Continue execution on script failure |
| `windows` | object | No | Windows-specific overrides |
| `posix` | object | No | Linux/macOS-specific overrides |

### Environment Variable Resolution

Variables are resolved using a 4-layer priority model:

1. **OS environment** (lowest priority)
2. **azd environment** values (`.env` + prior script outputs)
3. **`env` map** values (`${EXPRESSION}` substitution)
4. **`secrets` map** values (highest priority)

### Output Collection

Scripts can produce outputs by writing an `outputs.json` file alongside the script:

```json
{
  "WEBSITE_URL": { "type": "string", "value": "https://myapp.azurewebsites.net" },
  "RESOURCE_GROUP": { "type": "string", "value": "rg-myapp-dev" }
}
```

Outputs are merged across scripts and stored in the azd environment.
