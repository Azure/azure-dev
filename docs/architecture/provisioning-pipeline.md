# Provisioning Pipeline

How infrastructure provisioning works in azd.

## Overview

The provisioning pipeline creates or updates Azure infrastructure from Infrastructure as Code (IaC) templates. It is triggered by `azd provision` or as part of `azd up`.

## Pipeline Stages

```text
IaC Templates → Compilation → Preflight Checks → Deployment → State Tracking
```

### 1. Template Compilation

azd supports two IaC providers:

- **Bicep** (default) — Compiled to ARM templates via the Bicep CLI
- **Terraform** — Planned and applied via the Terraform CLI

For Bicep, azd can generate a fully resolved deployment snapshot using `bicep snapshot`, which evaluates expressions, applies conditions, expands copy loops, and flattens nested deployments.

### 2. Preflight Checks

Client-side validation that runs after compilation but before deployment:

- **Role assignment permissions** — Checks if the user has required RBAC roles
- **Resource conflicts** — Detects soft-deleted resources that would conflict
- **Quota availability** — Validates resource quotas (e.g., AI model capacity)
- **Location support** — Verifies resource types are available in the target region

Preflight checks are pluggable — new checks can be added via `AddCheck()`.

**UX behavior:**

- No issues → proceed silently
- Warnings only → display and prompt to continue
- Errors → display and abort

Disable with: `azd config set provision.preflight off`

### 3. Deployment

The compiled template is submitted to Azure Resource Manager (ARM) for deployment. azd monitors the deployment progress and displays status updates.

### 4. Provision State

After deployment, azd stores a hash of the template and parameters. On subsequent runs:

- If the hash matches → provisioning is skipped (no changes detected)
- If the hash differs → provisioning proceeds
- Use `--no-state` to force provisioning regardless

> **Note:** Provision state does not detect out-of-band changes made via the Azure Portal or other tools.

## Infrastructure Providers

### Bicep

- Default provider for azd projects
- Templates in `infra/` directory (convention: `main.bicep`)
- Compiled to ARM templates before deployment
- Supports modules, parameters, and outputs

### Terraform

- Alternative provider, configured in `azure.yaml`
- Uses standard Terraform workflow (init, plan, apply)
- State managed by Terraform (not azd provision state)

## Detailed Reference

- [Provision State](../../cli/azd/docs/provision-state.md) — Hash-based change detection
- [Local Preflight Validation](../../cli/azd/docs/design/local-preflight-validation.md) — Preflight check design
