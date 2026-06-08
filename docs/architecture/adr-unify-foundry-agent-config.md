# ADR: Unify Foundry agent configuration in `azure.yaml`

**Status:** Proposed

**Date:** 2026-06-08

**Tracking issue:** [#7962](https://github.com/Azure/azure-dev/issues/7962)

### Context

The `azure.ai.agents` extension currently models a hosted Foundry agent across
**three files**: `azure.yaml`, `agent.yaml` (an `AgentDefinition`), and
`agent.manifest.yaml` (an `AgentManifest`). This creates several problems:

1. **Three files, overlapping data.** The agent name appears in three places,
   container resources in two, the model deployment name in three. Two
   templating syntaxes (`{{param}}` and `${ENV}`) overlap. Issue
   [#7901](https://github.com/Azure/azure-dev/issues/7901) is a symptom — `init`
   fails when run in a directory that already contains the manifest.
2. **Scope conflation.** `services.<agent>.config` mixes agent-scoped concerns
   (container resources, env, startup command) with project-scoped resources
   (model deployments, connections, toolboxes).
3. **No sharing across agents.** Project-scoped resources are nested under a
   single agent, so a second agent that wants the same toolbox must redeclare
   it. There is nowhere to say "this toolbox belongs to the project."
4. **Divergent tooling.** The Foundry Toolkit for VS Code parses `agent.yaml`
   directly while `azd ai agent` works off an `AgentManifest`; the experiences
   diverge.
5. **The manifest layer carries no weight.** `agent.manifest.yaml` was designed
   for an agent catalog that was never built. Its templating isn't paying for
   itself, and abstracting concrete values with `${ENV_VAR}` blurs the line
   between a definition and a template.

We want `azure.yaml` to be the single source of truth: it describes **what
exists in the Foundry project** and **how the agent runs**; agent code defines
**what the agent does**; the azd environment carries deployment-target values;
Bicep is opt-in for developers who need full IaC reproducibility.

### Decision

Consolidate all hosted-agent configuration into `azure.yaml` and split it by
scope across two host kinds.

1. **`host: azure.ai.agent`** describes the agent runtime only. Its `config:`
   block maps to the Foundry create-agent API: `kind`, `description`,
   `metadata`, `protocols`, `container.resources`, `env`, `startupCommand`. The
   project-scoped fields (`deployments`, `resources`, `toolConnections`,
   `toolboxes`, `connections`) are removed from this schema.
2. **`host: azure.ai.project`** (new) owns all project-scoped Foundry
   data-plane state that can't be modeled in ARM/Bicep — today toolboxes,
   connections, and model deployments; future additions (eval datasets, vector
   indexes, knowledge sources) go here too (`additionalProperties: true`). It is
   a service **without source code**: no `project:` directory, no build, no
   artifact. One `azure.ai.project` service per Foundry project.
3. **Agents reference the project via the existing `uses:` field**
   (`uses: [foundry-project]`). This is azd's existing inter-service dependency
   primitive on `ServiceConfig` — it orders deploys and injects the
   dependency's outputs as env vars. **No `dependsOn`, no core schema change.**
4. **Container mode reuses the existing `docker:` block** (`docker.path`,
   `docker.remoteBuild`) to build the image from a Dockerfile. An
   already-published image is referenced via the existing top-level `image:`
   field on `ServiceConfig` (e.g. `myregistry.azurecr.io/my-agent:v1`) — no
   build, no new field. No new top-level `dockerfile` field — that would
   conflict with the existing `DockerProjectOptions` on `ServiceConfig`.
5. **Code-deploy mode uses a typed `runtime: { stack, version }` block**,
   following the existing azure.yaml runtime precedent — not a bare string.
6. **Deploy source is explicit and exactly one of three.** `image:` present →
   use the existing pre-built image; `docker:` present → build the image from a
   Dockerfile; `runtime:` present → zip the project for code-deploy. Zero or
   more than one present → validation error. No silent defaults.
7. **`container.resources` (`cpu`/`memory`) applies to every deploy mode**, not
   just container mode. The Foundry create-agent API carries `cpu` and `memory`
   for both code-deploy and container/image agents; when omitted the extension
   applies defaults (today `cpu: "1"`, `memory: "2Gi"`). So `container.resources`
   stays in the agent `config:` regardless of whether `image:`, `docker:`, or
   `runtime:` is used.
8. **`${VAR}` env-var expansion** uses the same syntax `azure.yaml` already
   supports. The extension performs the expansion inside `config:` blocks
   (the core framework does not expand `config:`).
9. **`init` populates `config:`** the way it currently populates `agent.yaml`,
   and stops emitting `agent.yaml` / `agent.manifest.yaml`. A deprecation window
   keeps reading the old files (with a warning + telemetry) before removal.

**Phasing (locked):**

- **Phase 1** — Consolidate files. Retire `agent.yaml` / `agent.manifest.yaml`,
  move their content into `azure.yaml`, and ship the new `azure.ai.project`
  service target. Provisioning continues to use existing Bicep/ARM.
- **Phase 2** — Bicep-less provisioning: the extension carries built-in Bicep
  templates and generates ARM in memory during `azd provision` (similar to
  `azd compose`), with opt-in eject to Bicep on disk. Gated on a separate RFC
  (not yet filed) and on a real multi-agent sample that needs shared resources.

### Open Questions (to resolve before locking the schema)

This ADR records the agreed direction; the following provision/deploy boundary
questions (raised in the issue's framework review) must be answered before the
`azure.ai.project` schema is finalized:

1. **What exactly does `azure.ai.project` Deploy create vs. what stays in
   provision/Bicep?** Toolboxes are clearly data-plane (Deploy). Model
   deployments and connections are ambiguous — today deployments are serialized
   to env vars and consumed by Bicep, and connection creation involves
   ARM-level resources plus credential handling. We need a clear inventory of
   the provision (ARM) vs. deploy (Foundry API) split.
2. **Idempotency and state.** On repeated `azd deploy`, does the project target
   diff declared config against existing Foundry state and apply incremental
   changes, or recreate from scratch? How are removals handled (a toolbox
   deleted from config — does Deploy delete it from Foundry)? Relates to
   [#8350](https://github.com/Azure/azure-dev/issues/8350) and
   [#8349](https://github.com/Azure/azure-dev/issues/8349).
3. **Error recovery across the `uses` chain.** If `azure.ai.project` Deploy
   fails partway (toolboxes created, a connection fails), what is the recovery
   story for downstream agents that depend on it?
4. **Service-level `runtime:` typing.** The typed `runtime: { stack, version }`
   block exists in the schema today only under the compose `appServiceResource`
   definition, **not under `services.<name>`**, and `ServiceConfig` has no
   `Runtime` field. Phase 1 can capture it via `AdditionalProperties` (untyped,
   not validated by the core schema), or core can promote `runtime` to a
   first-class service field. Decide whether the typing/validation cost
   justifies a core change.

### Proposed `azure.yaml` shape

The proposed end-state of `azure.yaml` is captured as standalone, illustrative
examples alongside this ADR, one self-contained file per scenario, under
[`azure-yaml-examples/`](./azure-yaml-examples/):

| Scenario | File | Deploy source |
|---|---|---|
| New project, zip/code-deploy | [`code-deploy.yaml`](./azure-yaml-examples/code-deploy.yaml) | `runtime:` |
| New project, build from Dockerfile | [`container-build.yaml`](./azure-yaml-examples/container-build.yaml) | `docker:` |
| New project, existing pre-built image (ACR) | [`existing-image.yaml`](./azure-yaml-examples/existing-image.yaml) | `image:` |
| Existing project, reuse existing model | [`brownfield-existing-model.yaml`](./azure-yaml-examples/brownfield-existing-model.yaml) | `runtime:` |
| Existing project, create new model | [`brownfield-new-model.yaml`](./azure-yaml-examples/brownfield-new-model.yaml) | `runtime:` |

Each file pairs a single `host: azure.ai.project` service (project-scoped
data-plane resources — model deployments, connections, toolboxes) with a
`host: azure.ai.agent` service that references it via `uses:`. The three deploy
sources (`runtime:` / `docker:` / `image:`) are mutually exclusive; exactly one
is required. The brownfield files use the `resourceId:` field introduced by the
Bicep-less RFC [#8065](https://github.com/Azure/azure-dev/issues/8065) (Phase 2).
