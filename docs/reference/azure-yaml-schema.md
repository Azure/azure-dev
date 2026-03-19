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

## Service Properties

| Property | Type | Description |
|---|---|---|
| `project` | string | **Required.** Relative path to the service source directory |
| `language` | string | Service language (`dotnet`, `py`, `js`, `ts`, `java`, `docker`) |
| `host` | string | Hosting target (`appservice`, `containerapp`, `function`, `staticwebapp`, `aks`, `ai`) |
| `module` | string | Bicep module path for the service's infrastructure |
| `hooks` | map | Service-level lifecycle hooks |
| `docker` | object | Docker build configuration |

## Hooks

Hooks run user-defined scripts at lifecycle points:

```yaml
hooks:
  preprovision:
    shell: sh
    run: ./scripts/setup.sh
  postdeploy:
    shell: sh
    run: ./scripts/smoke-test.sh
```

Available hook points: `prerestore`, `postrestore`, `preprovision`, `postprovision`, `predeploy`, `postdeploy`, `prepackage`, `postpackage`, `predown`, `postdown`.

## JSON Schema

The full JSON schema for `azure.yaml` is maintained in the [schemas/](../../schemas/) directory and published for editor validation.

## See Also

- [azure.yaml JSON Schema](../../schemas/) — Machine-readable schema definition
- [Feature Status](feature-status.md) — Which languages and hosts are stable/beta/alpha
