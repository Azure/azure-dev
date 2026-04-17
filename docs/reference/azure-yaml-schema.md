# azure.yaml Schema

The `azure.yaml` file is the project configuration file for the Azure Developer CLI. It lives at the root of your project and declares services, infrastructure, and lifecycle hooks.

## Overview

```yaml
name: my-project
metadata:
  template: my-org/my-template
services:
  web:
    project: ./src/web
    language: js
    host: appservice
  api:
    project: ./src/api
    language: python
    host: containerapp
```

## Top-Level Properties

| Property | Type | Description |
|---|---|---|
| `name` | string | **Required.** Project name used for resource naming |
| `metadata` | object | Template metadata (origin template, version) |
| `resourceGroup` | string | Override the default resource group name |
| `services` | map | Service definitions keyed by service name |
| `pipeline` | object | CI/CD pipeline configuration |
| `hooks` | map | Project-level lifecycle hooks |
| `infra` | object | Infrastructure provider configuration |
| `state` | object | Remote state backend configuration |
| `resources` | map | Azure resource definitions |
| `requiredVersions` | object | Version constraints for azd and extensions |
| `platform` | object | Platform-specific configuration |
| `workflows` | object | Workflow configuration |
| `cloud` | object | Cloud environment configuration |

## Service Properties

| Property | Type | Description |
|---|---|---|
| `project` | string | Relative path to the service source directory |
| `language` | string | Service language (`dotnet`, `csharp`, `fsharp`, `py`, `js`, `ts`, `java`, `docker`, `custom`) |
| `host` | string | **Required.** Hosting target (`appservice`, `containerapp`, `function`, `staticwebapp`, `aks`, etc.) |
| `module` | string | Bicep module path for the service's infrastructure |
| `hooks` | map | Service-level lifecycle hooks |
| `docker` | object | Docker build configuration |
| `image` | string | Container image (alternative to `project` for pre-built images) |
| `dist` | string | Path to pre-built distribution directory |
| `resourceName` | string | Override the Azure resource name |
| `k8s` | object | Kubernetes-specific configuration |
| `config` | object | Service-specific configuration |
| `resourceGroup` | string | Override the resource group for this service |
| `apiVersion` | string | API version for the hosting target |
| `env` | map | Environment variables passed to the service |
| `uses` | list | Service dependencies |
| `remoteBuild` | boolean | Enable remote build (e.g., for Azure Functions) |

## Hooks

Hooks run user-defined scripts at lifecycle points:

```yaml
hooks:
  preprovision:
    kind: sh
    run: ./scripts/setup.sh
  postdeploy:
    kind: sh
    run: ./scripts/smoke-test.sh
```

Available hook points (each supports `pre` and `post` prefixes):

- **Command hooks (project-level):** `provision`, `deploy`, `down`, `up`, `restore`, `package`, `infracreate`, `infradelete`, `publish`
- **Service lifecycle hooks (service-level):** `restore`, `build`, `package`, `publish`, `deploy`

For example, `preprovision` runs before provisioning, `postdeploy` runs after deployment. Service-level hooks are defined under a service's `hooks` section in `azure.yaml` and apply only to that service.

## JSON Schema

The full JSON schema for `azure.yaml` is maintained in the [schemas/](../../schemas/) directory and published for editor validation.

## See Also

- [azure.yaml JSON Schema](../../schemas/) — Machine-readable schema definition
- [Feature Status](feature-status.md) — Which languages and hosts are stable/beta/alpha
