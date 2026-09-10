# Provisioning Pipeline

How infrastructure provisioning works in azd.

## Overview

The provisioning pipeline creates or updates Azure infrastructure from Infrastructure as Code (IaC) templates. It is triggered by `azd provision` or as part of `azd up`.

## Pipeline Stages

```text
IaC Templates → Compilation → Provision Validation → Deployment → State Tracking
```

### 1. Template Compilation

azd supports two IaC providers:

- **Bicep** (default) — Compiled to ARM templates via the Bicep CLI
- **Terraform** — Planned and applied via the Terraform CLI

For Bicep, azd can generate a fully resolved deployment snapshot using `bicep snapshot`, which evaluates expressions, applies conditions, expands copy loops, and flattens nested deployments.

### 2. Provision Validation

Client-side (local) validation that runs after compilation but before deployment:

- **Role assignment permissions** — Checks if the user has required RBAC roles for role assignments in the template
- **AI model quota** — Validates that sufficient AI model capacity is available in the target region
- **Reserved resource names** — Warns when predicted resource names collide with Azure reserved or restricted names

The validation framework is pluggable — new checks can be added via `AddCheck()`.

**UX behavior:**

- No issues → proceed silently
- Warnings only → display and prompt to continue
- Errors → display and cancel

Disable local validation with: `azd config set validation.provision off`.
(The separate `azd config set provision.preflight off` disables only the server-side ARM preflight call.)

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
- [Provision Validation](../../cli/azd/docs/design/provision-validation.md) — Provision validation design
