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
| `env unset` | Beta |
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
| [Microsoft Foundry on existing AKS](../../cli/azd/extensions/azure.ai.projects/README.md#existing-aks-hosting) (default supporting resources) | In development (default off; explicit opt-in required) |

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
| Extensions | Stable |

## Extension gRPC Contracts

| Capability | Contract stage | Stable since | Beta status |
|---|---|---|---|
| Extension usage telemetry (`TelemetryService.ReportUsage`) | Stable v1 | azd 1.36.0 | Deprecated; wire route retained |
| Current Azure principal (`AccountService.GetCurrentPrincipal`) | Stable v1 | azd 1.36.0 | Deprecated; wire route retained |
| Deployment preview (`ServiceTargetService.Stream`) | Stable v1 | azd 1.37.0 | Preview messages/fields deprecated; wire route and Go SDK compatibility retained |

The deployment preview contract graduation does not change the CLI feature stage
in Advanced Features above. Host and SDK 1.37.0 shipped on October 21, 2026; see the
[second graduation audit](https://github.com/Azure/azure-dev/issues/10625).

These are contract graduations, not a change to the overall Extensions feature stage
or telemetry eligibility/privacy policy. The minimum azd runtime version for stable
consumers depends on the capabilities they use:

- Extension usage telemetry or current Azure principal: `requiredAzdVersion: ">=1.36.0"`.
- Deployment preview: `requiredAzdVersion: ">=1.37.0"`.

Extensions using multiple capabilities must meet the highest applicable minimum and
retain any stricter existing requirement.

`requiredAzdVersion` constrains the azd runtime version, not the Go SDK dependency.
Pin the SDK separately to a published module release containing the stable APIs in use.
See [contract lifecycle and SDK migration](../../cli/azd/docs/extensions/contract-versioning.md).

---

For the full feature tracking table, see [cli/azd/docs/feature-stages.md](../../cli/azd/docs/feature-stages.md).
