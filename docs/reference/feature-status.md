# Feature Status

Current maturity status of Azure Developer CLI features. See [Feature Stages](../concepts/feature-stages.md) for what each stage means.

## Commands

| Feature | Stage |
|---|---|
| `add` | Beta |
| `auth` | Stable |
| `config` | Stable |
| `deploy` | Stable |
| `down` | Stable |
| `env` | Stable |
| `help` | Stable |
| `infra generate` | Beta |
| `init` | Stable |
| `monitor` | Beta |
| `package` | Beta |
| `pipeline` | Beta |
| `provision` | Stable |
| `restore` | Beta |
| `show` | Stable |
| `template` | Beta |
| `up` | Stable |
| `update` | Beta |
| `version` | Stable |

## Languages

| Language | Stage |
|---|---|
| Python | Stable |
| JavaScript / TypeScript | Stable |
| Java | Stable |
| .NET (C#) | Stable |

## Infrastructure as Code

| Provider | Stage |
|---|---|
| Bicep | Stable |
| Terraform | Beta |

## Hosting Targets

| Host | Stage |
|---|---|
| Azure App Service | Stable |
| Azure Static Web Apps | Stable |
| Azure Container Apps | Stable |
| Azure Functions | Stable |
| Azure Kubernetes Service (AKS) | Beta |
| Azure AI | Beta |

## Clients

| Client | Stage |
|---|---|
| VS Code Extension | Beta |
| Codespaces | Beta |
| Cloud Shell | Beta |
| Visual Studio | Alpha |

## CI/CD

| Platform | Stage |
|---|---|
| GitHub Actions | Stable |
| Azure Pipelines | Stable |

## Advanced Features

| Feature | Stage |
|---|---|
| Resource Group Deployments | Beta |
| Layered Provisioning | Beta |
| Deployment Preview (`deploy --preview`) | Beta |
| Deployment Stacks | Alpha |
| Extensions | Alpha |

## Extension gRPC Contracts

| Capability | Contract stage | Stable since | Beta status |
|---|---|---|---|
| Extension usage telemetry (`TelemetryService.ReportUsage`) | Stable v1 | azd 1.36.0 | Deprecated; wire route retained |
| Current Azure principal (`AccountService.GetCurrentPrincipal`) | Stable v1 | azd 1.36.0 | Deprecated; wire route retained |

These are contract graduations, not a change to the overall Extensions feature stage
or telemetry eligibility/privacy policy. Stable consumers must declare
`requiredAzdVersion: ">=1.36.0"`. See
[contract lifecycle and SDK migration](../../cli/azd/docs/extensions/contract-versioning.md#october-2026-graduation-azd-1360).

---

For the full feature tracking table, see [cli/azd/docs/feature-stages.md](../../cli/azd/docs/feature-stages.md).
