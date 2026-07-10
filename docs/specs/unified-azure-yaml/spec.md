<!-- cspell:ignore azd azdext Bicep Foundry grpcserver nextstep noninteractive OpenAPI toolboxes Unmarshal -->

# Unified azure.yaml configuration and core next-step guidance

## Context and scope

This design implements the configuration migration described by
[issue #8710](https://github.com/Azure/azure-dev/issues/8710). It also
addresses the missing guidance between `azd provision` and `azd deploy`
described by [issue #8804](https://github.com/Azure/azure-dev/issues/8804).

The design covers the Azure AI Agents extension's configuration readers,
state-aware guidance, and doctor checks. It does not redesign Foundry
provisioning, agent deployment, or the content of individual agent manifests.

## Part 1: End-to-end experience

### New project initialization

`azd ai agent init` writes the complete deployable agent and Foundry resource
configuration into `azure.yaml`. It does not require a generated `agent.yaml`
or a retained `agent.manifest.yaml` for later guidance or doctor checks.

```yaml
services:
  travel-agent:
    host: azure.ai.agent
    project: src/travel-agent
    uses:
      - travel-project
      - search-connection
      - travel-tools
    kind: hosted
    name: travel-agent
    protocols:
      - protocol: responses
    environmentVariables:
      - name: SEARCH_KEY
        value: ${SEARCH_KEY}
    container:
      resources:
        cpu: "0.5"
        memory: 1Gi

  travel-project:
    host: azure.ai.project
    deployments:
      - name: chat
        model:
          name: gpt-4o
          format: OpenAI
          version: "2024-11-20"
        sku:
          name: GlobalStandard
          capacity: 1

  search-connection:
    host: azure.ai.connection
    name: search
    category: AzureAISearch
    target: https://example.search.windows.net
    authType: ApiKey

  travel-tools:
    host: azure.ai.toolbox
    name: travel-tools
    tools: []
```

The agent's `uses` entries preserve the resource ordering. The project service
is provisioned before connections and toolboxes, and those resources are ready
before the agent deploys. Existing Foundry projects use `endpoint` on the
`azure.ai.project` service instead of creating a second project.

### First provision

After a successful interactive `azd provision`, core azd prints one
consolidated terminal block after its command summary. The extension contributes
the suggestions, but core owns rendering and ordering.

```text
Next:
  azd deploy
  deploy the agent to Azure
```

If state still needs user input, the block names that prerequisite rather than
suggesting an action that will fail. For example, an unresolved environment
value remains actionable after a partial configuration:

```text
Next:
  azd env set SEARCH_KEY <value>
  referenced by azure.yaml but not set in azd env

  azd deploy
  deploy the agent to Azure
```

The block is omitted for JSON output, redirected output, and noninteractive
output. Scripts retain their existing stable machine-readable output.

### Deploy and inner loop

After `azd deploy`, core prints one top-level block for all deployed agent
services. It replaces the current behavior where guidance is visible only in a
single endpoint artifact note.

```text
Next:
  azd ai agent show travel-agent
  verify it is running

  azd ai agent invoke travel-agent '<payload>'
  test the deployment
```

`azd ai agent init`, `run`, `invoke`, `show`, and a successful `doctor`
continue to use the same resolver. They derive agent protocol,
environment-variable references, unresolved placeholders, model deployments,
connections, and toolboxes from the parsed `azure.yaml` service graph. A
sample payload from OpenAPI or a sibling README remains optional enrichment,
not configuration input.

### Existing projects and migration

Projects that already have unified service entries continue without a warning.
The readers support both flat service properties and the deprecated
config-nested service properties through `project.ServiceConfigProps`.

Projects whose guidance or doctor result still depends on an on-disk
`agent.yaml` or `agent.manifest.yaml` receive one migration warning with the
existing migration guide. The warning directs the user to rerun
`azd ai agent init`. During the compatibility window, deployment can retain
the existing `project.LoadAgentDefinition` fallback, but next-step and doctor
state do not treat legacy files as authoritative.

### Reuse, teardown, and failure

An `azure.ai.project` service with `endpoint` represents a reused Foundry
project. Guidance and doctor checks inspect its declared sibling resources but
do not infer ownership from the endpoint or recommend deletion. `azd down`
continues to follow the resource lifecycle ownership already defined by the
project, connection, toolbox, and agent service targets.

If provision succeeds but the extension cannot assemble its optional state,
core still reports provision success and omits the contributed block. If the
extension returns a structured contribution error, core logs it only in debug
diagnostics and does not change the completed command's exit status. A later
`azd ai agent doctor` reconstructs state from `azure.yaml`, so a rerun is safe
and does not depend on a partially written manifest file.

## Part 2: Technical design

### Configuration source and migration boundary

`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go` already converts the
agent template with `project.AgentDefinitionToServiceProperties` and writes it
as `ServiceConfig.AdditionalProperties`. It then calls `emitResourceServices`
in `internal/cmd/resource_services.go` to create:

- one `azure.ai.project` service that owns `deployments`
- one `azure.ai.connection` service for each connection
- one `azure.ai.toolbox` service for each toolbox
- `uses` edges from the agent service to those siblings

The migration completes this model by making all state consumers read the same
parsed service graph. `project.AgentDefinitionFromService` and
`project.LoadServiceTargetAgentConfig` in
`internal/project/agent_definition.go` are the canonical readers for agent
definition fields and agent-specific settings. `ServiceConfigProps` preserves
the config-nested compatibility path without adding another reader.

### State assembly

Replace the file-oriented portions of `nextstep.AssembleState` in
`internal/cmd/nextstep/state.go` with service-graph collectors:

1. Keep `collectServices` as the filtered and sorted list of
   `host: azure.ai.agent` services.
2. Replace `loadServiceProtocol` with a reader that obtains `Protocols` from
   `project.AgentDefinitionFromService`.
3. Replace `detectMissingVars` with a reader of the inline
   `EnvironmentVariables` in `AgentDefinitionInline`. It preserves the
   existing `${VAR}` and `${VAR:-default}` semantics and detects literal
   `{{NAME}}` placeholders in the same values.
4. Replace `populateManifestResources` in
   `internal/cmd/nextstep/manifest.go` with a collector over
   `azure.ai.project`, `azure.ai.connection`, and `azure.ai.toolbox` services.
   It must use `project.UnmarshalStruct` with the corresponding
   `ServiceTargetAgentConfig`, `Connection`, and `Toolbox` types.
5. Preserve deterministic ordering and the `(ServiceName, Name)` identity
   semantics of `ResourceRef`. For a resource sibling, `ServiceName` is the
   owning agent reached through its `uses` edge. A shared resource is reported
   for every consuming agent, not silently attributed to one of them.

The collector must reject malformed service properties as a state-assembly
diagnostic rather than silently interpreting a malformed unified entry as no
resources. Optional OpenAPI and README lookups remain best effort because they
only improve command examples.

### Guidance resolution and rendering

`nextstep.ResolveAfterInit` and `nextstep.ResolveAfterDeploy` remain pure
functions over `nextstep.State`. Their decision trees do not depend on file
names after this migration. `nextstep.PrintNext`, `PrintAllNext`, and
`FormatNextForNote` remain formatters, with one behavioral change: endpoint
artifact notes are no longer the primary post-deploy surface.

Create a core extension result contract for project lifecycle events. Today,
`azdext.ProjectEventHandler` in `cli/azd/pkg/azdext/event_manager.go` returns
only `error`, and `eventService.createProjectEventHandler` in
`cli/azd/internal/grpcserver/event_service.go` converts it only to completion
or failure status. The extension must not write directly to stdout from
`postprovisionHandler`, because that output can interleave with the core
progress UI.

The core contract needs a structured, ordered post-command contribution that:

- is returned by a successful project or service event handler
- is collected for the encompassing command
- is rendered by core after the final command summary
- is suppressed for JSON and noninteractive output
- carries a source identifier for debug diagnostics
- treats rendering failures as diagnostic, not command failures

The agents extension's `postprovisionHandler` in
`internal/cmd/listen.go` first clears `AI_AGENT_PENDING_PROVISION` after a
successful provision. It then assembles the refreshed state and contributes
the post-provision resolver output through the new contract. No direct
terminal write is added to the handler.

For deploy, `AgentServiceTargetProvider.Deploy` in
`internal/project/service_target_agent.go` currently calls
`augmentDeployNote`, which scopes `ResolveAfterDeploy` to one service and
embeds the result in `Artifact.Metadata["note"]`. Replace that embedding with
a structured contribution collected across deployed services. The final
resolver invocation receives the full deployed service set once, so multi-agent
projects produce one ordered block rather than repeated per-service blocks.
The static endpoint documentation note remains an endpoint artifact detail and
does not carry the navigation workflow.

### Doctor

`azd ai agent doctor` retains `nextstep.AssembleState` as its shared snapshot
source. Update `local.agent-yaml-valid` in
`internal/cmd/doctor/checks_project.go` to validate an inline agent definition
with `project.AgentDefinitionFromService` and the existing
`agent_yaml.ValidateAgentDefinition` rules. It no longer requires an
`agent.yaml` file per service.

Update `local.toolboxes` and `remote.connections` in
`internal/cmd/doctor/checks_toolboxes.go` and
`internal/cmd/doctor/checks_connections.go` to use the unified resource
references. Their status names, skip conditions, details payloads, and
remediation text must refer to declared `azure.yaml` services, not manifest
resources. `resolveDoctorTrailing` in `internal/cmd/doctor.go` continues to
select deploy guidance for deployed services and init guidance otherwise.

### Errors, compatibility, and observability

Malformed inline configuration returns the existing structured validation error
shape, including the service name and a suggestion to regenerate or fix its
`azure.yaml` entry. Missing legacy files are not an error for unified projects.
When compatibility fallback is used, `project.WarnLegacyAgentShape` remains the
single warning path.

No telemetry event is required for the first implementation. The core result
contract should record extension source and rendering suppression in debug
logs. A later telemetry proposal must follow the event and field requirements
in `cli/azd/AGENTS.md`.

## Part 3: Dependencies that need PM confirmation

1. **Core lifecycle output contract:** Confirm that the core framework change
   needed for extension-contributed terminal guidance is in scope for this
   milestone. Issue #8804 cannot deliver a clean post-provision block without
   it.
2. **Post-deploy presentation:** Confirm that the top-level post-deploy block
   replaces the workflow content in endpoint artifact notes, while retaining
   endpoint-specific documentation. This resolves the choice identified in
   issue #8804.
3. **Legacy-file support window:** Confirm the release and support policy for
   `agent.yaml` and `agent.manifest.yaml` after unified initialization. The
   extension needs a dated removal target before compatibility readers can be
   deleted.
4. **Ownership of shared resources:** Confirm whether a toolbox or connection
   referenced through multiple `uses` edges should appear once per consumer or
   once per resource in doctor remediation. This design proposes one result per
   consumer so each agent remains actionable.

## Part 4: Open questions

1. What is the supported core result shape for aggregating contributions from
   both project and service lifecycle events without duplicating a block during
   `azd up`?
2. Should a successful `azd provision` include `azd ai agent run` as a
   secondary local-development suggestion, or should it show only the required
   `azd deploy` progression?
3. When an agent service has no direct `uses` edge to a resource because a user
   authored an indirect graph, should doctor attribute the resource to every
   reachable agent or display the resource service key without an agent owner?
4. Should unified state classification continue to inspect Bicep outputs for
   infrastructure variables, or should the project service expose that
   classification directly?

## Summary of required changes

- `cli/azd/extensions/azure.ai.agents/internal/cmd/nextstep/state.go`: assemble
  agent protocols, variable references, placeholders, and resource references
  from parsed `azure.yaml` service properties.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/nextstep/manifest.go`:
  replace manifest walking with deterministic unified service-graph collection.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/nextstep/types.go`: update
  resource-reference documentation and any fields needed for `uses` ownership.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/doctor/checks_project.go`:
  validate inline agent definitions rather than on-disk `agent.yaml` files.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/doctor/checks_toolboxes.go`:
  inspect unified toolbox services and update user-facing results.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/doctor/checks_connections.go`:
  inspect unified connection services and update user-facing results.
- `cli/azd/extensions/azure.ai.agents/internal/cmd/listen.go`: contribute
  post-provision suggestions after clearing pending-provision state.
- `cli/azd/extensions/azure.ai.agents/internal/project/service_target_agent.go`:
  replace per-artifact deploy guidance with an aggregated contribution.
- `cli/azd/pkg/azdext/event_manager.go`: add a structured lifecycle result that
  can carry post-command contributions.
- `cli/azd/internal/grpcserver/event_service.go`: transport lifecycle
  contributions from extensions to core.
- Core command output pipeline: collect and render contributions after command
  summaries while preserving JSON and noninteractive behavior.
- Unit and integration tests: cover unified state collection, compatibility
  warnings, doctor output, multi-agent aggregation, output suppression, and
  core rendering order.
