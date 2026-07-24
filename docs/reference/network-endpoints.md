# Network Endpoints Reference — Hosts `azd` Connects To

> The external hosts and endpoints the Azure Developer CLI (`azd`) may contact at
> runtime. Use this to configure firewalls, proxies, or other traffic-filtering
> systems when running `azd` in a restricted network environment.

Every host below is derived from constants in the `azd` source. Endpoints that
depend on the target Azure cloud (public, US Government, China) are listed per
cloud in [Azure control plane](#1-azure-control-plane-per-cloud). Host names that
contain `<...>` are constructed at runtime from user- or resource-specific values.

> [!NOTE]
> Not every command contacts every host. `azd` only reaches a host when the
> corresponding feature runs — for example, template hosts are only contacted by
> `azd init`, and tool-download hosts only when a required CLI is missing. The
> [minimum allowlist](#minimum-allowlist-public-cloud) at the end summarizes the
> hosts a typical `provision` + `deploy` flow needs.

---

## 1. Azure control plane (per cloud)

`azd` selects a cloud from the `cloud` configuration (default `AzureCloud`). The
ARM Resource Manager and Entra (AAD) authority hosts come from the Azure SDK's
well-known cloud configuration; the storage, container-registry, Key Vault, and
portal suffixes are defined in [`cli/azd/pkg/cloud/cloud.go`](../../cli/azd/pkg/cloud/cloud.go).

### AzureCloud (public) — default

| Host / suffix | Purpose |
|---|---|
| `management.azure.com` | ARM control plane (resource manager) |
| `login.microsoftonline.com` | Entra ID (AAD) authority — sign-in and token acquisition |
| `graph.microsoft.com` | Microsoft Graph — app / service-principal management (see note below) |
| `portal.azure.com` | Portal deep links shown in output |
| `*.core.windows.net` | Storage endpoint suffix (blobs, queues, tables) |
| `*.azurecr.io` | Azure Container Registry suffix |
| `*.vault.azure.net` | Key Vault suffix |

### AzureUSGovernment

| Host / suffix | Purpose |
|---|---|
| `management.usgovcloudapi.net` | ARM control plane |
| `login.microsoftonline.us` | Entra ID authority |
| `portal.azure.us` | Portal deep links |
| `*.core.usgovcloudapi.net` | Storage suffix |
| `*.azurecr.us` | Container Registry suffix |
| `*.vault.usgovcloudapi.net` | Key Vault suffix |

### AzureChinaCloud

| Host / suffix | Purpose |
|---|---|
| `management.chinacloudapi.cn` | ARM control plane |
| `login.chinacloudapi.cn` | Entra ID authority |
| `portal.azure.cn` | Portal deep links |
| `*.core.chinacloudapi.cn` | Storage suffix |
| `*.azurecr.cn` | Container Registry suffix |
| `*.vault.azure.cn` | Key Vault suffix |

> [!IMPORTANT]
> **Microsoft Graph is not cloud-aware.** `graph.microsoft.com` is hardcoded to
> the public-cloud endpoint in
> [`cli/azd/pkg/graphsdk/graphsdk.go`](../../cli/azd/pkg/graphsdk/graphsdk.go)
> and is used regardless of the selected cloud (for example, when creating or
> assigning service principals during `azd pipeline config`).

The exact per-cloud endpoint metadata for a subscription can be queried at
`https://<management-endpoint>/metadata/endpoints?api-version=2023-12-01`.

---

## 2. Authentication

The Entra ID (AAD) authority host is chosen per cloud (see section 1) and used to
build MSAL sign-in and token URLs in
[`cli/azd/pkg/auth/manager.go`](../../cli/azd/pkg/auth/manager.go).

| Host | Purpose |
|---|---|
| `login.microsoftonline.com` (public) / `login.microsoftonline.us` (US Gov) / `login.chinacloudapi.cn` (China) | Interactive, device-code, and service-principal sign-in; token acquisition |
| `token.actions.githubusercontent.com` | OIDC issuer for GitHub Actions **federated credentials** — configured on Entra during `azd pipeline config`. Defined in [`cli/azd/pkg/entraid/entraid.go`](../../cli/azd/pkg/entraid/entraid.go) and [`cli/azd/pkg/pipeline/github_provider.go`](../../cli/azd/pkg/pipeline/github_provider.go) |

When running in Azure Cloud Shell, `azd` obtains tokens from the local managed
identity endpoint (provided via environment, not a fixed public host) — see
[`cli/azd/pkg/auth/cloudshell_credential.go`](../../cli/azd/pkg/auth/cloudshell_credential.go).

---

## 3. Telemetry

`azd` sends usage telemetry to Application Insights unless telemetry is disabled
(`AZURE_DEV_COLLECT_TELEMETRY=no`). Endpoints are defined in
[`cli/azd/internal/telemetry/telemetry.go`](../../cli/azd/internal/telemetry/telemetry.go).

| Host | Purpose |
|---|---|
| `centralus-2.in.applicationinsights.azure.com` | Telemetry ingestion (production builds) |
| `centralus.livediagnostics.monitor.azure.com` | Live-metrics stream (production builds) |
| `westus-0.in.applicationinsights.azure.com` | Telemetry ingestion (dev builds only) |
| `westus.livediagnostics.monitor.azure.com` | Live-metrics stream (dev builds only) |

See the [Telemetry Data Reference](telemetry-data.md) for what is collected and
[Environment Variables](environment-variables.md) for how to opt out.

---

## 4. Feature experimentation (flighting)

`azd` queries an experimentation (TAS) service at startup to resolve feature
flags. Defined in
[`cli/azd/cmd/middleware/experimentation.go`](../../cli/azd/cmd/middleware/experimentation.go).

| Host | Purpose |
|---|---|
| `default.exp-tas.com` | Experimentation / feature-flighting assignment service |

The endpoint can be overridden with `AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT`.
If the host is unreachable, `azd` logs the failure and continues with default
feature enablement.

---

## 5. External tools (auto-download)

When a required CLI tool is missing, `azd` downloads a pinned version to its
config directory. These are the download sources:

| Host | Tool | Source |
|---|---|---|
| `downloads.bicep.azure.com` | Bicep CLI | [`cli/azd/pkg/tools/bicep/bicep.go`](../../cli/azd/pkg/tools/bicep/bicep.go) |
| `github.com` | GitHub CLI (`gh`) release assets — `github.com/cli/cli/releases/...` | [`cli/azd/pkg/tools/github/github.go`](../../cli/azd/pkg/tools/github/github.go) |
| `github.com` | `pack` (Cloud Native Buildpacks) release assets — `github.com/buildpacks/pack/releases/...` | [`cli/azd/pkg/tools/pack/pack.go`](../../cli/azd/pkg/tools/pack/pack.go) |
| `mcr.microsoft.com` | Default buildpack builder image (`oryx/builder`) pulled by `pack build` when no Dockerfile is present. Overridable with `AZD_BUILDER_IMAGE`. | [`cli/azd/pkg/project/container_helper.go`](../../cli/azd/pkg/project/container_helper.go) |

> [!NOTE]
> GitHub release downloads redirect to GitHub's asset CDN, so
> `objects.githubusercontent.com` (and, for some flows,
> `raw.githubusercontent.com`) must also be reachable when downloading `gh` or
> `pack`.

Tools that `azd` does **not** download — Docker/Podman, Terraform, `kubectl`,
Helm, Kustomize, Azure CLI — must be installed separately. When one is missing,
`azd` prints an `aka.ms` install-help link (see section 8); those links are
informational and not contacted automatically.

---

## 6. Templates

Used by `azd init` and `azd template list`.

| Host | Purpose |
|---|---|
| `aka.ms` → `aka.ms/awesome-azd/templates.json` | Default template gallery source. Defined in [`cli/azd/pkg/templates/source_manager.go`](../../cli/azd/pkg/templates/source_manager.go) |
| `github.com` | Cloning template repositories (e.g. `github.com/Azure-Samples/<template>`). Defined in [`cli/azd/pkg/templates/path.go`](../../cli/azd/pkg/templates/path.go) |
| `raw.githubusercontent.com`, `api.github.com` | Resolved to `github.com` when normalizing template source URLs. Defined in [`cli/azd/pkg/templates/gh_source.go`](../../cli/azd/pkg/templates/gh_source.go) |
| `azure.github.io` | Template gallery info links (`awesome-azd`, `ai-app-templates`) shown in help text |

---

## 7. Extensions

Used by `azd extension` commands.

| Host | Purpose |
|---|---|
| `aka.ms` → `aka.ms/azd/extensions/registry` | Default extension registry source. Defined in [`cli/azd/pkg/extensions/manager.go`](../../cli/azd/pkg/extensions/manager.go) |

Extension artifacts are downloaded from whatever URL the registry entry
specifies; add those hosts to your allowlist as needed for the extensions you use.

---

## 8. Self-update

`azd` checks for newer releases and can update itself. Defined in
[`cli/azd/pkg/update/manager.go`](../../cli/azd/pkg/update/manager.go) and
[`cli/azd/cmd/update.go`](../../cli/azd/cmd/update.go).

| Host | Purpose |
|---|---|
| `aka.ms` → `aka.ms/azure-dev/versions/cli/latest` | Latest stable version check |
| `azuresdkartifacts.z5.web.core.windows.net` | Standalone release binaries |
| `aka.ms` → `aka.ms/install-azd.sh`, `aka.ms/install-azd.ps1` | Install / upgrade scripts |

The version check can be disabled — see [Environment Variables](environment-variables.md).

---

## 9. `aka.ms` redirector and informational links

`azd` uses `aka.ms` both for endpoints it contacts (templates, extensions,
self-update, above) and for help/error links it only prints. `aka.ms` is a
Microsoft URL shortener that redirects to a range of destinations, so allowlist
the `aka.ms` host itself plus the redirect targets you actually use.

Common install-help links (`aka.ms/azure-dev/<tool>-install`) point to external
tool documentation and are not contacted automatically.

---

## 10. CI/CD provider hosts

Contacted by `azd pipeline config` depending on the chosen provider:

| Host | Purpose |
|---|---|
| `github.com`, `api.github.com` | Repo, secrets, variables, and Actions setup (GitHub provider). Defined in [`cli/azd/pkg/pipeline/github_provider.go`](../../cli/azd/pkg/pipeline/github_provider.go) |
| `dev.azure.com` / `<org>.visualstudio.com` | Azure DevOps organization (Azure DevOps provider) — host is the user-supplied org URL. Defined in [`cli/azd/pkg/pipeline/azdo_provider.go`](../../cli/azd/pkg/pipeline/azdo_provider.go) |

---

## 11. Customer / deployment-target hosts

`azd` also contacts hosts derived from **your** subscription and resources, which
cannot be enumerated ahead of time. These include, at minimum:

- The container registry created for your project (`<registry-name>.azurecr.io`
  and the per-cloud suffix from section 1) — image push/pull.
- Storage accounts for deployment artifacts (`<account>.blob.core.windows.net`
  and the per-cloud suffix).
- App Service / Functions Kudu (SCM) endpoints (`<app>.scm.azurewebsites.net`)
  for zip deploy.
- Key Vault instances referenced by your environment
  (`<vault>.vault.azure.net`).
- Any application endpoints your `azure.yaml` and infrastructure define.

Allowlist the wildcard service suffixes from section 1 to cover these.

---

## Minimum allowlist (public cloud)

For a typical `azd provision` + `azd deploy` on public cloud, allow:

```
# Control plane & auth
management.azure.com
login.microsoftonline.com
graph.microsoft.com

# Azure service suffixes (your resources)
*.core.windows.net
*.azurecr.io
*.vault.azure.net
*.azurewebsites.net

# Tooling & templates (as needed)
downloads.bicep.azure.com     # Bicep download
mcr.microsoft.com             # buildpack builder image (containerized services)
github.com                    # gh/pack downloads, template clones
objects.githubusercontent.com # GitHub release assets
aka.ms                        # templates, extensions, self-update redirects

# Optional (disable to skip)
centralus-2.in.applicationinsights.azure.com   # telemetry (AZURE_DEV_COLLECT_TELEMETRY=no)
centralus.livediagnostics.monitor.azure.com    # telemetry
default.exp-tas.com                             # experimentation flighting
```

Swap the control-plane, auth, and service-suffix hosts for the US Government or
China equivalents in section 1 when targeting those clouds.

> [!NOTE]
> This list reflects hosts hardcoded in `azd`. Individual templates, hooks, and
> extensions may contact additional hosts (package registries such as npm/PyPI,
> base container images, etc.). Review your template's build and infrastructure
> for project-specific dependencies.
