<!-- cspell:ignore azd brownfield greenfield idempotency -->

# Foundry project service ownership

## Summary

[Issue #9085](https://github.com/Azure/azure-dev/issues/9085) moves ownership
of the `azure.ai.project` service from `azure.ai.agents` to
`azure.ai.projects`.

Provisioning ownership has already moved. The remaining work is authoring
ownership: selecting a Foundry project, creating or updating its `azure.yaml`
service, and adding project-owned model deployment declarations without the
agents extension writing the project block.

This spec proposes:

- Add `azd ai project init` for standalone Foundry project setup.
- Add `azd ai project deployment add` for azd-managed model deployments.
- Keep `azd ai agent init` as a one-command experience.
- Have agent init call project setup and model deployment setup as separate
  projects-owned operations.
- Keep the current `azure.ai.project` schema for this migration.
- Preserve existing projects and pre-split `azure.yaml` files.

## Current status

| Area | Status |
| --- | --- |
| Separate `azure.ai.project` service | Complete |
| Project service target in `azure.ai.projects` | Complete |
| `microsoft.foundry` provisioning provider migration | Complete in [PR #9133](https://github.com/Azure/azure-dev/pull/9133) |
| Standalone project authoring command | Not started |
| Standalone model deployment authoring command | Not started |
| Project setup migration out of agents | Not started |
| Agent-to-project init handoff | Not started |
| Removal of duplicated project, deployment authoring, and IaC setup code | Not started |

## Goals

- Let users configure a Foundry project without installing or initializing an
  agent.
- Make `azure.ai.projects` the only extension that writes
  `host: azure.ai.project`.
- Preserve the current first-run experience for agent users.
- Keep project initialization separate from model deployment authoring.
- Make repeated initialization safe and predictable.
- Keep existing automation working during the migration.
- Make project ownership clear in commands, help, errors, and documentation.

## Non-goals

- Changing the behavior of `azd provision`, `azd deploy`, or `azd up`.
- Solving shared-resource deletion tracked by
  [issue #6215](https://github.com/Azure/azure-dev/issues/6215).
- Moving connection, toolbox, skill, or routine authoring into projects.
- Redesigning the full Foundry infrastructure schema.
- Designing a complete imperative model deployment lifecycle, such as list,
  show, or delete commands.
- Automatically converting every legacy manifest on first use.

## Part 1: End-to-end experience

### Project command surface

The project extension exposes runtime-context commands and workspace-authoring
commands:

| Command | Purpose |
| --- | --- |
| `azd ai project set <endpoint>` | Set a user-level default endpoint for commands that need a Foundry project |
| `azd ai project show` | Show the resolved default endpoint and its source |
| `azd ai project unset` | Remove the user-level default endpoint |
| `azd ai project init` | Add or update a Foundry project in the current azd workspace |
| `azd ai project deployment add` | Add or update an azd-managed model deployment declaration |

`project set` keeps its current meaning. It does not start changing
`azure.yaml`. A separate `init` command avoids surprising users who only want
to set their default runtime context.

Project init writes `azure.yaml` directly because a Foundry project is
foundational workspace infrastructure. This is an intentional exception to the
definition-file journey proposed for agents, toolboxes, connections, skills,
and routines. This migration does not introduce a standalone `project.yaml` or
an `apply` command.

`project deployment add` follows the existing
`<area> <collection> <verb>` pattern used for owned child collections, such as
`toolbox skill add`. A model deployment remains nested under its project rather
than becoming a separate `azure.yaml` service.

### Initialize a new Foundry project

The user runs:

```bash
azd ai project init
```

The command asks:

```text
? How should the Foundry project be configured?
  Create a new Foundry project
  Use an existing Foundry project

? Select an Azure subscription:
? Select an Azure location:
```

Project init does not select, recommend, or add a model deployment. A project
without deployments is valid. Agent init can still request models required by
the selected agent template, but a new deployment is added through the separate
deployment command.

The command creates:

```yaml
infra:
  provider: microsoft.foundry

services:
  ai-project:
    host: azure.ai.project
```

If no azd project or environment exists, the command creates the minimum
workspace state needed to write this configuration.

### Add a model deployment

To declare a new deployment that azd should provision and manage, the user
runs:

```bash
azd ai project deployment add --model <model-name>
```

The command resolves the model version, SKU, and capacity using the existing
model selection flow. The deployment name defaults to the model name and can be
overridden with `--name`.

The command adds the declaration to the existing project service:

```yaml
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: <deployment-name>
        model:
          format: OpenAI
          name: <model-name>
          version: <model-version>
        sku:
          name: <sku-name>
          capacity: <capacity>
```

`deployment add` only updates project configuration. The deployment is created
or updated during `azd provision` or `azd up`.

An existing deployment that azd does not manage is not added to
`azure.yaml`. A consumer such as agent init validates the deployment and
references it by name. This preserves the current boundary: new deployments
selected for provisioning are declared, while existing deployments remain
external resources.

### Use an existing Foundry project

Interactive users select an existing project from their subscription.
Automation can provide its ARM resource ID:

```bash
azd ai project init \
  --project-id "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
```

The resulting service includes the existing endpoint:

```yaml
services:
  my-project:
    host: azure.ai.project
    endpoint: https://<account>.services.ai.azure.com/api/projects/<project>
```

The command also saves `AZURE_AI_PROJECT_ID` and
`FOUNDRY_PROJECT_ENDPOINT` in the active azd environment. This gives later
operations the project identity they already expect today.

Users who only have an endpoint can run:

```bash
azd ai project init \
  --project-endpoint "https://<account>.services.ai.azure.com/api/projects/<project>"
```

Endpoint-only setup is supported when azd does not need to create or update
resources on the existing project. If resource reconciliation is needed
later, the user is asked for the full project ID.

### Initialize an agent

The user continues to run:

```bash
azd ai agent init
```

There is no required extra command. Agent init invokes project setup
internally when the workspace does not already have a Foundry project. When the
agent needs a model, it either validates and references an existing deployment
or invokes project deployment add for a new azd-managed deployment. Project and
model prompts remain part of the same user flow.

The result remains a composed `azure.yaml`:

```yaml
services:
  ai-project:
    host: azure.ai.project

  support-agent:
    host: azure.ai.agent
    project: src/support-agent
    uses:
      - ai-project
```

The difference is ownership:

- `azure.ai.projects` creates or updates `ai-project` and its managed deployment
  declarations.
- `azure.ai.agents` creates or updates `support-agent` and records any existing
  deployment reference.
- Agents adds the `uses` relationship but never writes the project service.

If the user already ran `azd ai project init`, agent init reuses that service
and skips project selection. If the agent needs an additional model, only the
deployment operation runs.

### Non-interactive setup

New project without model selection:

```bash
azd ai project init --no-prompt
```

New project with an azd-managed model deployment:

```bash
azd env set AZURE_SUBSCRIPTION_ID <subscription-id>
azd env set AZURE_LOCATION <location>
azd ai project init --no-prompt
azd ai project deployment add --no-prompt --model <model-name>
```

Existing project and existing deployment:

```bash
azd ai project init \
  --no-prompt \
  --project-id "<project-resource-id>"
azd ai agent init \
  --no-prompt \
  --model-deployment <deployment-name>
```

Missing required values produce an actionable error. These commands do not
prompt or silently choose a different project or deployment in `--no-prompt`
mode.

### Repeated initialization

Project init is idempotent: it updates the existing project service in place,
preserves existing service metadata and all deployments, and leaves identical
configurations unchanged. It requires confirmation before switching to a
different existing project and fails when more than one project service exists.

Project deployment add is independently idempotent. It leaves an identical
declaration unchanged, preserves unrelated deployments, and requires
confirmation before replacing a conflicting declaration with the same name.
The detailed merge rules are defined in [Update behavior](#update-behavior).

### Existing and legacy projects

Pre-split `azure.yaml` files continue to work. This is required by
[issue #9085](https://github.com/Azure/azure-dev/issues/9085).

When init encounters legacy project settings:

- Current provisioning compatibility remains in place.
- New initialization writes a dedicated `azure.ai.project` service.
- Legacy properties are not deleted automatically.
- The user sees one migration notice explaining which service is now
  authoritative.

This additive approach lets users update the agents and projects extensions
without requiring an immediate manifest rewrite. Ending this compatibility or
deleting legacy properties requires a separate migration proposal.

### Existing agent flags

The following agent init flags remain supported by this feature:

```text
--project-id
--model
--model-deployment
--infra
```

Agents forwards `--project-id` and `--infra` to project init. `--model`
invokes project deployment add when a new deployment is required.
`--model-deployment` validates and references an existing deployment without
adding it to project configuration. The project-authoring flags display a
deprecation notice with the equivalent project command. `--model-deployment`
remains an agent-specific input and does not point to a project authoring
command. Plain `azd ai agent init` does not display a warning because it remains
a supported experience. Removing the flags is outside this feature and requires
a separately announced compatibility change.

### Failure and retry

Project init validates the requested project before updating the service.
Project deployment add validates the model before updating the deployment
list. If either operation fails during agent init, the agent service is not
added. A project service successfully written before a later deployment failure
remains valid. Downloaded source files also remain available, and rerunning init
safely resumes the setup.

### User-visible impact

| User | Before | After |
| --- | --- | --- |
| Project-only user | Must hand-edit `azure.yaml` or go through agent init | Can run project init, then optionally add a deployment |
| Model deployment author | Must hand-edit `azure.yaml` or go through agent init | Can run `azd ai project deployment add` |
| New agent user | Runs `azd ai agent init` | Runs the same command |
| Existing agent automation | Passes project and model flags to agent init | Continues to work during migration |
| Existing-project user | Agent init stores endpoint and project ID | Projects-owned init stores the same values |
| Manifest author | Agents may rewrite project configuration | The projects extension is the only writer |

## Part 2: Feature design

### Ownership model

| Responsibility | Owner after migration |
| --- | --- |
| Foundry project selection | `azure.ai.projects` |
| Subscription and location selection for project setup | `azure.ai.projects` |
| `azd`-managed model deployment declaration | `azure.ai.projects` |
| `azure.ai.project` service creation and update | `azure.ai.projects` |
| Project IaC generation | `azure.ai.projects` |
| Agent model requirement and existing deployment reference | `azure.ai.agents` |
| Agent source, runtime, protocol, and deployment mode | `azure.ai.agents` |
| `azure.ai.agent` service creation and update | `azure.ai.agents` |
| Agent `uses` relationship to the project | `azure.ai.agents` |

The main code movement is from the project setup paths in
`cli/azd/extensions/azure.ai.agents/internal/cmd/` into
`cli/azd/extensions/azure.ai.projects/internal/cmd/`.

`NewRootCommand` in the projects extension registers the new init command.
It also registers the deployment command group and its add command.
`emitResourceServices` in agents stops creating the project service or adding
managed deployments. It only locates the resulting project service by host and
adds its key to the agent's `uses`.

### `azure.ai.project` content

No new service property is required for this migration. The current schema in
`cli/azd/extensions/azure.ai.projects/schemas/azure.ai.project.json` already
contains:

- `endpoint` for an existing project.
- `deployments` for model deployments.
- `network` for private networking.
- `$ref` support for reusable deployment definitions.

Project init does not modify `deployments`. Project deployment add is the
projects-owned command that merges one managed deployment declaration into the
list. Existing deployments referenced by a consumer are not persisted in this
list.

The existing project ARM ID remains in `AZURE_AI_PROJECT_ID`. We do not add a
`resourceId` field to `azure.yaml` in this migration because it would create
two persisted sources for project identity.

### Agent-to-project handoff

Agent init uses two projects-owned workflows through `WorkflowService.Run`:

1. It passes project requirements to `azd ai project init`. Projects performs
   project selection, writes the project service, and persists shared project
   values in the azd environment.
2. For each new deployment selected for azd management, it invokes
   `azd ai project deployment add`. Projects resolves and merges the deployment
   declaration. Selecting an existing deployment skips this operation because
   the agent references that deployment by name.

The workflow service reports completion rather than returning command data.
After the workflows complete, agents finds the single `azure.ai.project`
service by host and reads any shared project values from the environment.
`--output json` remains a user-facing summary and is not the extension handoff
contract. Coordinated extension versions ensure a missing or incompatible
project or deployment command fails with upgrade guidance instead of silently
ignoring project settings.

### Update behavior

Project init and project deployment add own separate idempotent merges rather
than replacing the full service.

The merge rules are:

- Project init creates a service only when none exists.
- Reuse an existing project service key. For a new service, use the sanitized
  project name when available and collision-free; otherwise use `ai-project`.
- Project init updates only the project fields involved in the request and
  never changes `deployments`.
- Require confirmation before project init switches to a different existing
  project.
- Project deployment add merges exactly one declaration by deployment name.
- Leave an identical deployment declaration unchanged.
- Require confirmation before replacing a conflicting declaration with the
  same name. Fail with actionable guidance in `--no-prompt` mode.
- Never add a deployment that the user selected as an existing external
  resource.
- Preserve fields not involved in the current request.
- Reject ambiguous projects before making changes.

These rules prevent repeated agent init runs from removing private networking,
custom hooks, or manually added project settings.

### Infrastructure ownership

Project IaC generation moves with project setup. The direct spelling becomes:

```bash
azd ai project init --infra
azd ai project init --infra=terraform
```

The agent command forwards its existing `--infra` flag during the migration.
Brownfield eject is not required for this ownership change and remains tracked
by [issue #9127](https://github.com/Azure/azure-dev/issues/9127) and draft
[PR #9348](https://github.com/Azure/azure-dev/pull/9348).

Project init never replaces an existing non-Foundry infrastructure provider.
Endpoint-only adoption that needs no ARM reconciliation leaves the provider
unchanged. If project setup requires `microsoft.foundry` while another provider
is configured, the command fails with guidance instead of overwriting it.
Multi-provider composition is separate work.

### Rollout

The migration ships in three steps:

1. Add project init, project deployment add, and their independent update
   behavior.
2. Switch agent init to the two internal handoffs while keeping compatibility
   flags.
3. Remove duplicated project setup, managed deployment authoring, and IaC code
   from agents after the coordinated extensions are available.

`azure.ai.agents` must depend on a version of `azure.ai.projects` that includes
the project init and deployment add contracts. The `microsoft.foundry`
meta-package must update both versions together.

The sibling resource ownership issues migrate independently and do not gate
this rollout.
