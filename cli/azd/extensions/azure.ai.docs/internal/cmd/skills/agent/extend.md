---
short: Edit the on-disk agent.yaml (env vars, endpoint, card, runtime, container resources).
order: 25
---
# Extend: edit the on-disk `agent.yaml`

This topic covers the file at `<service-dir>/agent.yaml` only. Service-level config (model deployments, connections, toolboxes, tool resources) lives in `azure.yaml` -- see `configure`. Connection details live in `azd ai doc connection`.

## Two files, two schemas

After `azd ai agent init`, the agent is defined by two files. Putting a field in the wrong one is the single most common deploy failure.

| File                                  | What it holds                                                                                                |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `<service-dir>/agent.yaml`            | The flat `ContainerAgent`: `kind`, `name`, `protocols`, `environment_variables`, `agentEndpoint`, `agentCard`, `codeConfiguration`, `image`, container `resources` (cpu/memory). |
| `azure.yaml services.<name>.config`   | Model deployments, connections, toolboxes, tool resources, container settings, startup command. See `configure`. |
| `<env>/.env` (`azd env set`)          | Secrets and `PARAM_<CONN>_<KEY>` credential values referenced from `azure.yaml`.                             |

`agent.manifest.yaml` (the file passed to `azd ai agent init -m`) is **not** on disk after init. It's a seed format with a `template:` wrapper and outer `parameters:` / `resources:`. Init splits it across the three files above. Don't reintroduce the `template:` wrapper into the on-disk `agent.yaml` -- the deploy parser ignores it silently and your overrides won't apply.

## What lives where

| Want to ...                                                  | Edit                                                  | Topic              |
| ------------------------------------------------------------ | ----------------------------------------------------- | ------------------ |
| Change an env var the container reads                        | `agent.yaml` `environment_variables[]`                | this               |
| Edit the endpoint contract                                   | `agent.yaml` `agentEndpoint:`                         | this               |
| Edit the A2A agent card                                      | `agent.yaml` `agentCard:`                             | this               |
| Switch container vs. code deploy / pick a runtime            | `agent.yaml` `codeConfiguration:`                     | this               |
| Change container CPU / memory                                | `agent.yaml` `resources:` (cpu, memory)               | this               |
| Swap a model deployment                                      | `azure.yaml ... config.deployments[]`                 | `configure`        |
| Add / remove a connection                                    | `azure.yaml ... config.connections[]`                 | `connection add`   |
| Add a toolbox or tool inside one                             | `azure.yaml ... config.toolboxes[]` + `azd ai toolbox create/connection add` | `toolbox add`     |
| Wire a built-in tool needing a connection (bing/search)      | `azure.yaml ... config.resources[]`                   | `configure`        |
| Set a credential referenced as `${PARAM_...}`                | `azd env set PARAM_<CONN>_<KEY> <value>`              | `connection auth-types` |
| Patch endpoint / card without a full redeploy                | `agent.yaml`, then `azd ai agent endpoint update`     | `configure`        |

Edits to `agent.yaml` require a full `azd deploy` (creates a new immutable agent version). Edits to `azure.yaml`'s `config.connections[]` or `config.deployments[]` typically need `azd provision` first, then `azd deploy`.

## `kind:`

Two values deploy through this extension. Anything else fails validation.

| `kind:`    | When to use                                                       |
| ---------- | ----------------------------------------------------------------- |
| `hosted`   | Container-backed agent (Python / .NET / Node) running on Foundry. |
| `workflow` | Multi-step orchestration with a declarative `trigger:`. Preview.  |

`kind: prompt` (from raw AgentSchema docs) is **not** supported. Use `hosted` and put the system prompt in the agent's source.

## Hosted agent (`kind: hosted`)

The on-disk shape -- a flat `ContainerAgent`. Only `kind` and `name` are required.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/ContainerAgent.yaml
kind: hosted
name: my-agent
description: Answers questions about the docs corpus.
protocols:
  - protocol: responses
    version: "1.0.0"
  - protocol: invocations
    version: "1.0.0"
resources:
  cpu: "0.25"
  memory: "0.5Gi"
environment_variables:
  - name: AZURE_AI_MODEL_DEPLOYMENT_NAME
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
  - name: LOG_LEVEL
    value: info
codeConfiguration:
  runtime: python_3_13
  entryPoint: app.py
  dependencyResolution: remote_build   # or "bundled"
agentEndpoint:
  protocols: ["responses"]
  versionSelector:
    versionSelectionRules:
      - type: traffic
        agentVersion: "3"
        trafficPercentage: 100
  authorizationSchemes:
    - type: entra
agentCard:
  description: "What this agent does for users."
  skills:
    - id: answer-docs
      name: Answer documentation questions
      description: Cite a docs URL with each answer.
      examples:
        - "How do I rotate the API key?"
```

Key blocks:

* **`protocols:`** -- wire formats the agent serves. `responses` is the OpenAI Responses API; `invocations` is A2A invocations. Most agents advertise both. Editing requires a full redeploy.
* **`resources:`** -- container CPU / memory. Valid tiers: `0.25/0.5Gi`, `1/2Gi`, `2/4Gi`. Don't confuse this with the manifest's outer `resources[]` (which doesn't exist in this file).
* **`environment_variables:`** -- per-version env vars. Two reference forms:
  * `${VAR}` -- resolved from the active azd env at deploy time. Use this.
  * `{{VAR}}` -- resolved at init time from manifest `parameters:`. After init the placeholder is gone; don't reintroduce `{{...}}` here.
  * Not for secrets. Use a connection in `azure.yaml`.
* **`codeConfiguration:`** -- present means code deploy (ZIP upload). Required: `runtime` (`python_3_13`, `python_3_14`, `dotnet_10`, `node_22`) and `entryPoint`. Optional `dependencyResolution`: `remote_build` (default) or `bundled`. Absent means container deploy (needs a Dockerfile + `docker:` in azure.yaml).
* **`image:`** -- pre-built image reference (e.g. `myregistry.azurecr.io/myagent:v1`). When set, deploy can skip the Dockerfile build.
* **`agentEndpoint:`** -- traffic routing + protocols. Editing this block alone doesn't need a full redeploy -- use `azd ai agent endpoint update` (see `configure`).
* **`agentCard:`** -- A2A capability advertisement. Patched in-place by `endpoint update`, same as `agentEndpoint`.

## Workflow agent (`kind: workflow`) -- preview

```yaml
kind: workflow
name: nightly-report
trigger:
  schedule:
    cron: "0 3 * * *"
```

`trigger:` is free-form. See the AgentSchema docs for currently-supported trigger types.

## Validate

```bash
azd ai agent doctor --output json
```

Look for the `local.agent-yaml-valid` check; the failure message names the field path that failed.

Each successful deploy creates a new immutable agent version. Use `agentEndpoint.versionSelector` to route traffic.
