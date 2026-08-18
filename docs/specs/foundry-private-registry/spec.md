<!-- cspell:ignore JFrog CustomKeys imagePassthrough registryConnectionId tokenEndpoint provider_name logstream authType codeConfiguration remoteBuild BYO ACR Entra entra -->

# Private non-ACR registry connections for Foundry hosted agents

## Problem

Enable Foundry hosted agents to run from container images in a customer's **private, non-ACR registry**, such as private JFrog Artifactory. Today, hosted agents support **public and private ACR images** and **public non-ACR images**. Private non-ACR registries are not supported; this design closes that gap.

The developer registers the private registry as a Foundry project connection and names that connection on the hosted-agent version. When Foundry pulls the image, it authenticates to the registry using the connection's configured credential mode. The first supported mode is OAuth 2.0 token exchange ([RFC 8693](https://www.rfc-editor.org/rfc/rfc8693)) with the Foundry project's managed identity. Additional credential modes can be added over time without changing the hosted-agent contract.

Before this change, `azure.ai.agents` could not associate a pre-built image with a Foundry project connection through the service API's `container_configuration.registry_connection_id` property. Without that mapping, developers cannot use `azd` to deploy a hosted agent from a private non-ACR registry even when the registry, workload identity trust, Foundry project, and Foundry connection already exist.

This design adds a registry-neutral authoring and deployment path. JFrog is the first end-to-end example, but neither the `azure.yaml` contract nor production code contains vendor names, hostname allowlists, vendor-specific validation, or setup logic.

## Solution

