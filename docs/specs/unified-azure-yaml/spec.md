<!-- cspell:ignore azd azdext Foundry nextstep postdeploy postprovision -->

# Unified azure.yaml configuration and core next-step guidance

## Context and scope

This design aligns the end-to-end CLI experience for the configuration migration in [issue #8710](https://github.com/Azure/azure-dev/issues/8710) and the local-first guidance from [issue #7975](https://github.com/Azure/azure-dev/issues/7975). It covers the Azure AI Agents extension's configuration readers, next-step guidance, doctor checks, and the core contribution contract. Foundry provisioning, agent deployment, and manifest content are unchanged.

## Part 1: End-to-end experience

### One configuration source

`azd ai agent init` writes the agent definition and related `azure.ai.project`, `azure.ai.connection`, and `azure.ai.toolbox` services into `azure.yaml`. An `azure.ai.project` service with `endpoint` still represents an existing Foundry project without changing ownership or `azd down` behavior.

Next-step and doctor prefer the unified `azure.yaml` service graph. During the compatibility window, they retain the existing `agent.yaml` and `agent.manifest.yaml` fallback and migration warning. Other legacy readers are not removed by this design.

### Terminal experience

After successful provisioning and any required fixes, the primary action starts the local agent. Deployment remains the trailing action for when the user is ready.

```text
SUCCESS: Your application was provisioned in Azure in <duration>.

Next:
  azd ai agent run
  run the agent locally

When ready:
  azd deploy
  deploy the agent to Azure
```

Core renders guidance after the command result for human-facing terminal output. JSON, non-TTY, and other machine-oriented output suppress it. `--no-prompt` alone does not. Failed commands do not print success guidance.

## Part 2: Technical design

### Unified state and attribution

`nextstep.AssembleState` reads the parsed service graph first and falls back to legacy agent files only when unified configuration is unavailable. Doctor consumes the same state snapshot. Both continue classifying unresolved infrastructure variables by matching Bicep outputs, consistent with [issue #7975](https://github.com/Azure/azure-dev/issues/7975) and [PR #9081](https://github.com/Azure/azure-dev/pull/9081).

Each resource service is attributed to its own `azure.yaml` service key and checked once, including shared resources. `uses` defines dependency ordering, not ownership. Inline and legacy resources remain attributed to the agent service key.

### Core-rendered guidance

Core accepts one project-scoped post-command contribution with a stable identity. Post-provision writes or updates that contribution after refreshing state. Project post-deploy recomputes and replaces it, so `azd up` renders only the final post-deploy guidance. Service postdeploy handlers do not contribute user guidance.

The contribution is structured rather than direct stdout. Core renders it once after the command result and applies output-mode suppression. Contribution assembly failures remain diagnostic and do not change a successful command result.

Malformed unified configuration produces a service-scoped validation or doctor error instead of an empty state. Legacy fallback keeps the single warning path in `project.WarnLegacyAgentShape`.

## Decisions for John

1. **Core output contract:** Decide whether the project-scoped post-command contribution contract is in this milestone.
2. **Post-deploy presentation:** Decide whether top-level post-deploy guidance replaces workflow guidance in endpoint artifact notes while endpoint-specific documentation remains.
