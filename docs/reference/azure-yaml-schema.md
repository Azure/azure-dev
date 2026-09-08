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
| `layers` | list | Alpha. Project layers containing infrastructure and services |
| `pipeline` | object | CI/CD pipeline configuration |
| `hooks` | map | Project-level lifecycle hooks |
| `infra` | object | Infrastructure provider configuration |
| `state` | object | Remote state backend configuration |
| `resources` | map | Azure resource definitions |
| `requiredVersions` | object | Version constraints for azd and extensions |
| `platform` | object | Platform-specific configuration |
| `workflows` | object | Workflow configuration |
| `cloud` | object | Cloud environment configuration |

## Project Layers

Project layers group infrastructure and services into lifecycle units. Infrastructure entries and service names must
be unique across the project. Independent infrastructure entries within a layer may provision concurrently. If an
infrastructure dependency crosses layer boundaries, the dependent layer waits for the provider layer to complete.
Every infrastructure entry under a project layer must declare `provider` explicitly. Provider inheritance remains
available only in the legacy `infra.layers[]` format, where an entry may inherit `infra.provider`.

```yaml
layers:
  - name: application
    infra:
      - name: app-infra
        path: ./infra/app
        provider: bicep
    services:
      api:
        project: ./src/api
        host: containerapp
        language: js
```

The flat `infra` and `services` format and the existing `infra.layers[]` format remain supported. azd normalizes all
three formats internally and saves legacy projects in their original format. `azd provision <name>` continues to
select an individual infrastructure entry, including entries nested under a project layer.

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
| `remoteBuild` | boolean | Build and push with Azure Container Registry remote build instead of building locally |
| `imagePassthrough` | boolean | Reuse an existing remote service `image` without building or publishing it; `azd deploy --from-package` can override the image for one deployment |

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
- **Infrastructure entry hooks:** `provision`

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
