# ADR-001: Unify Foundry agent configuration in `azure.yaml`

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
   `docker.remoteBuild`). No new top-level `dockerfile` field — that would
   conflict with the existing `DockerProjectOptions` on `ServiceConfig`.
5. **Code-deploy mode uses a typed `runtime: { stack, version }` block**,
   following the existing azure.yaml runtime precedent — not a bare string.
6. **Deploy mode is explicit.** `docker:` present → container mode; `runtime:`
   present → code-deploy mode; neither → validation error; both → validation
   error. No silent defaults.
7. **`${VAR}` env-var expansion** uses the same syntax `azure.yaml` already
   supports. The extension performs the expansion inside `config:` blocks
   (the core framework does not expand `config:` — see Consequences).
8. **`init` populates `config:`** the way it currently populates `agent.yaml`,
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

### Consequences

**Easier:**

- One file to read and edit. The agent name, container resources, and model
  deployment name each live in exactly one place.
- Clean scope separation: "what the agent IS to Foundry" (`config:`) vs. "how
  azd packages and orders it" (service-level `docker:` / `runtime:` / `uses:`).
- Resource sharing across agents is natural — a second agent is just another
  `host: azure.ai.agent` with `uses: [foundry-project]`, nothing duplicated.
- Foundry Toolkit for VS Code can read/write `azure.yaml` directly, converging
  the CLI and Toolkit experiences.

**Harder / requires care:**

- The core framework does **not** expand `${VAR}` inside `config:` blocks
  (`Config` is serialized straight to `structpb` without `Envsubst`). The
  extension must own interpolation; a shared recursive config walker should be
  used by both the agent and project targets.
- `azure.ai.project` is a service with no source code — Package/Publish are
  no-ops and the lifecycle (toolbox/connection/deployment creation) must move
  out of provisioning hooks into the project target's `Deploy`.
- A deprecation window for `agent.yaml` / `agent.manifest.yaml` means two read
  paths exist temporarily; telemetry is needed to know when removal is safe.
- Downstream cleanup: rename/repurpose `exterrors.CodeInvalidAgentManifest`,
  retire the `agent_yaml` package, migrate reference templates/samples, and
  update docs (`glossary.md`, `feature-status.md`).

**Core impact:** Phase 1 needs **no mandatory azd core change**. `host` is an
open `type: string` (not a closed enum), host kinds register over gRPC via
`WithServiceTarget`, `uses` ordering and source-less services already work, and
`ServiceConfig.Config` is `map[string]any`. The only optional core changes are:
(a) adding `azure.ai.project` to the `host` examples in the core schema for
discoverability, and (b) promoting a service-level `runtime:` field to
`ServiceConfig` + schema if first-class typing is desired (see Open Question 4).

### Alternatives Considered

- **Make `agent.yaml` the source of truth; introduce `azure.yaml` only on
  opt-in.** Keeps a single per-agent file for the standalone path. Rejected
  because `agent.yaml` is per-agent by definition — project-scoped resources
  (toolboxes today, knowledge indexes tomorrow) have nowhere good to live.
  Every sub-option (inline in each agent, a new `foundry.yaml`, or AZD-only
  project scope) reintroduces duplication or a third file.
- **Hybrid: keep `agent.yaml` for the per-agent definition, `azure.yaml` for
  orchestration only.** Structurally separates concerns but keeps two parallel
  deploy code paths alive and requires permanent schema vigilance to keep
  agent-definition fields out of the service block. The "no `azure.yaml` in the
  root" win is mostly perceptual — the standalone path here is already
  `azure.yaml` + agent code, no `.azure/`, no `infra/`.
- **Project-scoped resources as a sibling service per resource type**
  (`host: azure.ai.toolbox`, etc., Option A). Rejected — proliferation risk;
  toolboxes aren't really "services" (no source dir, no build), and it sets a
  precedent for `host: azure.ai.eval-dataset`, `host: azure.ai.vector-index`,
  and so on.
- **Project-scoped resources under the agent extension namespace**
  (`extensions.azure.ai.agents.toolboxes:`). Rejected — still nests
  project-level resources under the agent extension's namespace, which doesn't
  address that models, connections, and toolboxes live at the Foundry **project**
  scope, not the agent scope.
- **A new top-level `resources:` section** (Option C). Clean separation but
  introduces a new top-level concept to the core azure.yaml schema — a bigger
  cross-cutting change than reusing a single `azure.ai.project` host kind
  (Option B, chosen).
