<!-- cspell:ignore azd brownfield greenfield idempotency -->

# Foundry project service ownership

## Summary

[Issue #9085](https://github.com/Azure/azure-dev/issues/9085) moves ownership
of the `azure.ai.project` service from `azure.ai.agents` to
`azure.ai.projects`.

Provisioning ownership has already moved. The remaining work is init-time
ownership: selecting a Foundry project, selecting its model deployment, and
creating or updating its `azure.yaml` service.

This spec proposes:

- Add `azd ai project init` for standalone Foundry project setup.
- Keep `azd ai agent init` as a one-command experience.
- Have agent init call the projects-owned setup internally.
- Keep the current `azure.ai.project` schema for this migration.
- Preserve existing projects and pre-split `azure.yaml` files.

## Current status

| Area | Status |
| --- | --- |
| Separate `azure.ai.project` service | Complete |
| Project service target in `azure.ai.projects` | Complete |
| `microsoft.foundry` provisioning provider migration | Complete in [PR #9133](https://github.com/Azure/azure-dev/pull/9133) |
| Standalone project authoring command | Not started |
| Project setup migration out of agents | Not started |
| Agent-to-project init handoff | Not started |
| Removal of duplicated project and IaC setup code | Not started |

## Goals

- Let users configure a Foundry project without installing or initializing an
  agent.
- Make `azure.ai.projects` the only extension that writes
  `host: azure.ai.project`.
- Preserve the current first-run experience for agent users.
- Make repeated initialization safe and predictable.
- Keep existing automation working during the migration.
- Make project ownership clear in commands, help, errors, and documentation.

## Non-goals

- Changing the behavior of `azd provision`, `azd deploy`, or `azd up`.
- Solving shared-resource deletion tracked by
  [issue #6215](https://github.com/Azure/azure-dev/issues/6215).
- Moving connection, toolbox, skill, or routine authoring into projects.
- Redesigning the full Foundry infrastructure schema.
- Automatically converting every legacy manifest on first use.

## Part 1: End-to-end experience

### Project command surface

`azd ai project` has two kinds of commands:

| Command | Purpose |
| --- | --- |
| `azd ai project set <endpoint>` | Set a user-level default endpoint for commands that need a Foundry project |
| `azd ai project show` | Show the resolved default endpoint and its source |
| `azd ai project unset` | Remove the user-level default endpoint |
| `azd ai project init` | Add or update a Foundry project in the current azd workspace |

`project set` keeps its current meaning. It does not start changing
`azure.yaml`. A separate `init` command avoids surprising users who only want
to set their default runtime context.

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
? Configure a model deployment? Yes
? Select a model:
```

Standalone project init does not select or recommend a model unless the user
chooses model setup or passes `--model`. Agent init can still request models
required by the selected agent template.

The command creates:

```yaml
infra:
  provider: microsoft.foundry

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

If no azd project or environment exists, the command creates the minimum
workspace state needed to write this configuration.

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
internally when the workspace does not already have a Foundry project.
Project and model prompts remain part of the same flow.

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

- `azure.ai.projects` creates or updates `ai-project`.
- `azure.ai.agents` creates or updates `support-agent`.
- Agents only records that it uses the project service.

If the user already ran `azd ai project init`, agent init reuses that service
and skips project selection unless the agent needs an additional model.

### Non-interactive setup

New project without model selection:

```bash
azd ai project init --no-prompt
```

New project with a model:

```bash
azd env set AZURE_SUBSCRIPTION_ID <subscription-id>
azd env set AZURE_LOCATION <location>
azd ai project init --no-prompt --model <model-name>
```

Existing project and existing deployment:

```bash
azd ai project init \
  --no-prompt \
  --project-id "<project-resource-id>" \
  --model-deployment <deployment-name>
```

Missing required values produce an actionable error. The command does not
prompt or silently choose a different project in `--no-prompt` mode.

### Repeated initialization

Project init is idempotent: it updates the existing project service in place,
preserves existing service metadata and unrelated deployments, and leaves
identical configurations unchanged. It requires confirmation before switching
to a different existing project and fails when more than one project service
exists. The detailed merge rules are defined in [Update behavior](#update-behavior).

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

Agents forwards them to project setup and displays a deprecation notice with
the equivalent `azd ai project init` command. Plain `azd ai agent init` does
not display a warning because it remains a supported experience. Removing the
flags is outside this feature and requires a separately announced compatibility
change.

### Failure and retry

Project setup validates the requested project and model before updating the
service. If setup fails during agent init, the agent service is not added.
Downloaded source files remain available, and rerunning init safely resumes
the setup.

### User-visible impact

| User | Before | After |
| --- | --- | --- |
| Project-only user | Must hand-edit `azure.yaml` or go through agent init | Can run `azd ai project init` |
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
| Model deployment selection and project configuration | `azure.ai.projects` |
| `azure.ai.project` service creation and update | `azure.ai.projects` |
| Project IaC generation | `azure.ai.projects` |
| Agent source, runtime, protocol, and deployment mode | `azure.ai.agents` |
| `azure.ai.agent` service creation and update | `azure.ai.agents` |
| Agent `uses` relationship to the project | `azure.ai.agents` |

The main code movement is from the project setup paths in
`cli/azd/extensions/azure.ai.agents/internal/cmd/` into
`cli/azd/extensions/azure.ai.projects/internal/cmd/`.

`NewRootCommand` in the projects extension registers the new init command.
`emitResourceServices` in agents stops creating the project service and only
locates the resulting project service by host and adds its key to the agent's
`uses`.

### `azure.ai.project` content

No new service property is required for this migration. The current schema in
`cli/azd/extensions/azure.ai.projects/schemas/azure.ai.project.json` already
contains:

- `endpoint` for an existing project.
- `deployments` for model deployments.
- `network` for private networking.
- `$ref` support for reusable deployment definitions.

The existing project ARM ID remains in `AZURE_AI_PROJECT_ID`. We do not add a
`resourceId` field to `azure.yaml` in this migration because it would create
two persisted sources for project identity.

### Agent-to-project handoff

Agent init passes the project requirements it discovered to
`azd ai project init` through `WorkflowService.Run`. Projects performs the
selection and update, writes the project service, and persists shared project
values in the azd environment.

The workflow service reports completion rather than returning command data.
After it completes, agents finds the single `azure.ai.project` service by host
and reads any shared project values from the environment. `--output json`
remains a user-facing summary and is not the extension handoff contract.
Coordinated extension versions ensure a missing or incompatible project init
command fails with upgrade guidance instead of silently ignoring project
settings.

### Update behavior

Projects owns an idempotent merge rather than replacing the full service.

The merge rules are:

- Create a service only when none exists.
- Reuse an existing project service key. For a new service, use the sanitized
  project name when available and collision-free; otherwise use `ai-project`.
- Update only project-owned fields.
- Preserve fields not involved in the current request.
- Match deployments by deployment name.
- Require confirmation for a conflicting project or deployment.
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

1. Add project init and project-owned update behavior.
2. Switch agent init to the internal handoff while keeping compatibility
   flags.
3. Remove duplicated project setup and IaC code from agents after the
   coordinated extensions are available.

`azure.ai.agents` must depend on a version of `azure.ai.projects` that includes
the init contract. The `microsoft.foundry` meta-package must update both
versions together.

The sibling resource ownership issues migrate independently and do not gate
this rollout.
