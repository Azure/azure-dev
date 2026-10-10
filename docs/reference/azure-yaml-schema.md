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
| `remoteBuild` | boolean | Enable remote build for code-based Azure Functions |

### Docker Properties

| Property | Type | Description |
|---|---|---|
| `path` | string | Path to the Dockerfile |
| `context` | string | Docker build context path |
| `platform` | string | Container platform target |
| `target` | string | Dockerfile build target |
| `registry` | string | Destination container registry |
| `image` | string | Name applied to a built container image |
| `tag` | string | Tag applied to a built container image |
| `buildArgs` | list | Arguments passed to the container build |
| `network` | string | Networking mode for Dockerfile `RUN` instructions |
| `remoteBuild` | boolean | Prefer building and pushing with Azure Container Registry; fall back locally only when ACR refuses scheduling with `TasksOperationsNotAllowed` |
| `imagePassthrough` | boolean | Reuse an existing remote service `image` without building or publishing it; `azd deploy --from-package` can override the image for one deployment |

If ACR refuses scheduling with `TasksOperationsNotAllowed`, azd warns and falls back automatically, including in non-interactive runs. Docker or Podman must be installed and running. Fallback publishes a supplied local package or builds and pushes from source.

Other errors, including build failures, cancellation, and failures reading remote logs or status, do not trigger fallback. If fallback also fails, azd preserves both errors. Set `docker.remoteBuild: false` to build locally.

If the local runtime is unavailable, the error explains both the ACR refusal and the failed fallback. Check that Docker or Podman is running and accessible before retrying; the underlying error includes connection or permission details.

`docker.imagePassthrough` declares that azd does not own the container image lifecycle. It requires the service-level
`image` property to contain a fully qualified remote image and cannot be combined with `docker.remoteBuild`. During package, publish, and deploy operations, azd
uses the configured image as the existing remote image without building, pulling, tagging, copying, or publishing it:

```yaml
services:
  api:
    host: containerapp
    image: registry.example.com/apps/api:1.0
    docker:
      imagePassthrough: true
```

The service `image` is the default. A fully qualified remote image supplied to `azd deploy --from-package` overrides it
for that deployment and is also passed through unchanged:

```bash
azd deploy api --from-package other-registry.example.com/apps/api:2.0
```

Passthrough overrides do not support local image names, archives, or directories. The `--from-package` override above
applies only to `azd deploy`; `azd publish --from-package` and `azd publish --to` are not supported for passthrough
services. Running `azd publish` without either flag reuses the configured remote image and does not publish it.

azd does not sign in to the source registry or verify access to it in this mode. The destination platform must already
have permission to pull the image through its managed identity or registry credentials.

When `imagePassthrough` is omitted or `false`, an external service image can still be pulled and copied into the
configured destination registry.

## Infrastructure Layer Variable Aliases

