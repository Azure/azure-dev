# Technical design: unify Foundry agent config in `azure.yaml`

<!-- cspell:ignore upserts unmarshals omitempty grpcserver osutil exterrors unprovisioned rebases rebasing webhook -->

## Overview

This document is the engineering design for the unified `azure.yaml` proposal. It covers
the **technical design** that azd core and the Foundry extensions need, and the
**end to end experience** a user gets at the command line.

Background and product spec can be found in the following sources:

- Product brief and sample file shapes: the `simple`, `complex`, and `separate-services`
  branches of the preview repo, and the proposed schemas under `schemas/`.
- RFC issue [#7962](https://github.com/Azure/azure-dev/issues/7962): Unify Foundry
  agent configuration in `azure.yaml`.
- RFC issue [#8049](https://github.com/Azure/azure-dev/issues/8049): Composition
  commands (`azd ai project add ...`), which depends on this work.

One note on shape, because it affects everything below. The earlier issue #7962 proposed
two host kinds, `azure.ai.project` for shared state and `azure.ai.agent` per agent, linked
with `uses:`. A later iteration collapsed everything into a single `host: microsoft.foundry`
service that owned all Foundry state with agents nested as an array. The current brief
generalizes the per-resource idea: **every Foundry resource is its own `services:` entry**
with a singular host, keyed by the resource name. A Foundry project is a project service plus
sibling services for its connections, toolboxes, skills, agents, and routines, ordered with
`uses:` and cross-referenced by name. This design follows that **separate-services** shape.
The single `host: microsoft.foundry` shape and the original two-host shape are superseded.

The host kinds are:

| Host kind | Owns |
|---|---|
| `azure.ai.project` | the Foundry project plus its `deployments:` array and optional `endpoint:` |
| `azure.ai.connection` | one project connection |
| `azure.ai.toolbox` | one toolbox |
| `azure.ai.skill` | one skill |
| `azure.ai.agent` | one agent (the existing host kind, with an evolved shape) |
| `azure.ai.routine` | one routine |

Why this shape. A Foundry project is a set of related data-plane resources, not one
monolith. Modeling each as a service lets azd core do the work it already does for services:
order them with `uses:`, report progress and failures per resource, and dispatch each to the
extension that owns it. Schema ownership falls out cleanly, because each sibling extension owns
the host kind, the service target, and the schema for the one resource it already manages
through its `azd ai` CLI. It also reuses the existing `azure.ai.agent` service-target pattern
rather than inventing a single project-wide target that fans out internally.

`microsoft.foundry` stays the meta-package extension id that bundles the sibling extensions; in
this shape it is no longer a host kind.

### Scope

In scope: the per-host schema conditionals, the new service targets across the sibling
extensions, the config binding, `$ref` includes, templating, the consolidated `init`, the
retirement of `agent.yaml` / `agent.manifest.yaml` and the old config-nested `azure.ai.agent`
shape, and the lifecycle behavior.

Out of scope, tracked elsewhere:

- Built in Bicep and the provision-less flow. This has its own RFC (not yet filed).
  This document only flags where it touches the provision experience.
- The `azd ai project add` composition commands. That is issue #8049, and it builds on
  this work.
- The Foundry Toolkit for VS Code parser switch. That is owned by the Toolkit team.

## Part 1: End to end experience

This part describes what a developer sees at the command line.

### 1.1 First run with `azd ai agent init`

Today `init` writes three files: `azure.yaml`, `agent.yaml`, and
`agent.manifest.yaml`. After this change it writes one file. The Foundry project, its
model deployments, and each of its agents are entries under `services:`.

```yaml
services:
  agent-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: { format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14" }
        sku: { name: GlobalStandard, capacity: 10 }
  basic-agent:
    host: azure.ai.agent
    uses: [agent-project]
    kind: hosted
    description: A basic agent hosted by Foundry.
    project: src/basic-agent
    docker: { path: Dockerfile, remoteBuild: true }
    protocols:
      - { protocol: responses, version: "1.0.0" }
    startupCommand: python main.py
    env:
      FOUNDRY_MODEL_DEPLOYMENT_NAME: gpt-4.1-mini
```

There is no `agent.yaml` and no `agent.manifest.yaml`. A developer who opens the project
sees one source of truth, with each resource as a clearly named service.

### 1.2 Inner loop: provision then deploy

The verbs do not change. The mental model is provision creates the project, deploy
fills it in.

1. `azd provision` creates the Foundry project at the ARM level through the
   `azure.ai.project` service. With the built in Bicep flow (separate RFC), no `infra/`
   folder is required for a Foundry only project. If that service sets an `endpoint:` value,
   provision is skipped and azd uses the existing project (see 1.4).
2. `azd deploy` walks the services in `uses:` order. The project service reconciles its
   deployments; the connection, toolbox, and skill services reconcile their resources through
   Foundry APIs; each agent service builds and publishes its artifact (when it has source) and
   posts the agent; routine services run last, since a routine references an agent by name.
3. `azd up` does both in order.

For a project with several agents, azd core sees several services and shows progress per
service, for example one line for building `support-agent` and another for `research-agent`.
The per resource detail is native, because each resource is a service. There is no
extension-internal fan-out.

### 1.3 A Foundry project plus a normal service

A non Foundry service can sit next to the Foundry services and consume them. The frontend
declares `uses:` on the project (or on a specific agent), which orders deploy and injects the
project endpoint as an environment value.

```yaml
services:
  support-platform:
    host: azure.ai.project
    # deployments ...
  # connection, toolbox, skill, agent, routine services ...
  webapp:
    project: src/webapp
    host: containerapp
    uses: [support-platform]
    env:
      FOUNDRY_PROJECT_ENDPOINT: ${FOUNDRY_PROJECT_ENDPOINT}
```

No new ordering logic is needed. `uses:` already exists on every service.

### 1.4 Use an existing Foundry project

When `endpoint:` is set on the `azure.ai.project` service, azd connects to that project
instead of creating one. This is the path for teams that provision infrastructure on their
own, and for reusing a shared or private network bound account, which is the minimum issue
[#8165](https://github.com/Azure/azure-dev/issues/8165) asks for. Provisioning a new network
bound account from scratch is part of the separate built-in Bicep work, not this field.
Network settings such as VNET binding live on the Foundry **Account** resource, not on the
Foundry **Project**, and azd does not model the Account as a service here, so customizing them
today means ejecting to Bicep. That built-in Bicep work must provide a path to create and
customize those network settings.

```yaml
services:
  agent-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
  basic-agent:
    host: azure.ai.agent
    uses: [agent-project]
    kind: hosted
    project: src/basic-agent
    # ...
```

The absence of `endpoint:` on the project service is the signal to provision a new project.
This keeps the common case simple and makes reuse explicit.

### 1.5 Opening an older project (migration)

When a project still has `agent.yaml` or `agent.manifest.yaml` next to `azure.yaml`, or still
uses the old config-nested `host: azure.ai.agent` shape, azd keeps working during a
deprecation window:

1. It prints a warning that points at the migration guide.
2. It reads the old files and builds the same in memory services the new shape would produce,
   a project service plus sibling resource services plus an agent service, so build and deploy
   still run.
3. It records a telemetry signal so the team can watch how fast old projects fade.

Note the nuance on `azure.ai.agent`. The host kind is **kept**, but its shape changes: the
agent fields move out from under `config:` to direct service-level properties, and the
project-scoped resources that used to live with the agent (deployments, connections,
toolboxes) move into their own services. Migration rewrites the old single-agent config into a
project service, sibling resource services, and an agent service.

After the window closes, the old files and the old config-nested shape are removed, and the
warning becomes an error with a link to the guide.

### 1.6 Teardown with `azd down`

The rule for Foundry data plane state is the same one issue #8049 uses: removing an entry from
`azure.yaml` means stop using it, not destroy it. The destructive path is explicit and runs
through `azd down` or the per resource `azd ai` commands.

`azd down` removes the Foundry project that azd provisioned, which takes its deployments,
connections, toolboxes, skills, agents, and routines with it. If the project service used an
`endpoint:` to point at an existing project, `azd down` does not delete that project, because
azd did not create it. azd's existing teardown logic likely already enforces this "don't
delete what azd didn't provision" rule, so this may need no custom handling; confirm against
the current `azd down` behavior during implementation.

### 1.7 When part of a deploy fails

A project can hold several agent and resource services. If three services succeed and the
fourth fails, the run stops with an error that names the failing service, and the state
already written stays in place. Because services are ordered by `uses:`, anything downstream of
the failure does not run. azd core names the failing service directly, so the user is not left
guessing which one broke. Re-running `azd deploy` is safe: each service target upserts, so
finished work is detected and skipped or refreshed rather than duplicated. Part 2.8 covers the
reconcile rules.

## Part 2: Technical design

### 2.1 How the config reaches the extension

Each Foundry resource is its own service entry, so each carries its own `host` and its own
resource keys. Those keys (`deployments` on the project; `category`, `target`, `credentials`
on a connection; `tools` on a toolbox; `instructions` on a skill; the agent fields on an
agent; `trigger` on a routine) do not need new fields on `ServiceConfig`. They land in the
existing inline map on each entry and travel to the owning extension unchanged.

- `cli/azd/pkg/project/service_config.go` declares
  `AdditionalProperties map[string]any` with the `yaml:",inline"` tag. Any key on a service
  entry that is not a known field is captured here. The Foundry keys parse today with no core
  struct change.
- `cli/azd/pkg/project/mapper_registry.go` converts both `Config` and `AdditionalProperties`
  into a `google.protobuf.Struct` and sends them to the service target extension over gRPC.
  Each extension receives its resource's keys as structured data.

The only required core change for parsing is the per-host JSON Schema (2.3). The Go side is
already wired.

On the extension side, each provider unmarshals its struct into typed Go values. Sketches of
the shapes they bind to:

```go
type ProjectConfig struct {
    Endpoint    string       `json:"endpoint,omitempty"`
    Deployments []Deployment `json:"deployments,omitempty"`
}

type AgentConfig struct {
    Kind      string   `json:"kind"`      // hosted | prompt
    Toolboxes []string `json:"toolboxes,omitempty"` // names of azure.ai.toolbox services
    Skill     string   `json:"skill,omitempty"`     // name of an azure.ai.skill service
    // hosted: Project plus exactly one deploy mode (docker | runtime | image)
    // prompt: Instructions (inline string or a path to a prompt file)
}
```

`Agent` is the union of a hosted agent and a prompt agent. Both kinds may carry `toolboxes`
(named references to `azure.ai.toolbox` services), `tools` (tools attached directly to the
agent), a single `skill` reference (an `azure.ai.skill` service), `env`, and `metadata`. A
hosted agent additionally carries `project` plus exactly one deploy mode: `docker`, `runtime`,
or a prebuilt `image`. A prompt agent carries none of those build fields; instead it carries
`instructions`, an inline string or a path to a prompt file (see 2.4). These cross-service
references resolve by name in 2.6.

### 2.2 Wiring the new hosts to service targets

The extension family already registers one service target, `azure.ai.agent`. The
separate-services shape adds a host kind per resource, each owned by the sibling extension that
already owns that resource's data-plane CLI.

| Host kind | Owning extension | Resource |
|---|---|---|
| `azure.ai.project` | `azure.ai.projects` | project plus deployments |
| `azure.ai.connection` | `azure.ai.connections` | connection |
| `azure.ai.toolbox` | `azure.ai.toolboxes` | toolbox |
| `azure.ai.skill` | `azure.ai.skills` | skill |
| `azure.ai.agent` | `azure.ai.agents` | agent (existing host, evolved) |
| `azure.ai.routine` | `azure.ai.routines` | routine |

Each extension registers its host through the same pattern `azure.ai.agents` uses today at
`cli/azd/extensions/azure.ai.agents/internal/cmd/listen.go`, which calls
`WithServiceTarget("azure.ai.agent", ...)`. Each sibling adds a `WithServiceTarget("azure.ai.<kind>", ...)`
call that returns its provider, and declares the provider in its `extension.yaml` under
`providers` with `type: service-target`.

Core dispatch needs no change. When azd reads `host: azure.ai.<kind>`, the service manager in
`cli/azd/pkg/project/service_manager.go` resolves the host string against the IoC container.
Extension hosts are registered through the gRPC path in
`cli/azd/internal/grpcserver/service_target_service.go`, which wraps the extension in an
`ExternalServiceTarget`. The same path already serves `azure.ai.agent`.

Optional rollout control: `service_manager.go` checks `alpha.IsFeatureKey(host)` before it
resolves a host. If the team wants the new shape behind a flag during preview, register the new
hosts as alpha features so they only activate after the user enables them. This is optional,
because installing the extensions is already an opt in step.

### 2.3 Schema composition

Add one conditional per host kind to `schemas/v1.0/azure.yaml.json`. They differ from the
existing `azure.ai.agent` block, which references an extension schema into the `config:` field.
For these hosts the schema is composed at the service level, because the resource keys are
direct properties of the entry, not nested under `config:`.

```json
{
  "if": { "properties": { "host": { "const": "azure.ai.toolbox" } } },
  "then": {
    "allOf": [
      { "$ref": "https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/azure.ai.toolboxes/schemas/azure.ai.toolbox.json" }
    ],
    "properties": {
      "project": false,
      "runtime": false,
      "docker":  false,
      "image":   false,
      "config":  false
    }
  }
}
```

The code-less resource hosts (`azure.ai.project`, `azure.ai.connection`, `azure.ai.toolbox`,
`azure.ai.skill`, `azure.ai.routine`) turn off `project`, `runtime`, `docker`, and `image`,
because those resources have no source. The `azure.ai.agent` conditional evolves the same way,
moving from a `$ref` into `config:` to composing the agent schema at the service level, but it
**keeps** `project`, `runtime`, `docker`, and `image`, because an agent does carry source.
Every conditional turns off `config:`, since the schema is composed at the service level.

Each sibling extension publishes the schema for the host it owns
(`azure.ai.project.json`, `azure.ai.connection.json`, `azure.ai.toolbox.json`,
`azure.ai.skill.json`, `azure.ai.agent.json`, `azure.ai.routine.json`), plus the shared
`FileRef.json`. Each resource entry is `oneOf` an inline object or a file reference, which is
what enables the split file layout in 2.4.

One detail to settle: the preview schemas set `additionalProperties: false`, while the brief
text asks for `true` so fields can be added without a schema break. Recommend `true` on each
resource schema. Note that genuinely new resource types (eval datasets, vector indexes,
memories) arrive as new host kinds with their own services and schemas, not as new keys on an
existing entry.

### 2.4 `$ref` file includes and overlay overrides

The `complex` sample splits large entries into their own files under `agents/`, `toolboxes/`,
and `skills/`, and pulls them in with `$ref`. In the separate-services shape the `$ref` sits on
the service entry, beside `host:`. The `host:` and the service key (the resource name) stay
inline, and the `$ref` supplies the rest of the body. Deployments are the exception: they stay
an array on the project service, so a deployment `$ref` sits at the array-item level.

```yaml
services:
  research-agent:
    host: azure.ai.agent
    uses: [agent-project]
    $ref: ./agents/research-agent.yaml
```

`FileRef.json` allows sibling keys next to `$ref`, which act as overrides layered on top of the
loaded file. This is not an azd feature today and the brief does not call it out, so the design
defines it here.

**Who resolves it.** Two options:

- Core resolves `$ref` includes generically for any service field. This helps every extension
  but is new core behavior and forces core to own the merge and validation rules.
- Each owning extension resolves `$ref` while it parses its own keys. This is contained to the
  Foundry extensions and needs no core change.

Recommend each owning extension owns resolution for the first version, with a clear path to
move it into core later if other extensions need includes.

**Resolution rules.**

- A relative `$ref` path resolves relative to the file that holds it, so nested includes work.
  Per `FileRef.json`, absolute paths and URLs are also accepted; the brief makes that call, so
  the design follows it. Treat such includes as trusted input the same way `azure.yaml` itself
  is, and surface a clear error when a path cannot be read.
- Sibling keys overlay on the loaded file. Use a shallow overlay at the top level of the
  object. Scalars and arrays from the sibling replace the loaded value. This keeps the result
  easy to predict.
- A loaded file is validated against the same per resource schema as an inline entry.

**Path resolution and rebasing.** The provider receives its keys as already parsed data, so the
`$ref` strings arrive as plain values. To open a top level `$ref` it needs the directory that
holds `azure.yaml`, not the agent source path, so the provider must get the project root from
the azd client or environment (the gRPC `ServiceConfig` carries the service's `project` path,
not the project root). Once a split file is loaded, every relative path inside it rebases to
that file's own directory, not to `azure.yaml`. The `complex` sample is explicit: in
`agents/support-agent.yaml`, `project: ../src/support-agent` is relative to the `agents/`
folder, and in `skills/code-review.yaml`, `instructions: ../prompts/code-review.md` is relative
to the `skills/` folder. The resolver must track each loaded file's base directory and apply it
to that file's `project`, `instructions`, and any nested `$ref`.

**Instruction and prompt files.** Separate from `$ref`, a skill's `instructions` and a prompt
agent's `instructions` accept either an inline string or a path to a `.md` or `.txt` file the
extension reads at deploy time; the `complex` sample uses both forms. This is a file read, not
a structural include: the file's text becomes the field value, with no overlay. The path
follows the same rebasing rule above, so an `instructions:` path inside a split agent or skill
file resolves against that file's directory. `${VAR}` and `${{...}}` expansion (2.5) applies to
the loaded text.

**Interaction with #8049.** The composition commands write into these same services. If a
resource is split into a file, the writer has to decide whether to append a new service entry
inline or edit the referenced file. Define the default as: append inline, and only edit a split
file when the command explicitly targets it. Both this design and #8049 should share one YAML
edit helper that understands `$ref` entries, so reads and writes agree.

### 2.5 Templating: `${VAR}` and `${{...}}`

Two resolvers have to coexist without stepping on each other.

- `${VAR}` is an azd environment value, resolved on the client before anything is sent to
  Foundry. Example: a connection secret read from the azd environment.
- `${{...}}` is special Foundry syntax that azd never resolves. azd treats it as an opaque
  string value and passes it through unchanged; Foundry resolves it on the server, for example
  `${{connections.x.credentials.key}}`.

Core already leaves this alone. `pkg/osutil/expandable_string.go` runs `${VAR}` expansion with
`drone/envsubst`, but core only applies it to typed fields such as the resource name and image.
It does not expand `Config` or `AdditionalProperties`, so `${{...}}` passes through core
untouched.

That moves the work to the extensions. The risk is that if an extension expands `${VAR}` with
the same library, the library may try to read `${{...}}` as well. The expander must touch only
`${VAR}` and leave `${{...}}` intact. A simple and safe approach: protect every `${{...}}` span
with a placeholder, run `${VAR}` expansion, then restore the placeholders. Put this in one
shared helper so every Foundry field, in every Foundry extension, expands the same way.

Which fields take which:

| Field | `${VAR}` | `${{...}}` |
|---|---|---|
| Agent `env` values | yes | yes |
| Connection credentials | yes (secret from azd env) | yes (Foundry managed) |
| Routine `input` values | yes | yes |
| Model deployment names and SKUs | yes | no |

### 2.6 Service target lifecycle: one target per resource

This is the biggest change from the superseded single-service shape. Instead of one service
target that fans out across nested agents, each host kind is its own service target that handles
one resource, and azd core orders the services with `uses:`.

Each provider implements the same interface (`Initialize`, `Package`, `Publish`, `Deploy`,
`Endpoints`, `GetTargetResource`). Behavior by host:

| Host | Package | Publish | Deploy |
|---|---|---|---|
| `azure.ai.project` | none | none | Resolve the project (provisioned, or via `endpoint:`) and upsert each deployment. |
| `azure.ai.connection` | none | none | Upsert the connection; resolve `${VAR}` secrets from the azd env. |
| `azure.ai.toolbox` | none | none | Upsert the toolbox; resolve named connection references. |
| `azure.ai.skill` | none | none | Upsert the skill; load the `instructions` file when it is a path. |
| `azure.ai.agent` | build when hosted with `project` plus `docker` or `runtime` (`docker` builds an image, local or ACR when `remoteBuild` is set; `runtime` zips the source); prompt and prebuilt `image` agents skip this | push the artifact (ACR for images, Foundry storage for zips) | post `createAgentVersion` with the published artifact reference; resolve named toolbox and skill references. |
| `azure.ai.routine` | none | none | Upsert the routine; resolve the referenced agent by name. |

Ordering is expressed in `azure.yaml` with `uses:` and enforced by azd core: the project first,
then connections, then toolboxes and skills, then agents, then routines (a routine references
an agent). The author writes the `uses:` edges; azd core topologically orders the services.
There is no extension-internal ordering or fan-out.

Cross-resource references resolve by name against the live project: an agent's `toolboxes` and
`skill`, a toolbox's connection, and a routine's agent are names that the owning provider looks
up in Foundry at deploy time. The `uses:` edge guarantees the referenced resource is reconciled
first.

Details to define in code:

- Independent services (several connections, or agents that do not depend on each other) can
  deploy concurrently; azd core's existing service scheduling handles this within the `uses:`
  constraints.
- A failure in any one service stops the run with azd core naming the failed service;
  downstream services do not run. Attribution is native, so the extension does not have to
  synthesize it.
- The project-state reconcile that used to live in the post provision and post deploy hook
  handlers in `azure.ai.agents`/`listen.go` moves into the per resource `Deploy` methods of the
  owning providers.

### 2.7 Reworking `init`

Today `init` reconciles data across three files. The functions
`extractToolboxAndConnectionConfigs` and `extractConnectionConfigs` in
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go` read the manifest, normalize auth
types, move secrets into the azd environment, and feed typed arrays that then get written across
`agent.yaml` and `azure.yaml`. The file write for `agent.yaml` happens in
`writeAgentDefinitionFile`.

After this change `init` writes the several service entries directly: a project service with
its deployments, sibling services for connections, toolboxes, and skills, and one agent service
per agent, wired together with `uses:`. The roughly two hundred lines of cross file
reconciliation collapse into building the in memory services and writing them. `agent.yaml` and
`agent.manifest.yaml` are no longer produced for new projects.

Deprecation detection lives here too. If the old files are present, or the project uses the
config-nested `host: azure.ai.agent` shape, print the warning, run the fallback that produces
the equivalent in memory services, and emit the telemetry signal. The error code
`CodeInvalidAgentManifest` in `internal/exterrors/codes.go` stays while the manifest path is
read. Rename or retire it when that path is removed.

### 2.8 State reconciliation and idempotency

Each service's `Deploy` issues an idempotent CreateOrUpdate call for its resource (the project
upserts each declared deployment) and lets Foundry reconcile server-side. azd does not query
Foundry's live state and diff it on the client. The effect is upsert: create what is missing
and update what changed, with the service owning that logic. Removing a service from
`azure.yaml` stops azd from managing that resource, but does not delete it from Foundry.
Deletion is the job of `azd down` or the per resource `azd ai` commands.

Two Foundry behaviors to confirm against the API contracts, because they decide how the
CreateOrUpdate is written:

- Whether each resource type exposes an idempotent CreateOrUpdate (PUT style) call, so the
  provider never has to read first and branch between create and update.
- Whether a re run after a partial failure can safely repeat the calls that already succeeded,
  which the CreateOrUpdate model should give us.

This area overlaps with existing reports [#8349](https://github.com/Azure/azure-dev/issues/8349),
[#8350](https://github.com/Azure/azure-dev/issues/8350), and
[#8587](https://github.com/Azure/azure-dev/issues/8587); reconciling data-plane resources such as
toolboxes and connections at deploy time as their own services, together with the upsert model
above and the brief's "drop from config stops management, does not destroy" semantics, is meant
to cover them. #8587 in particular, the provision failure for resources layered into provision,
is addressed because toolboxes and connections are reconciled at deploy as their own services
rather than created through provision layers.

### 2.9 Telemetry and errors

- Emit a telemetry event when the old file fallback runs and when the config-nested
  `host: azure.ai.agent` shape is seen, so the deprecation window length can be driven by data.
- Per service failures are surfaced by azd core directly, because each resource is a service, so
  failure attribution is native. The extension does not synthesize per agent attribution.
- Keep `CodeInvalidAgentManifest` while the manifest path exists, and plan its rename or removal
  with that path.

### 2.10 Sibling extensions and schema ownership

Foundry already ships per resource extensions, bundled by the `microsoft.foundry`
meta-package: `azure.ai.toolboxes`, `azure.ai.connections`, `azure.ai.projects`,
`azure.ai.skills`, and `azure.ai.routines`, alongside `azure.ai.agents`. Each owns a data-plane
CLI (`azd ai toolbox`, `azd ai connection`, and so on) that acts on a live project.

In the separate-services shape, ownership is clean: each sibling extension owns its host kind,
its service target, and the per resource schema for the one resource it already manages
(`azure.ai.project.json`, `azure.ai.connection.json`, `azure.ai.toolbox.json`,
`azure.ai.skill.json`, `azure.ai.routine.json`), while `azure.ai.agents` owns `azure.ai.agent`
and `azure.ai.agent.json`. There is no single project-wide schema to compose, and no new core
mechanism for one extension to contribute part of another's schema: each host kind has its own
conditional in `azure.yaml.json` that references the owning extension's schema, which is exactly
the pattern `azure.ai.agent` already uses. Reconciliation is naturally distributed, since each
provider reconciles its own resource at deploy.

This is the realization of what earlier drafts called the per-extension ownership option, but
without the schema-slice merge problem, because the resources are separate services rather than
nested keys under one entry.

One naming note: `microsoft.foundry` is the meta-package extension id that bundles these
siblings; in this shape it is not a host kind. The host kinds are the `azure.ai.<kind>` strings,
each resolved by its owning extension.

## Open questions

A few things this design surfaces that the brief and the related issues do not already settle.
None of them block the overall shape, but they are worth talking through together before we lock
the first version.

1. **`azure.ai.agent`: kept, not deprecated.** The brief body, written for the single-service
   shape, says to deprecate `host: azure.ai.agent` because agents are not top-level services
   there. The separate-services shape reuses `azure.ai.agent` as the go-forward agent host with
   an evolved, service-level schema. Let's confirm the position this design takes: keep
   `azure.ai.agent` as the agent host, and deprecate only its old config-nested shape and the
   `agent.yaml` / `agent.manifest.yaml` files.
2. **Host kind naming.** `azure.ai.project` vs `azure.ai.foundry` for the project host, and the
   `azure.ai.<kind>` family vs the `microsoft.foundry` meta-package name. These lock in once
   shipped.
3. **Cross-service references and `uses:` correctness.** References across services are by name,
   and deploy order depends on the author writing the `uses:` edges. Decide whether `init` and
   the #8049 composition commands always emit the `uses:` edges automatically, and whether a
   missing edge is a validation error or just a deploy-order risk.
4. **Code-less project service.** `azure.ai.project` is a provision-only service with no source.
   Confirm azd's provision and deploy treat a code-less service cleanly (provision creates it,
   deploy upserts deployments), and that `azd deploy <name>` on it behaves sensibly.
5. **Agent versioning.** Issue [#8066](https://github.com/Azure/azure-dev/issues/8066) notes
   that `azure.yaml` only represents the latest state of an agent, even though agents are
   versioned, and Foundry will add traffic splitting across versions. Deploy posts a new version
   each run, so let's decide together what the YAML should mean: latest only, pinned, or
   something that can express more than one version.
6. **Composition surface naming.** Issue [#8049](https://github.com/Azure/azure-dev/issues/8049)
   puts the `add` commands in an `azd ai project` surface, written against the earlier two host
   shape, while this design models each resource as its own `azure.ai.<kind>` service. The open
   part is where the `add` commands live, and how they name the service they create, so they do
   not collide.

## Summary of required changes

The brief's "Missing functionality" table already lists the baseline work. This spec keeps that
list and adds the deltas below.

azd core:

- Add a `host: azure.ai.<kind>` conditional per Foundry resource to
  `schemas/v1.0/azure.yaml.json`, each composing the owning extension's schema at the service
  level and turning off `config:`. The code-less resource hosts also turn off `project`,
  `runtime`, `docker`, and `image`; the `azure.ai.agent` host keeps them.
- Recommend `additionalProperties: true` on each resource schema (the preview currently has
  `false`); new resource types arrive as new host kinds, not new keys on an existing entry.
- Optional: register the new hosts as alpha features for a gated preview, and a shared helper if
  core ever expands these fields while preserving `${{...}}`.
- No new schema-slice contribution or merge mechanism is required: each host kind has its own
  conditional, reusing the existing `azure.ai.agent` pattern.

Sibling extensions (`azure.ai.projects`, `azure.ai.connections`, `azure.ai.toolboxes`,
`azure.ai.skills`, `azure.ai.routines`), one host kind each:

- Register the host and a service target, and publish the per resource schema. The project
  service provisions or connects the project and upserts its deployments; the others upsert their
  resource at deploy time.
- Resolve `$ref` includes with shallow overlay overrides, rebasing each loaded file's paths to
  its own directory, and load `instructions` prompt files at deploy time.
- Expand `${VAR}` while preserving `${{...}}` through the shared helper.

`azure.ai.agents` extension (the agent host, evolved):

- Evolve `azure.ai.agent` from the config-nested shape to service-level direct properties,
  keeping `project`, `runtime`, `docker`, and `image`.
- Implement the `azure.ai.agent` service target: package and publish per agent (`docker`,
  `runtime`, or a prebuilt `image`), post `createAgentVersion`, and resolve named toolbox and
  skill references.
- Rework `init` to write the project, sibling, and agent services and stop emitting `agent.yaml`
  and `agent.manifest.yaml`.
- Add the deprecation fallback and telemetry for the old files and the config-nested
  `azure.ai.agent` shape.

Shared across the Foundry extensions:

- One YAML edit helper that understands `$ref` entries, shared with #8049.
- One `${VAR}` / `${{...}}` expansion helper.
