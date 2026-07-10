<!-- cspell:ignore azd azdext Foundry grpcserver nextstep noninteractive -->

# Unified azure.yaml configuration and core next-step guidance

## Context and scope

This design implements the configuration migration in [issue #8710](https://github.com/Azure/azure-dev/issues/8710) and restores the missing guidance after `azd provision` described in [issue #8804](https://github.com/Azure/azure-dev/issues/8804).

The scope is limited to the Azure AI Agents extension's configuration readers, next-step guidance, and doctor checks, plus the core contract needed to render extension guidance. Foundry provisioning, agent deployment, and manifest content are unchanged.

## Part 1: End-to-end experience

### One configuration source

`azd ai agent init` writes the agent definition and related `azure.ai.project`, `azure.ai.connection`, and `azure.ai.toolbox` services into `azure.yaml`. The agent's `uses` entries preserve dependencies between these services. Generated `agent.yaml` and retained `agent.manifest.yaml` files are no longer required for guidance or doctor checks.

An `azure.ai.project` service with `endpoint` continues to represent an existing Foundry project. Reusing a project does not transfer ownership or change `azd down` behavior.

### Terminal experience

After a successful interactive `azd provision`, core azd renders the extension contribution after its existing command result. Colors are omitted below, and values in angle brackets are dynamic.

```text
SUCCESS: Your application was provisioned in Azure in <duration>.
You can view the resources created under the resource group <resource-group> in Azure Portal:
<portal-url>

Next:
  azd deploy
  deploy the agent to Azure
```

If required state is missing, the block shows the fix first, such as `azd env set SEARCH_KEY <value>`, and keeps `azd deploy` as the final action. After deploy, the same top-level block suggests `azd ai agent show` and `azd ai agent invoke` for the deployed services.

The block is suppressed for JSON, redirected, and noninteractive output. A failed command never prints success guidance. If optional guidance assembly fails after a successful command, azd keeps the successful exit status and records the failure only in debug output.

### Existing projects

Unified projects continue without warnings. During the compatibility window, deployment may still read the legacy files through `project.LoadAgentDefinition`, but next-step guidance and doctor use `azure.yaml` as the source of truth. Legacy fallback emits the existing migration warning and directs the user to rerun `azd ai agent init`.

## Part 2: Technical design

### Authoritative service graph

Initialization already writes inline agent properties through `project.AgentDefinitionToServiceProperties` in `internal/project/agent_definition.go` and sibling resources through `emitResourceServices` in `internal/cmd/resource_services.go`. `project.AgentDefinitionFromService`, `project.LoadServiceTargetAgentConfig`, and `project.ServiceConfigProps` remain the canonical readers, including compatibility with config-nested service properties.

### State and doctor

Refactor `nextstep.AssembleState` in `internal/cmd/nextstep/state.go` to read protocols, environment references, placeholders, model deployments, connections, and toolboxes from the parsed service graph. Resource ownership follows `uses` edges and remains deterministic for shared resources. This replaces the file readers in `state.go` and `manifest.go`.

Doctor continues to consume the same state snapshot. Its project, toolbox, and connection checks validate inline agent properties and sibling resource services rather than requiring `agent.yaml` or `agent.manifest.yaml`.

### Core-rendered guidance

`azdext.ProjectEventHandler` currently returns only `error`, and `event_service.go` transports only completion or failure. Add a structured successful result that can carry ordered post-command contributions. Core collects these contributions, renders them once after the command result, and applies output-mode suppression.

The agents extension's `postprovisionHandler` clears `AI_AGENT_PENDING_PROVISION`, assembles refreshed state, and returns the resolved guidance. It does not write directly to stdout. Deploy contributes guidance for each deployed agent, and core aggregates it into one block. `augmentDeployNote` no longer embeds workflow guidance in an endpoint artifact note.

### Compatibility and failures

Malformed unified configuration produces a service-scoped validation or doctor error instead of being treated as an empty configuration. Legacy fallback keeps the single warning path in `project.WarnLegacyAgentShape`. Contribution failures remain diagnostic and do not change a completed command's result. No new telemetry is required for the first implementation.

## Part 3: Dependencies that need PM confirmation

1. **Core output contract:** Confirm that extension-contributed post-command output is in scope for this milestone. A clean post-provision block for issue #8804 depends on it.
2. **Post-deploy presentation:** Confirm that the top-level block replaces workflow guidance in endpoint artifact notes while endpoint-specific documentation remains.
3. **Legacy support window:** Confirm the release when `agent.yaml` and `agent.manifest.yaml` compatibility can be removed.

## Part 4: Open questions

1. What contribution shape prevents duplicate guidance when both project and service events run during `azd up`?
2. Should post-provision guidance include `azd ai agent run` as a secondary local-development action, or only `azd deploy`?
3. How should doctor attribute a shared or indirectly referenced resource to agent services?
4. Should infrastructure-variable classification continue to inspect Bicep outputs, or move into the project service contract?

## Summary of required changes

- **Unified state:** Replace next-step manifest and agent-file readers with parsed `azure.yaml` service-graph readers.
- **Doctor:** Validate inline agent definitions and sibling project, connection, and toolbox services.
- **Core events:** Transport and collect structured post-command contributions from extension lifecycle handlers.
- **Core output:** Render one contribution block after the command result and suppress it for machine-oriented output.
- **Extension lifecycle:** Return post-provision and aggregated post-deploy guidance without direct terminal writes or artifact-note workflow content.
- **Tests:** Cover unified state, legacy fallback, doctor results, terminal ordering, multi-agent aggregation, and output suppression.