Add an optional `registryConnectionId` property to hosted-container agent authoring and map it to `definition.container_configuration.registry_connection_id` in the agent-version request. The service contract is defined by the merged [Foundry REST API specification](https://github.com/Azure/azure-rest-api-specs/pull/44915), based on the service team's [internal Vienna design](https://msdata.visualstudio.com/Vienna/_git/vienna/pullrequest/2207414).

The agent image remains the top-level service `image`. A pre-built image initialized by `azd ai agent init` also enables `docker.imagePassthrough: true`, which tells core azd that the configured image is the final remote artifact. Core owns Package and Publish for that artifact without requiring Docker and without building, pulling, tagging, copying, authenticating to, or pushing the image.

```yaml
services:
  private-registry-agent:
    host: azure.ai.agent

    # The final, already-published image reference.
    image: registry.example.com/agents/private-registry-agent:1.0

    # Core passes the remote image reference through unchanged.
    docker:
      imagePassthrough: true

    # A Foundry project connection short name or ID.
    registryConnectionId: private-registry

    kind: hosted
    name: private-registry-agent
    protocols:
      - protocol: invocations
        version: 1.0.0
```

The resulting service request places the connection reference only inside the container configuration:

```json
{
  "definition": {
    "kind": "hosted",
    "container_configuration": {
      "image": "registry.example.com/agents/private-registry-agent:1.0",
      "registry_connection_id": "private-registry"
    }
  }
}
```

When `registryConnectionId` is absent, `registry_connection_id` is omitted. Existing public-image, ACR, and code-deploy request shapes remain unchanged.

## Scope

**In scope**

- Registry-neutral `registryConnectionId` authoring and mapping to `definition.container_configuration.registry_connection_id` for hosted agents using private, already-published non-ACR images.
- Explicit core image passthrough through top-level `image` and `docker.imagePassthrough: true`.
- Greenfield agent init against a pre-existing Foundry project connection and declarative sibling `azure.ai.connection` workflows.
- Connection lookup, dependency validation, and pass-through of generic credential and metadata fields.
- Compatibility for older pre-built-image projects, with JFrog as the first E2E example rather than a production dependency.

**Out of scope**

- Vendor detection, allowlists, adapters, setup wizards, or vendor-specific production validation.
- Image build, pull, copy, login, push, or non-ACR remote build behavior.
- Static pull secrets or a first-class registry connection category; the initial flow uses generic `CustomKeys` connections.
- Creating or validating vendor OIDC configuration, identity mappings, repository permissions, Entra applications, or token-exchange semantics.
- Changes to existing source-build, ACR, public-image, or code-deploy behavior outside explicit image passthrough.

## Relationship to implementation work

The design spans two implementation PRs that share the same service contract but remain separable by responsibility:

```text
Foundry service API contract                    Azure/azure-rest-api-specs#44915
└─ core container image passthrough             Azure/azure-dev#9588
   └─ agents private-registry connection        Azure/azure-dev#9586
```

Core PR #9588 adds the explicit `docker.imagePassthrough` lifecycle and gRPC model field. It is registry-neutral and applies to every service target that opts into the final-image contract. Extension PR #9586 adds agent authoring, init, dependency validation, Foundry connection lookup, and REST mapping; it uses the core signal for both the pre-existing BYO-image path and the new private-registry path.

The service API contract is independently useful to other clients: `registry_connection_id` names a Foundry project connection and leaves its authentication mechanism behind the connection boundary. azd narrows its initial user workflow to the generic `CustomKeys` token-exchange shape without narrowing the service contract or adding vendor-specific behavior.

> [!IMPORTANT]
> This document specifies target behavior. Availability on `main` is tracked by core PR #9588 and extension PR #9586; the commands and authoring examples below require builds that include both implementations.

## Authoring contract

`registryConnectionId` is a sibling of `image`, `kind`, `protocols`, and `container` on an `azure.ai.agent` service.

```yaml
services:
  private-registry-agent:
    host: azure.ai.agent

    # Required by registryConnectionId: this must be a pre-built image.
    image: registry.example.com/team/agent:v1

    # Required for the no-build, no-pull passthrough lifecycle.
    docker:
      imagePassthrough: true

    # Optional for public images and ACR; required for this private non-ACR flow.
    registryConnectionId: production-registry

    kind: hosted
    name: private-registry-agent
    protocols:
      - protocol: responses
        version: 1.0.0
    container:
      resources:
        cpu: "1"
        memory: 2Gi
```

| Field | Rule |
| --- | --- |
| `image` | Top-level service image containing the already-published remote image reference. When `registryConnectionId` is set, init and deploy require an explicit registry host and repository; the schema separately constrains the connection to hosted, non-code agents. |
| `docker.imagePassthrough` | Treats the service image as the final remote artifact. Requires `image`, cannot be combined with `docker.remoteBuild`, and preserves the image instead of copying it to another registry. |
| `registryConnectionId` | Optional non-empty Foundry project connection short name or ID. Valid only for a hosted container agent with a pre-built image. |
| `uses` | Must include a matching sibling `azure.ai.connection` service when `registryConnectionId` names such a service. An external connection name or ID is not added to `uses`. |

The connection reference survives unified `azure.yaml` and legacy agent-manifest parsing and round trips. The flag value supplied to init takes precedence over a manifest value.

## Greenfield agent workflow

This workflow creates a new local agent project that targets existing Foundry resources. Before running `azd ai agent init`, use the Foundry portal or Azure CLI to create the Foundry project connection. The Foundry project, private image, registry-side OIDC trust and identity binding, and Entra audience application must also already exist. Record the project resource ID and connection name; azd references these resources but does not bootstrap them.

```bash
PROJECT_ID="<foundry-project-resource-id>"
CONNECTION_NAME="private-registry"

# Init verifies the pre-created connection and writes the agent service with
# imagePassthrough enabled.
azd ai agent init --no-prompt \
  --agent-name private-registry-agent \
  --image <private-registry-host>/<repository>/agent:<tag> \
  --project-id "$PROJECT_ID" \
  --registry-connection "$CONNECTION_NAME"

# Do not run azd provision after this init workflow.
# The project and connection already exist.
azd deploy --no-prompt
```

The generated agent service has the following essential shape:

```yaml
services:
  private-registry-agent:
    host: azure.ai.agent

    # Init reuses an existing project service key or derives one from the
    # selected project name, with ai-project as the fallback.
    uses:
      - <project-service-key>

    # The image is passed directly to Foundry.
    image: <private-registry-host>/<repository>/agent:<tag>
    docker:
      imagePassthrough: true

    # This is an external Foundry connection, not an azd service dependency.
    registryConnectionId: private-registry

    kind: hosted
    name: private-registry-agent
```

The external connection is intentionally not appended to `uses`. The project dependency key is not fixed: init reuses an existing `azure.ai.project` service key, otherwise derives one from the selected project name, and falls back to `ai-project`. Init verifies the connection by name or ID when project context is available, but Foundry remains authoritative for compatibility, authentication, and image-pull failures.

## Declarative sibling connection workflow

A connection can instead be represented as a sibling `azure.ai.connection` service. In this flow, `azd provision` is required because azd provisions the declared connection before deploying the agent.

The example below uses an existing Foundry project, but the connection and agent remain declarative azd services:

```yaml
name: private-registry-agent

infra:
  provider: microsoft.foundry

services:
  existing-foundry-project:
    host: azure.ai.project

    # Brownfield project: azd does not create or reconfigure the project.
    endpoint: https://<account>.services.ai.azure.com/api/projects/<project>

  private-registry:
    host: azure.ai.connection

    # Provision the connection on this project.
    uses:
      - existing-foundry-project

    # Phase 1 uses the generic Foundry connection shape.
    category: CustomKeys
    target: ${REGISTRY_URL}
    authType: CustomKeys
    credentials:
      keys:
        audience: ${REGISTRY_AUDIENCE}
        tokenEndpoint: ${REGISTRY_TOKEN_ENDPOINT}

        # Passed through unchanged. azd does not interpret this field.
        body.provider_name: ${REGISTRY_PROVIDER}
    metadata:
      type: registry_connection
      mode: oauth_token_exchange

  private-registry-agent:
    host: azure.ai.agent

    # The project and sibling connection must be ready before deployment.
    uses:
      - existing-foundry-project
      - private-registry

    kind: hosted
    name: private-registry-agent

    # The already-published private image remains in its source registry.
    image: ${REGISTRY_IMAGE}
    docker:
      imagePassthrough: true

    # Resolves to the sibling connection provisioned above.
    registryConnectionId: private-registry

    protocols:
      - protocol: invocations
        version: 1.0.0
    container:
      resources:
        cpu: "1"
        memory: 2Gi
```

Provision and deploy the declarative services:

```bash
# Existing-project context used by the Foundry provider.
azd env set AZURE_AI_PROJECT_ID <foundry-project-resource-id>

# Generic connection and image configuration.
azd env set REGISTRY_URL https://<private-registry-host>
azd env set REGISTRY_AUDIENCE <entra-audience-app-id>
azd env set REGISTRY_TOKEN_ENDPOINT /access/api/v1/oidc/token
azd env set REGISTRY_PROVIDER <oidc-provider-name>
azd env set REGISTRY_IMAGE <private-registry-host>/<repository>/agent:<tag>

# Provision creates or updates the sibling Foundry connection.
azd provision --no-prompt

# Deploy submits the hosted-agent definition.
azd deploy --no-prompt
```

When `registryConnectionId` matches a local service name, that service must have `host: azure.ai.connection`, must appear in the agent's `uses`, and must be enabled by its deployment condition. A reference that does not match a local service is treated as an external Foundry connection name or ID and is left for the service to resolve.

## Lifecycle

### Init

For a flag-based or manifest-based pre-built image, init writes the image at the top level of the agent service and enables `docker.imagePassthrough: true`. When `--registry-connection` is supplied, init writes `registryConnectionId` and skips ACR setup.

The non-interactive private-registry flow does not prompt for an image source, template, language, Dockerfile, registry, or connection. `--registry-connection` requires `--image` unless the supplied hosted-agent manifest already contains an image.

### Provision

The pre-created-connection workflow does not run provision after init. The Foundry project and connection are prerequisites, and the generated environment records the existing project context.

The declarative sibling workflow runs provision to create or update the `azure.ai.connection` service. Provision passes generic credentials and metadata to the Foundry connection resource without interpreting registry-specific fields.

### Build

Core recognizes `docker.imagePassthrough: true`, parses the image reference, and returns without a local or remote build. Docker is not required. `docker.remoteBuild` is neither generated nor permitted with image passthrough. The agents extension applies the stricter fully qualified reference requirement when a registry connection is present.

### Package

The agents extension delegates Package to core's container lifecycle. Core emits a remote container artifact for the configured service image. It does not pull, tag, copy, or otherwise materialize the image locally.

### Publish

The agents extension delegates Publish to core. Core returns a remote publish artifact for the same image reference without registry login, pull, tag, or push operations. A publish-time image override is rejected because passthrough preserves the configured image.

### Deploy

The agents extension loads the hosted-agent definition, requires an image with an explicit registry host and repository when a registry connection is present, validates the connection reference and dependencies, and sends the image and optional connection reference to Foundry. `registryConnectionId` becomes `definition.container_configuration.registry_connection_id`.

Foundry uses the project connection and its service identity to retrieve the image. Foundry and the registry are authoritative for token exchange, trust, repository authorization, manifest access, and container startup.

### Update

Changing an external connection's vendor-side configuration remains outside azd. After updating the external connection or image, rerun `azd deploy` as appropriate.

For a declarative sibling connection, rerun `azd provision` after changing its declared credentials or metadata, then rerun `azd deploy` if the agent image or connection reference changed.

## Package and publish compatibility fallback

Newly generated pre-built-image services use core `docker.imagePassthrough`. This is the preferred lifecycle because it gives core an explicit signal that the image is already published and must not be built or copied.

Older agent project configurations may contain a pre-built image without `docker.imagePassthrough`. The agents extension retains its existing pre-built-image artifact path as a compatibility fallback. When that path selects the configured image as pre-built, extension Package emits a remote pre-built artifact and Publish preserves it without publishing a container.

The fallback does not change the new authoring contract and is not a substitute for generating `imagePassthrough`. Existing ambiguous configurations may retain their previous image-source selection behavior; newly initialized services avoid that ambiguity and do not prompt.

## Existing behavior

The feature is additive.

- **Public non-ACR BYO images** continue to work without `registryConnectionId`. Newly initialized pre-built images use the same explicit image-passthrough lifecycle.
- **Pre-built ACR images** do not require a Foundry registry connection and continue to use the existing Foundry/Azure identity behavior.
- **Source build and ACR publish** remain unchanged when `docker.imagePassthrough` is absent.
- **Private ACR** continues to use the existing Azure identity and networking paths rather than this generic registry-connection contract.
- **Code deploy** continues to package and upload code. It cannot declare `registryConnectionId`, and no container registry configuration is emitted.
- **Non-hosted agents**, including `prompt-voice` and `workflow`, cannot use `registryConnectionId`.
- **Connection provisioning** continues to use the existing generic `azure.ai.connection` implementation.

## Validation

Validation is divided between schema checks, init, deployment, core container handling, and the Foundry service.

### azd validates

- `registryConnectionId` is a non-empty, non-whitespace string.
- The connection property applies only to `kind: hosted` container agents.
- A registry connection requires a pre-built image.
- A registry connection cannot be combined with code deploy or `codeConfiguration`.
- `--registry-connection` cannot be combined with `--kind` (today `--kind` selects the non-hosted `prompt-voice` flow).
- `--registry-connection` requires `--image` when no manifest supplies an image.
- Every agent with `registryConnectionId` uses an image with an explicit registry host and repository, whether authored through the init flag, a manifest, or declarative `azure.yaml`; deploy enforces this contract before calling Foundry.
- Core parses the configured passthrough image before creating its artifact.
- `docker.imagePassthrough: true` has a top-level service `image`.
- `docker.imagePassthrough` is not combined with `docker.remoteBuild`.
- Core container Publish rejects an image override when image passthrough is enabled.
- An external connection exists by short name or ID when init has an existing selected project available for verification.
- A matching sibling service has `host: azure.ai.connection`.
- A matching sibling connection appears in the agent's `uses`.
- A matching sibling connection is not disabled by its deployment condition.
- `registry_connection_id` is serialized only under `definition.container_configuration`.
- An empty connection value is omitted from the REST request.

### azd does not validate

- Registry vendor or hostname.
- A vendor or hostname allowlist.
- Whether a particular registry is enabled by the Foundry service.
- Vendor-specific required credential keys.
- The meaning of arbitrary `body.*` fields.
- Audience, issuer, subject, token endpoint, or token-exchange semantics.
- Vendor-side OIDC provider configuration.
- Entra audience application configuration.
- Registry-side identity mappings or repository pull permissions.
- Whether the image can be pulled with the resulting service token.

These checks belong to the registry and Foundry service. Their errors surface during connection use, image retrieval, or hosted-agent activation.

## Security and trust boundaries

The local azd process does not authenticate to the private registry in the image-passthrough flow. It does not request a vendor token, run `docker login`, inspect the image manifest, or transfer image layers.

The developer or administrator owns the registry-side OIDC provider, subject and identity mappings, repository permissions, image publication, and any vendor-specific policy. azd does not create these resources.

The developer or administrator also owns the Entra audience application required by the selected token-exchange design. azd does not create the application, choose its audience, or establish federation between Entra and the registry.

The `azure.ai.connection` service stores the generic service contract used by Foundry. azd passes declared credentials and metadata to the Foundry project connection without assigning vendor semantics. Sensitive values should be supplied through azd environment substitution rather than committed directly to `azure.yaml`.

Foundry owns the hosted-agent service identity, connection resolution, token exchange, registry access attempt, image pull, and workload startup. The registry independently evaluates the presented identity and authorizes or denies repository access.

A connection short name or resource ID is configuration, not proof that the connection is safe or correctly scoped. Init's existence check confirms only that the selected project exposes a matching connection. It does not attest to the connection's credentials, trust relationships, or permissions.

No registry credential is placed in the hosted-agent REST payload. The payload contains the image reference and Foundry connection reference; credential material remains behind the Foundry connection boundary.

## Registry neutrality

Production behavior treats the connection as an opaque Foundry project connection reference. The implementation does not branch on image hostname, connection metadata, registry vendor, token endpoint, or credential key names.

The generic connection provider continues to pass arbitrary credential keys through unchanged. A new registry vendor supported by the same Foundry service contract can therefore be configured through a pre-created external connection or declarative `azure.yaml` without an azd production-code change.

JFrog-specific names and setup instructions belong only in examples, test fixtures, and validation documentation. They must not be introduced into schemas, command validation, REST mapping, dependency logic, or core image-passthrough handling.

## Documentation and telemetry

User documentation should distinguish the two workflows: a pre-created external connection on an existing Foundry project, with no provision after init, and declarative sibling connection provisioning, which requires provision before deploy.

Documentation must state that azd does not configure vendor OIDC trust or create the Entra audience application. Vendor setup documentation may be linked as a prerequisite but must not be presented as azd-managed infrastructure.

No image name, registry hostname, connection credential, audience, token endpoint, provider name, or vendor name should be added to telemetry. Existing command and lifecycle telemetry can report success or failure without recording registry-specific configuration.

## References

- [Issue #9582: Support private non-ACR registry connections for hosted agents](https://github.com/Azure/azure-dev/issues/9582).
- [PR #9586: Support private non-ACR registry connections](https://github.com/Azure/azure-dev/pull/9586).
- [PR #9588: Add container image passthrough](https://github.com/Azure/azure-dev/pull/9588).
- [Public Foundry service API contract: `container_configuration.registry_connection_id`](https://github.com/Azure/azure-rest-api-specs/pull/44915).
- [Internal Foundry service design](https://msdata.visualstudio.com/Vienna/_git/vienna/pullrequest/2207414) — **internal Microsoft link; not publicly accessible**.
- [JFrog OIDC configuration API](https://docs.jfrog.com/administration/reference/createoidcconfiguration) and [identity mapping API](https://docs.jfrog.com/administration/reference/createoidcidentitymapping) — vendor setup references for the JFrog end-to-end example only.