Each entry under `infra.layers[]` or `layers[].infra[]` can translate between
the **[provider view](../concepts/glossary.md#provider-view)** and the
**[project view](../concepts/glossary.md#project-view)**:

- The provider view uses the variable names an infrastructure provider reads or emits.
- The project view uses the environment variable names the project stores and supplies to CI.
- Both alias maps are written as `PROVIDER_VARIABLE: PROJECT_VARIABLE`.

```yaml
infra:
  layers:
    - name: producer
      path: infra/producer
      outputAliases:
        PROVIDER_ENDPOINT: PROJECT_ENDPOINT
    - name: consumer
      path: infra/consumer
      paramAliases:
        PROVIDER_INPUT: PROJECT_ENDPOINT
```

- `paramAliases` maps an environment variable name in the provider view to its source
  variable name in the project view. The provider variable is the name referenced by
  the parameter file, not necessarily the IaC parameter name.
- `outputAliases` maps an output name in the provider view to the variable name
  persisted in the project view. Outputs without a mapping keep their
  original names.

Names on both sides of either alias map must match `^[A-Za-z_][A-Za-z0-9_]*$`.
azd checks this when it loads the project configuration.

In this example, the producer's `PROVIDER_ENDPOINT` output is stored as
`PROJECT_ENDPOINT`. The consumer reads that value as `PROVIDER_INPUT`. These aliases
work with both Bicep and Terraform providers. For Bicep layers, azd uses the
aliases during static dependency analysis, so the consumer waits for the producer.
Terraform dependencies are not inferred from aliases. Declare
`dependsOn: [producer]` on the consumer when it needs the producer's outputs.
This also applies to Bicep consumers of Terraform outputs.

Pipeline configuration exports parameter variables and secrets using project-view
names. Outputs from earlier layers are also tracked in the project view while
planning later layers.

Service environment-update events also use project-view output names during
provisioning and `azd env refresh`. This includes the .NET user-secrets integration,
which converts `__` in those names to the .NET configuration separator `:`.

Two output aliases in one infrastructure entry cannot target the same project variable; azd rejects this when it
loads the project configuration. An alias can also collide with an output that has
no alias. Since provider output names are not declared in `azure.yaml`, azd checks
that case after deployment, before writing any deployment outputs to the environment.

Aliases do not bypass the environment filter for dynamic loader variables.
Names in the `LD_` and `DYLD_` namespaces are excluded from `Dotenv()` and
subprocess environments, including when introduced by an input alias.

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

- **Command hooks (project-level):** `build`, `deploy`, `down`, `package`, `provision`, `publish`, `restore`, `up`
- **Service lifecycle hooks (service-level):** `restore`, `build`, `package`, `publish`, `deploy`

For example, `preprovision` runs before provisioning, `postdeploy` runs after deployment. Service-level hooks are defined under a service's `hooks` section in `azure.yaml` and apply only to that service.

## JSON Schema

The full JSON schema for `azure.yaml` is maintained in the [schemas/](../../schemas/) directory and published for editor validation.

## Host-Specific Notes

### App Service (`host: appservice`)

App Service supports two deployment modes:

- **Zip deploy** (default): When `language` is set to a non-Docker language (e.g., `python`, `js`, `dotnet`), azd builds the code, creates a zip archive, and deploys it via the Kudu zip deploy API.
- **Container deploy**: When `language: docker` is set, azd builds the container image, pushes it to ACR, and updates the site's `linuxFxVersion`. Currently Linux App Service only. Your infrastructure (bicep/terraform) must configure ACR access (e.g., managed identity ACR pull, identity assignment) before deploying. azd only updates the image reference at deploy time.

**Note**: Container deployment supports both `language: docker` (with a Dockerfile) and polyglot containerization (e.g., `language: python` with `docker.path` pointing to a Dockerfile). You can also use a pre-built `image:` without local source.

Example container deployment:

```yaml
services:
  web:
    project: ./src/web
    language: docker
    host: appservice
    docker:
      path: ./Dockerfile
```

### Function App (`host: function`)

Function Apps support code and container deployment:

- **Zip deploy** (default): When no container configuration is present, azd builds the Function project, creates a zip archive, and deploys it through the Function App deployment API. The top-level `remoteBuild` property applies only to this mode.
- **Container deploy**: Configure `language: docker`, set `docker.path` for a non-Docker language, or provide a pre-built `image`. azd builds or resolves the image, publishes it to ACR when needed, and updates the Function App's `linuxFxVersion`.

Container-based Function infrastructure must configure a Linux Function App, an initial `DOCKER|` image reference, and registry pull access before deployment. These settings remain the responsibility of Bicep or Terraform. If the provisioned Function App and the service disagree about the deployment mode, azd fails before attempting an incompatible upload and identifies the configuration mismatch.

Unlike App Service, Function Apps always deploy to the main site. Deployment slots are not part of the Function App workflow, so `AZD_DEPLOY_{SERVICE}_SLOT_NAME` has no effect and azd never prompts for a slot.

Example TypeScript container deployment:

```yaml
services:
  function:
    project: ./src/function
    language: ts
    host: function
    docker:
      path: ./Dockerfile
```

## See Also

- [azure.yaml JSON Schema](../../schemas/) — Machine-readable schema definition
- [Feature Status](feature-status.md) — Which languages and hosts are stable/beta/alpha
