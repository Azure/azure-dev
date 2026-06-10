# Technical design: unify Foundry agent config in `azure.yaml`

<!-- cspell:ignore upserts unmarshals omitempty grpcserver osutil exterrors unprovisioned rebases rebasing webhook -->

## Overview

This document is the engineering design for the unified `azure.yaml` proposal. It covers
the **technical design** that azd core and the `azure.ai.agents` extension need, and the
**end to end experience** a user gets at the command line.

Background and product spec can be found in the following sources:

- Product brief and sample file shapes: the `simple` and `complex` branches of the
  preview repo, and the proposed schemas under `schemas/`.
- RFC issue [#7962](https://github.com/Azure/azure-dev/issues/7962): Unify Foundry
  agent configuration in `azure.yaml`.
- RFC issue [#8049](https://github.com/Azure/azure-dev/issues/8049): Composition
  commands (`azd ai project add ...`), which depends on this work.

One note on shape, because it affects everything below. The earlier issue #7962
proposed two host kinds, `azure.ai.project` for shared state and `azure.ai.agent` per
agent, linked with `uses:`. The current brief replaces that with a single
`host: microsoft.foundry` service that owns all Foundry state, with agents nested as an
array inside that one entry. This design follows the single service shape. The two
service shape is treated as superseded.

### Scope

In scope: the schema change, the new service target, the config binding, `$ref`
includes, templating, the consolidated `init`, the deprecation of the old host and old
files, and the lifecycle behavior.

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
model deployments, and its agents all live in a single `services:` entry.

```yaml
services:
  agent-project:
    host: microsoft.foundry
    deployments:
      - name: gpt-4.1-mini
        model: { format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14" }
        sku: { name: GlobalStandard, capacity: 10 }
    agents:
      - name: basic-agent
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

There is no `agent.yaml` and no `agent.manifest.yaml`. A developer who opens the
project sees one source of truth.

### 1.2 Inner loop: provision then deploy

The verbs do not change. The mental model is provision creates the project, deploy
fills it in.

1. `azd provision` creates the Foundry project at the ARM level. With the built in
   Bicep flow (separate RFC), no `infra/` folder is required for a Foundry only
   project. If the service sets an `endpoint:` value, provision is skipped and azd uses
   the existing project (see 1.4).
2. `azd deploy` runs the Foundry service target. For each agent that has source code it
   builds an artifact, publishes it, then writes the project state and posts each agent
   to Foundry.
3. `azd up` does both in order.

For a project with several agents, the deploy output groups work under the one Foundry
service and shows progress per agent, for example one line for building `support-agent`
and another for `research-agent`. azd core sees a single service. The per agent detail
comes from the extension (see Part 2.6).

### 1.3 A Foundry project plus a normal service

A non Foundry service can sit next to the Foundry service and consume it. The frontend
declares `uses:` on the Foundry service, which orders deploy and injects the project
endpoint as an environment value.

```yaml
services:
  support-platform:
    host: microsoft.foundry
    # deployments, connections, toolboxes, skills, routines, agents ...
  webapp:
    project: src/webapp
    host: containerapp
    uses: [support-platform]
    env:
      FOUNDRY_PROJECT_ENDPOINT: ${FOUNDRY_PROJECT_ENDPOINT}
```

No new ordering logic is needed. `uses:` already exists on every service.

### 1.4 Use an existing Foundry project

When `endpoint:` is set on the Foundry service, azd connects to that project instead of
creating one. This is the path for teams that provision infrastructure on their own, and
for reusing a shared or private network bound account.

```yaml
services:
  agent-project:
    host: microsoft.foundry
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    agents:
      - { name: basic-agent, kind: hosted, project: src/basic-agent, ... }
```

The absence of `endpoint:` is the signal to provision a new project. This keeps the
common case simple and makes reuse explicit.

### 1.5 Opening an older project (migration)

When a project still has `agent.yaml` or `agent.manifest.yaml` next to `azure.yaml`, or
still uses `host: azure.ai.agent`, azd keeps working during a deprecation window:

1. It prints a warning that points at the migration guide.
2. It reads the old files and builds the same in memory Foundry service the new shape
   would produce, so build and deploy still run.
3. It records a telemetry signal so the team can watch how fast old projects fade.

After the window closes, the old path is removed and the warning becomes an error with a
link to the guide.

### 1.6 Teardown with `azd down`

The rule for Foundry data plane state is the same one issue #8049 uses: removing an
entry from `azure.yaml` means stop using it, not destroy it. The destructive path is
explicit and runs through `azd down` or the per resource `azd ai` commands.

`azd down` removes the Foundry project that azd provisioned. If the service used an
`endpoint:` to point at an existing project, `azd down` does not delete that project,
because azd did not create it.

### 1.7 When part of a deploy fails

A Foundry service can hold several agents. If three agents build and the fourth fails,
the run stops with an error that names the failing agent, and the state already written
to Foundry stays in place. Re-running `azd deploy` is safe: the service target upserts,
so finished work is detected and skipped or refreshed rather than duplicated. Part 2.8
covers the reconcile rules.

## Part 2: Technical design

### 2.1 How the config reaches the extension

The new top level keys (`deployments`, `connections`, `toolboxes`, `skills`,
`routines`, `agents`, and `endpoint`) do not need new fields on `ServiceConfig`. They
land in the existing inline map and travel to the extension unchanged.

- `cli/azd/pkg/project/service_config.go` declares
  `AdditionalProperties map[string]any` with the `yaml:",inline"` tag. Any key on the
  service entry that is not a known field is captured here. The Foundry keys parse today
  with no core struct change.
- `cli/azd/pkg/project/mapper_registry.go` converts both `Config` and `AdditionalProperties`
  into a `google.protobuf.Struct` and sends them to the service target extension over
  gRPC. The extension receives the Foundry keys as structured data.

The only required core change for parsing is the JSON Schema (2.3). The Go side is
already wired.

On the extension side, the provider unmarshals the struct into typed Go values. A sketch
of the shape it binds to:

```go
type FoundryProjectConfig struct {
    Endpoint    string       `json:"endpoint,omitempty"`
    Deployments []Deployment `json:"deployments,omitempty"`
    Connections []Connection `json:"connections,omitempty"`
    Toolboxes   []Toolbox    `json:"toolboxes,omitempty"`
    Skills      []Skill      `json:"skills,omitempty"`
    Routines    []Routine    `json:"routines,omitempty"`
    Agents      []Agent      `json:"agents,omitempty"`
}
```

`Agent` is the union of a hosted agent and a prompt agent. Both kinds may carry
`toolboxes` (named references into the project `toolboxes`), `tools` (tools attached
directly to the agent), a single `skill` reference, `env`, and `metadata`. A hosted
agent additionally carries `project` plus exactly one deploy mode: `docker`, `runtime`,
or a prebuilt `image`. A prompt agent carries none of those build fields; instead it
carries `instructions`, an inline string or a path to a prompt file (see 2.4). These
references are validated in 2.6.

### 2.2 Wiring the new host to a service target

The extension already registers one service target. It adds a second one next to it.

- `cli/azd/extensions/azure.ai.agents/internal/cmd/listen.go` calls
  `WithServiceTarget("azure.ai.agent", ...)`. Add a sibling call
  `WithServiceTarget("microsoft.foundry", ...)` that returns the new Foundry provider.
- Declare the new provider in `extension.yaml` under `providers` with
  `type: service-target`.

Core dispatch needs no change. When azd reads `host: microsoft.foundry`, the service
manager in `cli/azd/pkg/project/service_manager.go` resolves the host string against the
IoC container. Extension hosts are registered through the gRPC path in
`cli/azd/internal/grpcserver/service_target_service.go`, which wraps the extension in an
`ExternalServiceTarget`. The same path already serves `azure.ai.agent`.

Optional rollout control: `service_manager.go` checks `alpha.IsFeatureKey(host)` before
it resolves a host. If the team wants the new shape behind a flag during preview, register
`microsoft.foundry` as an alpha feature so it only activates after the user enables it.
This is optional, because installing the extension is already an opt in step.

### 2.3 Schema composition

Add one conditional to `schemas/v1.0/azure.yaml.json`. It differs from the
`azure.ai.agent` block, which references an extension schema into the `config:` field.
For the Foundry host the schema is composed at the service level, because the Foundry
keys are direct properties of the entry, not nested under `config:`.

```json
{
  "if": { "properties": { "host": { "const": "microsoft.foundry" } } },
  "then": {
    "allOf": [
      { "$ref": "https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/azure.ai.agents/schemas/microsoft.foundry.json" }
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

The service level `project`, `runtime`, `docker`, and `image` are turned off because
those belong to each agent, not to the project. `config:` is turned off because the
Foundry schema is composed at the service level instead.

The extension publishes `microsoft.foundry.json` plus the per resource files
(`Deployment.json`, `Connection.json`, `Toolbox.json`, `Skill.json`, `Routine.json`,
`Agent.json`, and `FileRef.json`). The composed schema points its arrays at those files.
Each array item is `oneOf` an inline object or a file reference, which is what enables
the split file layout in 2.4.

One detail to settle: the preview `microsoft.foundry.json` sets
`additionalProperties: false` at the top, while the brief text asks for `true` so future
resource types (eval datasets, vector indexes, memories) can be added without a schema
break. Recommend `true` at the project level.

### 2.4 `$ref` file includes and overlay overrides

The `complex` sample splits large entries into their own files under `agents/`,
`toolboxes/`, and `skills/`, and pulls them in with `$ref`:

```yaml
agents:
  - name: triage-agent
    kind: prompt
    # ... inline ...
  - $ref: ./agents/support-agent.yaml
```

`FileRef.json` allows sibling keys next to `$ref`, which act as overrides layered on top
of the loaded file. This is not an azd feature today and the brief does not call it out,
so the design defines it here.

**Who resolves it.** Two options:

- Core resolves `$ref` includes generically for any service field. This helps every
  extension but is new core behavior and forces core to own the merge and validation
  rules.
- The extension resolves `$ref` while it parses its own keys. This is contained to
  Foundry and needs no core change.

Recommend the extension owns resolution for the first version, with a clear path to move
it into core later if other extensions need includes.

**Resolution rules.**

- A relative `$ref` path resolves relative to the file that holds it, so nested includes
  work. Per `FileRef.json`, absolute paths and URLs are also accepted. The security
  tradeoff of remote and absolute includes (reading arbitrary files, fetching remote
  content) is raised as an open question rather than restricted here, to stay consistent
  with the brief.
- Sibling keys overlay on the loaded file. Use a shallow overlay at the top level of the
  object. Scalars and arrays from the sibling replace the loaded value. This keeps the
  result easy to predict.
- A loaded file is validated against the same per resource schema as an inline entry.

**Path resolution and rebasing.** The extension receives its keys as already parsed data,
so the `$ref` strings arrive as plain values. To open a top level `$ref` it needs the
directory that holds `azure.yaml`, not the agent source path, so the provider must get the
project root from the azd client or environment (the gRPC `ServiceConfig` carries the
agent `project` path, not the project root). Once a split file is loaded, every relative
path inside it rebases to that file's own directory, not to `azure.yaml`. The `complex`
sample is explicit: in `agents/support-agent.yaml`, `project: ../src/support-agent` is
relative to the `agents/` folder, and in `skills/code-review.yaml`,
`instructions: ../prompts/code-review.md` is relative to the `skills/` folder. The
resolver must track each loaded file's base directory and apply it to that file's
`project`, `instructions`, and any nested `$ref`.

**Instruction and prompt files.** Separate from `$ref`, a skill's `instructions` and a
prompt agent's `instructions` accept either an inline string or a path to a `.md` or
`.txt` file the extension reads at deploy time; the `complex` sample uses both forms.
This is a file read, not a structural include: the file's text becomes the field value,
with no overlay. The path follows the same rebasing rule above, so an `instructions:`
path inside a split agent or skill file resolves against that file's directory. `${VAR}`
and `${{...}}` expansion (2.5) applies to the loaded text.

**Interaction with #8049.** The composition commands write into these same arrays. If a
section is split into a file, the writer has to decide whether to append an inline entry
or edit the referenced file. Define the default as: append inline, and only edit a split
file when the command explicitly targets it. Both this design and #8049 should share one
YAML edit helper that understands `$ref` entries, so reads and writes agree.

### 2.5 Templating: `${VAR}` and `${{...}}`

Two resolvers have to coexist without stepping on each other.

- `${VAR}` is an azd environment value, resolved on the client before anything is sent
  to Foundry. Example: a connection secret read from the azd environment.
- `${{...}}` is resolved by Foundry on the server, for example
  `${{connections.x.credentials.key}}`. It must reach Foundry unchanged.

Core already leaves this alone. `pkg/osutil/expandable_string.go` runs `${VAR}`
expansion with `drone/envsubst`, but core only applies it to typed fields such as the
resource name and image. It does not expand `Config` or `AdditionalProperties`, so
`${{...}}` passes through core untouched.

That moves the work to the extension. The risk is that if the extension expands `${VAR}`
with the same library, the library may try to read `${{...}}` as well. The extension
needs an expander that touches only `${VAR}` and leaves `${{...}}` intact. A simple and
safe approach: protect every `${{...}}` span with a placeholder, run `${VAR}` expansion,
then restore the placeholders. Put this in one shared helper so every Foundry field
expands the same way.

Which fields take which:

| Field | `${VAR}` | `${{...}}` |
|---|---|---|
| Agent `env` values | yes | yes |
| Connection credentials | yes (secret from azd env) | yes (Foundry managed) |
| Routine `input` values | yes | yes |
| Model deployment names and SKUs | yes | no |

### 2.6 Service target lifecycle and per agent fan out

The new provider implements the same interface as the current agent provider
(`Initialize`, `Package`, `Publish`, `Deploy`, `Endpoints`, `GetTargetResource`), but a
single service now stands for a whole project, so several methods fan out across the
agents inside it.

| Method | Behavior |
|---|---|
| `Initialize` | Validate the Foundry config. Check agent kinds; for each `kind: hosted` agent require exactly one deploy mode (`docker`, `runtime`, or a prebuilt `image`), while `kind: prompt` agents carry none; and check that every toolbox, `tools` entry, `skill`, connection, and model deployment an agent references resolves. Resolve the project, provisioned or via `endpoint`. |
| `Package` | For each agent that has `project` plus `docker` or `runtime`, build. `docker` builds an image, local or in ACR when `remoteBuild` is set. `runtime` zips the source. Prompt agents and prebuilt `image` agents skip this. |
| `Publish` | Push each artifact. Images go to ACR. Zips upload to Foundry storage. |
| `Deploy` | First reconcile project state: deployments, connections, toolboxes, skills, and prompt agents through Foundry APIs. Then post each hosted agent with `createAgentVersion` and the published artifact reference. Reconcile routines last, since they reference an agent by name. |
| `Endpoints` | Return the project endpoint, plus per agent endpoints when they are known. |
| `GetTargetResource` | Resolve the ARM resource for the Foundry project. |

Deploy order inside the project matters, because later items reference earlier ones.
Apply deployments and connections first, then toolboxes and skills, then agents, and
routines last, since a routine references an agent by name.

Fan out details to define in code:

- Build and publish can run agents concurrently. Bound the concurrency and aggregate the
  results.
- A single agent failure stops the run with an error that names the agent, so the user
  is not left guessing which one broke. Core only sees one service fail, so this naming
  has to come from the extension.
- The project state reconcile in `Deploy` lifts out of the current post provision and
  post deploy hook handlers in `listen.go`, which already do this work for the old host.

### 2.7 Reworking `init`

Today `init` reconciles data across three files. The functions
`extractToolboxAndConnectionConfigs` and `extractConnectionConfigs` in
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go` read the manifest, normalize
auth types, move secrets into the azd environment, and feed typed arrays that then get
written across `agent.yaml` and `azure.yaml`. The file write for `agent.yaml` happens in
`writeAgentDefinitionFile`.

After this change `init` writes the Foundry service entry directly. The roughly two
hundred lines of cross file reconciliation collapse into building the in memory config
and writing it to the service top level fields. `agent.yaml` and `agent.manifest.yaml`
are no longer produced for new projects.

Deprecation detection lives here too. If the old files are present, or the project uses
`host: azure.ai.agent`, print the warning, run the fallback that produces the equivalent
in memory Foundry service, and emit the telemetry signal. The error code
`CodeInvalidAgentManifest` in `internal/exterrors/codes.go` stays while the manifest path
is read. Rename or retire it when that path is removed.

### 2.8 State reconciliation and idempotency

Deploy compares the declared state in `azure.yaml` against the live state in Foundry and
applies the difference. The model is upsert: create what is missing, update what changed,
and leave the rest. Removing an entry from `azure.yaml` stops azd from managing it, but
does not delete it from Foundry. Deletion is the job of `azd down` or the per resource
`azd ai` commands.

Two Foundry behaviors to confirm against the API contracts, because they decide how the
upsert is written:

- Whether create calls are idempotent, or whether the provider has to check first and
  then choose create or update.
- Whether a re run after a partial failure can safely repeat the calls that already
  succeeded.

This area overlaps with existing reports [#8349](https://github.com/Azure/azure-dev/issues/8349)
and [#8350](https://github.com/Azure/azure-dev/issues/8350); the upsert model above and the
brief's "drop from config stops management, does not destroy" semantics are meant to cover
them.

### 2.9 Telemetry and errors

- Emit a telemetry event when the old file fallback runs and when `host: azure.ai.agent`
  is seen, so the deprecation window length can be driven by data.
- Surface per agent failures as structured errors that name the agent. Core sees one
  service, so the attribution has to be added by the extension.
- Keep `CodeInvalidAgentManifest` while the manifest path exists, and plan its rename or
  removal with that path.

### 2.10 Sibling extensions and schema ownership

Foundry already ships per resource extensions, bundled by the `microsoft.foundry`
meta-package: `azure.ai.toolboxes`, `azure.ai.connections`, `azure.ai.projects`,
`azure.ai.skills`, and `azure.ai.routines`. Each owns a data-plane CLI (`azd ai toolbox`,
`azd ai connection`, and so on) that acts on a live project. None of them participate in
`azure.yaml` today.

That raises who owns the `microsoft.foundry.json` schema slices and their reconciliation.
Following the brief, v1 takes **Option A**: the `azure.ai.agents` extension owns the full
schema and reconciles every slice (deployments, connections, toolboxes, skills, routines,
agents), while the sibling extensions keep their imperative CLIs unchanged. The
alternative, Option B, has the meta-package own the schema and each sibling register a
slice contribution, which needs a new "one extension contributes part of another's schema"
mechanism in core. Recommend A for v1 and re-evaluate B once the shape is in users' hands.

One naming note: `microsoft.foundry` is both the host kind string and the existing
meta-package extension id. That overlap is intentional in the brief, but the host kind is
resolved by `azure.ai.agents`, not by the meta-package.

## Open questions

Decisions this design surfaces that the brief does not already settle. Provision and scope
items sit in the same list as the design items.

1. **Provision layers in multi service projects.** Issue
   [#8587](https://github.com/Azure/azure-dev/issues/8587) reports `azd provision <agent>`
   failing with "no layers defined in azure.yaml", which left a toolbox unprovisioned. The
   single service shape and the in memory Bicep both interact with how provision layers are
   built, so confirm the intended behavior.
2. **Reusing an existing project.** The `endpoint:` field and the private network case in
   issue [#8165](https://github.com/Azure/azure-dev/issues/8165) both ask azd to use an
   account it did not create. Confirm whether reuse is in scope for the first version.
3. **Agent versioning.** Issue [#8066](https://github.com/Azure/azure-dev/issues/8066)
   notes that `azure.yaml` only represents the latest state of an agent, even though agents
   are versioned. Deploy posts a new version each run, so the intended meaning of the YAML,
   latest only or pinned, needs a decision.
4. **`$ref` resolution and overlay rules.** Core loader or extension. Shallow overlay or
   deep merge. Arrays replaced or merged. 2.4 recommends extension owned with a shallow
   overlay, but this should be ratified.
5. **Absolute and remote `$ref` paths.** `FileRef.json` accepts absolute paths and URLs.
   Confirm whether to keep that, accepting reading arbitrary files and fetching remote
   content, or restrict to project-local paths behind an opt-in. The design follows the
   brief and accepts them for now (2.4).
6. **Split file validation.** The language server can follow a `$ref` to a local file for
   editor hints, but runtime validation of a loaded file against the per resource schema
   needs to be confirmed.
7. **Inline config size over gRPC.** A big project becomes a large protobuf struct. Confirm
   there is no practical size limit, or define how to chunk it.
8. **Composition surface naming.** Issue #8049 places the `add` commands in an `azd ai
   project` surface, but an `azure.ai.projects` extension already exists. Confirm where the
   schema and the `add` commands live so the two do not collide.

## Summary of required changes

The brief's "Missing functionality" table already lists the baseline work. This spec
keeps that list and adds the deltas below.

azd core:

- Add the `host: microsoft.foundry` conditional to `schemas/v1.0/azure.yaml.json`,
  composing the extension schema at the service level and turning off `project`,
  `runtime`, `docker`, `image`, and `config`.
- Recommend `additionalProperties: true` at the project level in `microsoft.foundry.json`
  (the preview currently has `false`).
- Optional: register `microsoft.foundry` as an alpha feature for a gated preview, and a
  shared helper if core ever expands these fields while preserving `${{...}}`.

`azure.ai.agents` extension (v1 owns the full schema and reconciliation, Option A in 2.10):

- Publish `microsoft.foundry.json` and the per resource schema files.
- Add the `microsoft.foundry` service target with per agent fan out for package, publish,
  and deploy; require a deploy mode only for `kind: hosted` agents; reconcile routines
  after agents.
- Resolve `$ref` includes with shallow overlay overrides, rebasing each loaded file's
  paths to its own directory, and load `instructions` prompt files at deploy time.
- Expand `${VAR}` while preserving `${{...}}` through a shared helper.
- Rework `init` to write the single Foundry service and stop emitting `agent.yaml` and
  `agent.manifest.yaml`.
- Add the deprecation fallback and telemetry for the old files and `host: azure.ai.agent`.
- Add skills and routines reconciliation.
