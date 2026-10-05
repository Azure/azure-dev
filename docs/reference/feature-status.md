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

## Foundry Extension Authoring

The evaluations extension supports conversation dataset authoring in static
mode (completed messages, no target invocation) and simulation mode (scenario
seeds, an agent target, and an independent simulator model).
Simulation validates eligible Azure OpenAI project connections before writing
configuration and can reuse qualified locally authored bindings. See the
[evaluation extension contract](../../cli/azd/extensions/azure.ai.evaluations/README.md).

These are extension authoring capabilities, not a feature-stage graduation or
proof of production connection APIs, evaluator availability, accepted request
IDs, or deployed model readiness. Target-service acceptance remains separate
from local configuration validation.

---

For the full feature tracking table, see [cli/azd/docs/feature-stages.md](../../cli/azd/docs/feature-stages.md).
