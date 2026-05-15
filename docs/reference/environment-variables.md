# Environment Variables

Environment variables that configure Azure Developer CLI behavior. These can be set in your shell, CI/CD pipeline, or `.env` files.

## Feature Flags

| Variable | Description |
|---|---|
| `AZD_ALPHA_ENABLE_<NAME>` | Enable a specific alpha feature |

## Configuration

| Variable | Description |
|---|---|
| `AZD_CONFIG_DIR` | Override the default user configuration directory |
| `AZURE_DEV_COLLECT_TELEMETRY` | Set to `no` to disable telemetry collection |

## Behavior

| Variable | Description |
|---|---|
| `AZD_DEMO_MODE` | When set, hides PII (e.g., subscription IDs) in output |
| `AZD_FORCE_TTY` | Force terminal detection mode (`true` forces TTY mode, `false` forces non-TTY mode) |
| `AZD_IN_CLOUDSHELL` | Indicates azd is running in Azure Cloud Shell |
| `AZD_SKIP_UPDATE_CHECK` | Skip the periodic update availability check |
| `AZD_DEBUG_LOG` | Enable debug file logging |

## Tool Path Overrides

Override the path to external tools that azd invokes:

| Variable | Description |
|---|---|
| `AZD_BICEP_TOOL_PATH` | Path to the Bicep CLI binary |
| `AZD_GH_TOOL_PATH` | Path to the GitHub CLI binary |
| `AZD_PACK_TOOL_PATH` | Path to the Cloud Native Buildpacks (`pack`) binary |

## Build Configuration

| Variable | Description |
|---|---|
| `AZD_BUILDER_IMAGE` | Builder image for Dockerfile-less container builds |

## Provisioning

| Variable | Description |
|---|---|
| `AZD_DEPLOYMENT_ID_FILE` | Absolute path to a file where `azd` writes ARM deployment resource IDs in NDJSON format (one JSON line per layer) during Bicep provisioning. The file is truncated at the start of each run; each layer appends `{"deploymentId":"...","layer":"<name>"}`. Enables external tooling (e.g., VS Code extension) to track all in-flight deployments without scraping console output. See [cli/azd/docs/environment-variables.md](../../cli/azd/docs/environment-variables.md) for the full contract. |

## IDE Integration

Set by IDE hosts (VS Code, Visual Studio) when spawning azd as a subprocess. Users do not set these manually.

| Variable | Description |
|---|---|
| `AZD_AUTH_ENDPOINT` | Authentication endpoint URL set by IDE hosts for integrated authentication |
| `AZD_AUTH_KEY` | Authentication key set by IDE hosts for integrated authentication |
| `AZD_AUTH_CERT` | Authentication certificate/TLS trust configuration set by IDE hosts |

For details on the external authentication protocol, see [cli/azd/docs/external-authentication.md](../../cli/azd/docs/external-authentication.md).

## See Also

For the full reference with implementation details, see [cli/azd/docs/environment-variables.md](../../cli/azd/docs/environment-variables.md).
