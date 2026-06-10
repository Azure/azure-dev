# Bicep-less `azd ai agent init` via Extension-Owned Provisioning

## Problem

`azd ai agent init` clones `Azure-Samples/azd-ai-starter-basic` into every
new project, dropping ~300 lines of conditional Bicep (`shouldCreateAcr`,
`useExistingAiProject ? X : Y`, 30+ outputs) on the developer's disk before
they write any agent code. The starter Bicep lives in a sample repo on
`main`, so slimming it would break every project initialized by every prior
extension build.

See RFC [#8065](https://github.com/Azure/azure-dev/issues/8065) for the full
problem statement.

## Solution

Move infrastructure templates from `Azure-Samples/azd-ai-starter-basic` into
the `azure.ai.agents` extension binary. `azd ai agent init` produces only
`azure.yaml` and an agent code project — no `infra/` directory. At provision
time, the extension's own provisioning provider (registered via the
[PR #7482](https://github.com/Azure/azure-dev/pull/7482) framework)
synthesizes Bicep in memory from `azure.yaml` and applies it.
`azd ai agent init --infra` ejects on demand: the same synthesis writes Bicep
to `./infra/`, and subsequent provisions read from disk. The developer opts
in by declaring `infra.provider: microsoft.foundry` in `azure.yaml`.

The `azure.yaml` shape is fixed by the
[Foundry `azure.yaml` reference](https://github.com/therealjohn/foundry-azd-config-preview/blob/main/REFERENCE.md):
a single `host: microsoft.foundry` service per project, with nested
`agents:`, `deployments:`, `connections:`, `toolboxes:`, etc. This spec
only changes how that file is *provisioned*; it does not redesign the YAML.

## Scope

**In scope:**

- Bicep-less default behavior for `azd ai agent init`
- `azd ai agent init --infra` eject command
- Embedded templates inside the `azure.ai.agents` extension
- Retiring `Azure-Samples/azd-ai-starter-basic` as the init target
- Schema updates to allow extension-named providers in `infra.provider`
- ARM-backed synthesis only: Foundry project + model deployments + ACR
  (when needed for container agents)

**Out of scope:**

- **`azd deploy`** — agent code push, data-plane reconciliation (connections,
  toolboxes, skills, routines, agent definitions) are all the deploy verb's
  job, not provisioning's. This spec ends at "ARM resources are in place."
- `$ref:` resolution, `skills:`, `routines:`, agent-level `tools:`/`skill:` —
  data-plane state. Synthesizer reads them only to skip them; `azd deploy`
  reconciles them via Foundry APIs.
- Unified `azure.yaml` schema for `host: microsoft.foundry` —
  [#7962](https://github.com/Azure/azure-dev/issues/7962)
- `azd ai agent add` and incremental composition —
  [#8049](https://github.com/Azure/azure-dev/issues/8049)
- Service-host-driven provider auto-routing (removes the explicit
  `infra.provider:` declaration) — RFC #8065 Core Ask #1
- Coexistence with non-Foundry services in the same project — users with
  mixed projects use `infra.layers[]` to scope `microsoft.foundry` to their
  Foundry services; mixed-provider auto-routing is a future spec.

## Activation

| Trigger                                       | Behavior                                                                                       |
| --------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `azd ai agent init` (default)                 | Write `azure.yaml` + agent code project. No `./infra/`. `azure.yaml` includes `infra.provider: microsoft.foundry`. |
| `azd ai agent init --infra`                   | Same as default, plus synthesize and write Bicep to `./infra/`. Project starts on-disk.        |
| `azd ai agent init --infra` (existing project, no `./infra/`) | Synthesize current `azure.yaml` and write `./infra/`. Do not re-prompt or touch agent code. Refuse if `./infra/` already exists. |
| `azd provision` (no `./infra/`)               | Extension synthesizes Bicep in memory, applies via ARM SDK.                                    |
| `azd provision` (with `./infra/`)             | Extension reads from `./infra/` instead of synthesizing. Same ARM-side output.                 |

## Architecture

```
cli/azd/extensions/azure.ai.agents/
  internal/cmd/init.go             ← gen azure.yaml; gen --infra path
  internal/cmd/listen.go           ← register provider via
                                     WithProvisioningProvider("microsoft.foundry", ...)
  internal/project/provisioning.go ← FoundryProvisioningProvider implementation
  internal/synthesis/              ← in-memory Bicep generation from azure.yaml
    synthesizer.go                 ← top-level: ServiceConfig → template files
    project.bicep.tmpl             ← embedded template: Foundry project + deps
    deployments.bicep.tmpl         ← embedded template: model deployments
    acr.bicep.tmpl                 ← embedded template: ACR (if any nested
                                     agent has a docker: block)
    *.tmpl                         ← other embedded templates
  internal/deploy/
    bicep_runner.go                ← ARM SDK deployment wrapper
    parameters.go                  ← parameter resolution (env vars, prompts)
  extension.yaml                   ← +capability: provisioning-provider
                                     +providers: {name: microsoft.foundry,
                                                  type: provisioning-provider}

cli/azd/pkg/                       ← Core changes (small)
  infra/provisioning/provider.go   ← (no change needed)

schemas/v1.0/azure.yaml.json       ← relax infra.provider enum → examples
```

## Provider Resolution

The extension's provider plugs into the existing IoC-registered factory.
Provider selection (`cli/azd/pkg/infra/provisioning/manager.go:505-540`) is
unchanged:

```go
providerKey := m.options.Provider                    // from azure.yaml infra.provider
if providerKey == NotSpecified {
    defaultProvider, _ := m.defaultProvider()        // "bicep"
    providerKey = defaultProvider
}
err = m.serviceLocator.ResolveNamed(string(providerKey), &provider)
```

Built-in providers register at `cli/azd/pkg/azd/default.go:79-87`. Extension
providers register at runtime via `RegisterProvisioningProviderRequest`
(`cli/azd/internal/grpcserver/provisioning_service.go:138-152`) into the same
container. `bicep` and `microsoft.foundry` are equivalent keys to the resolver.
`ParseProvider` (`cli/azd/pkg/infra/provisioning/provisioning.go:53-57`) was
relaxed in PR #7482 to accept any string.

## Explicit `infra.provider:` Declaration

The RFC ideal is service-host-driven auto-routing — the extension is picked
because `host: microsoft.foundry` is present, not because `infra.provider:`
is declared. Verified gap (`cli/azd/pkg/project/importer.go:288-358`):
`ProjectInfrastructure` never inspects `service.Host` to pick a provisioning
provider. The Aspire branch (the only service-driven precedent) and the
compose branch both hard-code Bicep.

Adding service-host auto-routing requires a net-new branch in
`ProjectInfrastructure` plus a host→extension registry. We defer that and
ship an explicit declaration. The
[reference YAML](https://github.com/therealjohn/foundry-azd-config-preview/blob/main/REFERENCE.md)
omits `infra.provider:`; this spec requires it as a one-line addition until
auto-routing lands:

```yaml
infra:
  provider: microsoft.foundry         # added by `azd ai agent init`

services:
  my-project:
    host: microsoft.foundry
    # endpoint: ${FOUNDRY_PROJECT_ENDPOINT}   # uncomment for brownfield
    deployments:
      - name: gpt-4.1-mini
        model: { format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14" }
        sku:   { capacity: 10, name: GlobalStandard }
    connections: [ ... ]              # data-plane, ignored by synthesizer
    toolboxes:   [ ... ]              # data-plane, ignored by synthesizer
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        docker: { path: Dockerfile, remoteBuild: true }
        # … rest is data-plane, ignored by synthesizer
```

The synthesizer only reads the ARM-backed fields (project itself, model
`deployments`, and ACR if any nested agent has a `docker:` block); the
rest is the deploy verb's job and out of scope for this spec.

**Trade:** developers see a provider name in the `infra.provider:` slot —
mild friction, but `microsoft.foundry` matches the host kind and reads
naturally next to `bicep` / `terraform`. Removed once service-host
auto-routing lands.

## On-Disk Reuse (Post-Eject Behavior)

`azure.yaml` is **never mutated by eject**. The extension's provider decides
internally whether to synthesize or read from disk:

```go
// FoundryProvisioningProvider.Deploy(ctx)
if exists("./infra/main.bicep") {
    templates = readFromDisk("./infra/")
} else {
    templates = synthesizeFromYAML(serviceConfig)
}
return deployTemplates(ctx, templates)
```

The developer sees one `infra.provider: microsoft.foundry` declaration that
holds across both modes. Eject is a pure file-write operation; `azure.yaml`
stays clean.

Verified: all Core sites that read `./infra/` tolerate a missing directory:

| Site                                                                  | Behavior when `./infra/` is absent                |
| --------------------------------------------------------------------- | ------------------------------------------------- |
| `cli/azd/pkg/project/importer.go:323` (`pathHasModule` call)          | `os.ReadDir` returns NotExist → caller's `err == nil && moduleExists` guard falls through |
| `cli/azd/pkg/project/project.go:187` (`hooksFromInfraModule` call)    | Returns empty → no hooks merged                   |
| `cli/azd/pkg/infra/provisioning/manager.go:125` (`azdFileShareUploadOperations` call) | Missing dir → no operations               |
| `cli/azd/pkg/project/importer.go:304` (`detectProviderFromFiles` gate) | Only runs when `Provider == NotSpecified`; with our explicit declaration, never executes |

## In-Memory Synthesis

The extension owns the Bicep deployment pipeline. Composition:

```
ServiceConfig (host: microsoft.foundry, from azure.yaml)
    │
    ▼
synthesis.Synthesizer
    │   - validates against azure.ai.agent.json schema
    │   - reads ARM-backed fields only:
    │       * project itself (or skip if endpoint: set)
    │       * deployments[]
    │       * agents[].docker → triggers ACR
    │       * agents[].image  → no ACR (pre-built image)
    │   - skips data-plane fields (connections, toolboxes, skills,
    │     routines, agents[].tools, agents[].skill, $ref)
    │   - resolves ${VAR} from azd env; passes ${{...}} through verbatim
    ▼
[]TemplateFile (main.bicep, modules/*.bicep, main.parameters.json)
    │
    ▼
deploy.BicepRunner
    │   - resolves remaining parameters (prompts, env)
    │   - calls ARM REST: deployments.CreateOrUpdate
    │   - streams progress via grpcbroker.ProgressFunc
    │   - captures outputs (project endpoint, ACR login server, etc.)
    ▼
ProvisioningDeployResult (back to azd Core via gRPC)
```

The extension does **not** delegate deployment to Core's Bicep provider — no
such delegation API exists today (`cli/azd/grpc/proto/deployment.proto`
exposes only `GetDeployment`/`GetDeploymentContext`). The extension
reimplements the deploy step using `armresources.DeploymentsClient`; a future
Core API could expose a shared Bicep-deploy path to avoid drift.

### What the synthesizer ignores (deploy verb's job)

The reference YAML carries a lot of data-plane state that has no ARM
representation. The synthesizer reads these only to skip them; `azd deploy`
reconciles them via Foundry APIs (out of scope for this spec):

| YAML field                          | Why ignored at synthesis                                  |
| ----------------------------------- | --------------------------------------------------------- |
| `connections:`                      | Foundry data-plane resource, not ARM                      |
| `toolboxes:`                        | Foundry data-plane resource                               |
| `skills:`                           | Foundry data-plane resource                               |
| `routines:`                         | Foundry data-plane resource                               |
| `agents[].tools:`                   | Agent definition, posted via Foundry API at deploy        |
| `agents[].skill:`                   | Reference to a skill (data-plane)                         |
| `agents[].protocols:`               | Agent definition                                          |
| `agents[].env:`                     | Agent runtime env, applied at deploy                      |
| `agents[].startupCommand:`          | Agent runtime config                                      |
| `agents[].container.resources:`     | Agent runtime config                                      |
| `agents[].runtime:`                 | Code-deploy mode marker; deploy verb's job                |
| `$ref:` (anywhere)                  | Loaded but contents treated as data-plane; not validated  |

## Validation Pipeline

Synthesis runs only on a valid `azure.yaml`. Order, all before Bicep is
generated:

1. **Schema validation** — each `host: microsoft.foundry` service's body
   against `azure.ai.agent.json`. Failures surface with field path:
   `services.my-project.deployments[0].sku: required`.
2. **Service graph invariants** — at least one `host: microsoft.foundry`
   service exists. Multiple are allowed (each is its own Foundry project).
   `uses:` between Foundry services and other azd services is honored as
   normal ordering — but synthesis only acts on the Foundry-hosted ones.
3. **Per-agent deploy-mode invariant** — each entry in `agents[]` has
   exactly one of `docker:`, `runtime:`, or `image:`. Two or more = error.
   None = error.
4. **Env reference resolution** — every `${VAR}` in ARM-backed fields
   (project endpoint, deployment name/SKU/capacity, ACR options) must
   resolve from the azd environment. `${{...}}` is opaque to the
   synthesizer — it is passed through verbatim for Foundry to resolve
   server-side at runtime.
5. **Brownfield consistency** — if `endpoint:` is set on the Foundry
   service, the value must look like a Foundry project endpoint URL
   (`https://<account>.services.ai.azure.com/api/projects/<project>` or
   equivalent). Reachability is a deploy-time check, not synthesis.

All five run on every `provision`, `preview`, and `init --infra`. Data-plane
fields (connections, toolboxes, skills, routines, agent-level tools/skill,
`$ref:` contents) are not validated here — the deploy verb owns them.

## Brownfield: Existing Foundry Projects

Replaces today's `USE_EXISTING_AI_PROJECT` / `AZURE_AI_PROJECT_ID` env vars
(which the starter Bicep branches on) with the
[reference doc's](https://github.com/therealjohn/foundry-azd-config-preview/blob/main/REFERENCE.md)
`endpoint:` field on the Foundry service:

```yaml
services:
  my-project:
    host: microsoft.foundry
    endpoint: ${FOUNDRY_PROJECT_ENDPOINT}   # presence → existing-project mode
    deployments: [ ... ]
    agents:       [ ... ]
```

When `endpoint:` is set, synthesis omits the Foundry project ARM resource
and generates references to wire `FOUNDRY_PROJECT_ENDPOINT`,
`AZURE_RESOURCE_GROUP`, and tenant/subscription/location from the existing
project (resolved at deploy time via the endpoint). It still synthesizes
ARM-backed children declared inline — additional model `deployments[]`,
ACR if any nested agent has a `docker:` block. The
`useExistingAiProject` ternary collapses to a single field-presence check.

The endpoint URL (not the ARM resource ID) is the user-facing identifier in
the reference doc, matching what `az` CLI and the Portal display. The deploy
verb resolves the ARM ID from the endpoint when it needs control-plane
access; synthesis treats `endpoint:` purely as a "skip ARM project creation"
signal.

## Eject Command (`azd ai agent init --infra`)

Infra-only operation. Four contexts:

| Context                                   | Behavior                                                                                       |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Empty directory                           | Run init normally + write `./infra/` from synthesis.                                           |
| Existing Bicep-less azd agent project     | Synthesize current `azure.yaml`; write `./infra/`. Do not re-prompt; do not touch agent code. Do not modify `azure.yaml` (`infra.provider:` stays `microsoft.foundry`). |
| Existing on-disk project (`./infra/` exists) | Refuse to overwrite. Print: *"`./infra/` already exists. To regenerate from `azure.yaml`, delete the `infra/` directory and run the command again."* |
| Not an azd agent project                  | Refuse: "no `host: microsoft.foundry` service found in `azure.yaml`; nothing to eject." |

Eject is **all-or-nothing for the whole project** — no partial mode where
some agents synthesize and others sit on disk. To regenerate, the user
deletes `./infra/` themselves and re-runs the command. No `--force`, no
implicit destruction of user-owned files.

Example output:

```
> azd ai agent init --infra

Generating infrastructure files from azure.yaml...

  Created infra/main.bicep
  Created infra/main.parameters.json
  Created infra/modules/foundry-project.bicep
  Created infra/modules/acr.bicep

Future provisions will read from ./infra/.

Next steps:
  azd provision    Apply changes
```

Refused:

```
> azd ai agent init --infra

Error: ./infra/ already exists.

If you want to regenerate from azure.yaml, delete the infra directory
and run the command again.
```

## Post-Eject CLI Behavior

CLI commands keep modifying `azure.yaml` after eject. Drift risk: `azure.yaml`
declares something requiring a new ARM resource (e.g., second container agent
needing ACR), but on-disk Bicep doesn't have it.

| Command class                                                | Bicep-less project        | On-disk project (post-eject)                                                         |
| ------------------------------------------------------------ | ------------------------- | ------------------------------------------------------------------------------------ |
| Modifies data-plane only (`add tool`, `add toolbox`)         | Apply normally            | Apply normally — nothing in Bicep changes                                            |
| Modifies `azure.yaml` requiring new ARM resources            | Apply; next `provision` synthesizes the new resources | Apply to `azure.yaml` and warn: "your project uses on-disk Bicep; delete `./infra/` and run `azd ai agent init --infra` to regenerate, or edit `infra/` manually" |
| Eject (`init --infra`)                                       | Allowed                   | Refused — user must delete `./infra/` and re-run                                     |

CLI never silently patches user-owned Bicep.

**Accepted trade.** Post-eject, the user-driven `rm -rf ./infra/ && azd ai
agent init --infra` flow throws away any hand-edits the user made. We pick
this over auto-diff/merge (which would re-introduce silent rewrites of
user-owned files) and over refusing `add` post-eject (which would gut the
CLI for ejected projects). Auto-merge is future work, out of scope here.

## Core Changes Required

Small, mechanical. All ride alongside `azure.ai.agents` extension work.

### 1. Relax `infra.provider` enum in schemas

| File                                  | Change                                                         |
| ------------------------------------- | -------------------------------------------------------------- |
| `schemas/v1.0/azure.yaml.json:44-52`  | Change `enum: ["bicep","terraform"]` → `examples: [...]`       |
| `schemas/alpha/azure.yaml.json:44-52` | Same                                                           |

Without this, `infra.provider: microsoft.foundry` fails IDE schema
validation despite being runtime-valid.

> **Note on `uses` / `runtime`:** the original RFC asked Core to surface
> `services.<svc>.uses` and a typed `services.<svc>.runtime` on the
> extension-facing proto. With the consolidated `host: microsoft.foundry`
> shape, agents and their runtimes are *nested* inside the service body
> (`agents[].runtime`, `agents[].docker`, `agents[].image`), not separate
> services. The extension reads them through the existing
> `additional_properties` channel; no proto/struct changes needed for v1.

### 2. (Optional, deferred) Auto-install for `provisioning-provider` extensions

Today: `cli/azd/cmd/auto_install.go:511-578` auto-installs extensions for
unknown `service-target-provider` host kinds. No equivalent for
`provisioning-provider`. Tracked as `#7502`.

Acceptable to defer — developers writing `infra.provider: microsoft.foundry`
have opted in explicitly. `azd ai agent init` force-installs the extension at
init time anyway. The failure mode is `git clone` + `azd up` on a fresh
machine where the README is the install instruction.

## Extension Changes Required

### Schemas

The reference YAML keys (project + nested `agents:`, `deployments:`,
`connections:`, `toolboxes:`, `skills:`, `routines:`) are governed by the
existing `azure.ai.agent.json` schema the agents extension already publishes
(`cli/azd/extensions/azure.ai.agents/schemas/azure.ai.agent.json`). The
schema covers both ARM-backed fields (read by the synthesizer) and
data-plane fields (read by the deploy verb). `additionalProperties: true`
keeps it forward-compatible with future Foundry resource types.

### Embedded templates

`cli/azd/extensions/azure.ai.agents/internal/synthesis/*.tmpl` — Go-embedded
Bicep templates, versioned with the extension. Templates are tailored: ACR
only included when at least one entry in `agents[]` has a `docker:` block;
monitoring only when explicitly added via `azd ai agent add monitoring` (per
#8049). Replaces `Azure-Samples/azd-ai-starter-basic` Bicep entirely.

### Provider implementation

`internal/project/provisioning.go` implements
`azdext.ProvisioningProvider` (`cli/azd/pkg/azdext/provisioning_manager.go:23-36`).
Registered via `WithProvisioningProvider("microsoft.foundry", factory)` in
`internal/cmd/listen.go`. `extension.yaml` adds the
`provisioning-provider` capability and declares the provider:

```yaml
capabilities:
  - custom-commands
  - lifecycle-events
  - mcp-server
  - service-target-provider
  - provisioning-provider          # new
  - metadata
providers:
  - { name: azure.ai.agent,    type: service-target }       # existing
  - { name: microsoft.foundry, type: provisioning-provider } # new
```

Method behaviors:

| Method            | Implementation                                                                 |
| ----------------- | ------------------------------------------------------------------------------ |
| `Initialize`      | Validate `azure.yaml` (5-step pipeline above); resolve env vars                |
| `State`           | Query ARM for last deployment; return outputs                                  |
| `Deploy`          | If `./infra/` exists, read from disk; else synthesize. Apply via ARM SDK.      |
| `Preview`         | Synthesize (or read from disk), then call ARM What-If; return diff summary. Mirrors Core's Bicep provider. |
| `Destroy`         | Delete resource group or use deployment stacks                                 |
| `EnsureEnv`       | Prompt for required env vars (subscription, location) if missing              |
| `Parameters`      | Return parameter list from synthesized/on-disk template                        |
| `PlannedOutputs`  | Return output list from synthesized/on-disk template                           |

## Stability Contract

Synthesis output is best-effort stable within a patch extension version.
Same `azure.yaml` → semantically identical Bicep. Across minor versions,
the output may change; documented in the changelog with recommendation to run
`azd provision --preview` after upgrades.

## Telemetry

| Field                          | Values                       | Where emitted                         |
| ------------------------------ | ---------------------------- | ------------------------------------- |
| `provision.synthesis_source`   | `embedded` \| `on_disk`      | `Deploy()` start                      |
| `init.infra_flag`              | `true` \| `false`            | `azd ai agent init` start             |

Lets us measure eject rate and confirm the Bicep-less default sticks.

## Downstream Impact

- **`Azure-Samples/azd-ai-starter-basic`** — retired as init target. Repo stays
  as reference. Sample README points at extension.
- **Other AZD samples that embed agent definitions** — `azd init -t <sample>`
  unchanged. Those samples bring their own `infra/` and the extension respects
  them. Only default `azd ai agent init` (no `-t`) goes Bicep-less.
- **Foundry Toolkit (VS Code)** — reads `azure.yaml`; absence of `./infra/`
  is normal, not corruption. No new files to parse.
- **Migration** — projects created by prior extension versions already have
  `infra/` on disk; they stay on the on-disk path. No action needed.
- **Documentation** — new doc covering Bicep-less default, eject command, and
  stability contract.

## Risks

| Risk                                                        | Mitigation                                                                                       |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `infra.provider: microsoft.foundry` confuses developers     | Documented in extension README; removed once service-host auto-routing lands                     |
| Extension's Bicep deployment drifts from Core's             | Pin to specific ARM SDK version; integration tests vs. Core's bicep provider for parity         |
| Synthesis output changes between minor versions             | Changelog notes; `azd provision --preview` recommended after upgrade                             |
| Brownfield projects with custom Bicep edits hit eject + drift | Eject is opt-in; first-time eject just writes synthesized Bicep, no merge logic                 |
| Auto-install gap (#7502) bites a teammate cloning the repo  | README install instruction until #7502 lands                                                     |

## Open Questions

1. Should the extension's `Deploy()` warn when both `./infra/` exists and
   `azure.yaml` config has changed since last eject? (Drift detection.)
   **Proposal:** no detection — matches "on-disk Bicep is the source of
   truth"; CLI `add` commands already warn at the entry point. Revisit when
   auto-merge lands.
2. Schema branch for typed `host: microsoft.foundry` validation in the v1
   `azure.yaml.json` (per #7962) — does it land in this RFC's PRs or #7962's?
   **Proposal:** #7962 owns the schema branch, since it defines the field
   shapes. Extension validates against its own embedded `azure.ai.agent.json`
   schema at runtime, so IDE schema lag during the gap is cosmetic.

## Test Plan

- Unit: synthesizer determinism (same input → byte-equal output)
- Unit: validation pipeline error paths (all five steps)
- Unit: `ResolveNamed("microsoft.foundry")` returns extension provider
- Unit: synthesizer ignores data-plane fields (`connections:`, `toolboxes:`,
  `skills:`, `routines:`, agent-level `tools:`/`skill:`, `$ref:`) without
  error
- Unit: `${{...}}` passes through synthesis unchanged; `${VAR}` resolves
- Integration: `azd ai agent init` produces no `./infra/`
- Integration: `azd provision` succeeds with synthesized templates against a
  Foundry project with one container agent (ACR included) and one code-deploy
  agent (no ACR)
- Integration: `azd ai agent init --infra` writes `./infra/`; next
  `azd provision` reads from disk (verified via extension log)
- Integration: brownfield `endpoint:` skips ARM project creation
- E2E: `init` → `provision` → `down` on a single-agent project (deploy is
  out of scope for this spec)
- E2E: `init --infra` → manual edit of `infra/main.bicep` → `provision`
  applies the edit
- Regression: projects created by prior extension versions with on-disk Bicep
  continue to work (extension reads `./infra/` like today)

## References

- RFC [#8065](https://github.com/Azure/azure-dev/issues/8065) — original
- Issue [#7962](https://github.com/Azure/azure-dev/issues/7962) — unified
  schema (dependency)
- Issue [#8049](https://github.com/Azure/azure-dev/issues/8049) — incremental
  composition (parallel)
- PR [#7482](https://github.com/Azure/azure-dev/pull/7482) — custom
  provisioning provider framework (merged)
- Issue [#7502](https://github.com/Azure/azure-dev/issues/7502) — auto-install
  for provisioning providers (deferred dependency)
- Reference: [therealjohn/foundry-azd-config-preview](https://github.com/therealjohn/foundry-azd-config-preview/blob/main/REFERENCE.md) — target `azure.yaml` shape
