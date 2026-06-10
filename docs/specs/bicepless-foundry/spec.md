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
in by declaring `infra.provider: azure.ai.agents` in `azure.yaml`.

## Scope

**In scope:**

- Bicep-less default behavior for `azd ai agent init`
- `azd ai agent init --infra` eject command
- Embedded templates inside the `azure.ai.agents` extension
- Retiring `Azure-Samples/azd-ai-starter-basic` as the init target
- Schema updates to allow extension-named providers in `infra.provider`

**Out of scope:**

- Unified `azure.yaml` schema for `azure.ai.project` / `azure.ai.agent` host
  kinds — [#7962](https://github.com/Azure/azure-dev/issues/7962)
- `azd ai agent add` and incremental composition —
  [#8049](https://github.com/Azure/azure-dev/issues/8049)
- Service-host-driven provider auto-routing (removes the explicit
  `infra.provider:` declaration) — RFC #8065 Core Ask #1

## Activation

| Trigger                                       | Behavior                                                                                       |
| --------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `azd ai agent init` (default)                 | Write `azure.yaml` + agent code project. No `./infra/`. `azure.yaml` includes `infra.provider: azure.ai.agents`. |
| `azd ai agent init --infra`                   | Same as default, plus synthesize and write Bicep to `./infra/`. Project starts on-disk.        |
| `azd ai agent init --infra` (existing project, no `./infra/`) | Synthesize current `azure.yaml` and write `./infra/`. Do not re-prompt or touch agent code. Refuse if `./infra/` already exists. |
| `azd provision` (no `./infra/`)               | Extension synthesizes Bicep in memory, applies via ARM SDK.                                    |
| `azd provision` (with `./infra/`)             | Extension reads from `./infra/` instead of synthesizing. Same ARM-side output.                 |

## Architecture

```
cli/azd/extensions/azure.ai.agents/
  internal/cmd/init.go             ← gen azure.yaml; gen --infra path
  internal/cmd/listen.go           ← register provider via WithProvisioningProvider
  internal/project/provisioning.go ← FoundryProvisioningProvider implementation
  internal/synthesis/              ← in-memory Bicep generation from azure.yaml
    synthesizer.go                 ← top-level: ServiceConfig → template files
    project.bicep.tmpl             ← embedded template: Foundry project + deps
    agent.bicep.tmpl               ← embedded template: ACR (if container agents)
    *.tmpl                         ← other embedded templates
  internal/deploy/
    bicep_runner.go                ← ARM SDK deployment wrapper
    parameters.go                  ← parameter resolution (env vars, prompts)

cli/azd/pkg/                       ← Core changes (small)
  project/service_runtime.go       ← NEW: ServiceRuntime type (no Stack enum)
  project/service_config.go        ← +Runtime *ServiceRuntime (Uses already present)
  project/mapper_registry.go       ← +Uses, +Runtime in ServiceConfig↔proto mappers
  infra/provisioning/provider.go   ← (no change needed)

cli/azd/grpc/proto/
  models.proto                     ← +uses, +runtime (typed) on ServiceConfig message

schemas/v1.0/azure.yaml.json       ← +runtime under services.<svc> (uses already present);
                                     relax infra.provider enum → examples
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
container. `bicep` and `azure.ai.agents` are equivalent keys to the resolver.
`ParseProvider` (`cli/azd/pkg/infra/provisioning/provisioning.go:53-57`) was
relaxed in PR #7482 to accept any string.

## Explicit `infra.provider:` Declaration

The RFC ideal is service-host-driven auto-routing — the extension is picked
because `host: azure.ai.agent` is present, not because `infra.provider:` is
declared. Verified gap (`cli/azd/pkg/project/importer.go:288-358`):
`ProjectInfrastructure` never inspects `service.Host` to pick a provisioning
provider. The Aspire branch (the only service-driven precedent) and the
compose branch both hard-code Bicep.

Adding service-host auto-routing requires a net-new branch in
`ProjectInfrastructure` plus a host→extension registry. We defer that and
ship an explicit declaration:

```yaml
infra:
  provider: azure.ai.agents

services:
  foundry-project:
    host: azure.ai.project
    config: { ... }
  my-agent:
    host: azure.ai.agent
    uses: [foundry-project]
    runtime: { stack: python, version: "3.13" }
    config: { ... }
```

**Trade:** developers see an extension name in the `infra.provider:` slot
historically used for IaC engines (`bicep`, `terraform`) — a real concept
leak we accept to reuse PR #7482's plumbing as-is, with no Core changes to
`ProjectInfrastructure`. Revisit once service-host auto-routing lands.

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

The developer sees one `infra.provider: azure.ai.agents` declaration that
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
ServiceConfig (from azure.yaml)
    │
    ▼
synthesis.Synthesizer
    │   - validates azure.yaml against extension schemas
    │   - merges defaults
    │   - selects templates based on services (ACR only if container agent)
    │   - resolves ${VAR} from azd env
    ▼
[]TemplateFile (main.bicep, modules/*.bicep, main.parameters.json)
    │
    ▼
deploy.BicepRunner
    │   - resolves remaining parameters (prompts, env)
    │   - calls ARM REST: deployments.CreateOrUpdate
    │   - streams progress via grpcbroker.ProgressFunc
    │   - captures outputs
    ▼
ProvisioningDeployResult (back to azd Core via gRPC)
```

The extension does **not** delegate deployment to Core's Bicep provider — no
such delegation API exists today (`cli/azd/grpc/proto/deployment.proto`
exposes only `GetDeployment`/`GetDeploymentContext`). The extension
reimplements the deploy step using `armresources.DeploymentsClient`; a future
Core API could expose a shared Bicep-deploy path to avoid drift.

## Validation Pipeline

Synthesis runs only on a valid `azure.yaml`. Order, all before Bicep is
generated:

1. **Schema validation** — each `azure.ai.*` service's `config:` block against
   its JSON schema. Failures: `services.foundry-project.config.deployments[0].sku: required`.
2. **Service graph invariants** — exactly one `azure.ai.project` service;
   every `azure.ai.agent` `uses:` exactly one project; no cycles.
3. **Deploy-mode invariant** — each `azure.ai.agent` has exactly one of
   `runtime:` or `docker:`. Both = error. Neither = error.
4. **Env reference resolution** — every `${VAR}` in `config:` blocks must
   resolve from the azd environment.
5. **Brownfield consistency** — if `resourceId:` is set on the project, it
   must be a syntactically valid Foundry project ARM resource ID (existence
   check at deploy time).

All five run on every `provision`, `preview`, and `init --infra`.

## Brownfield: Existing Foundry Projects

Replaces today's `USE_EXISTING_AI_PROJECT` / `AZURE_AI_PROJECT_ID` env vars
(which the starter Bicep branches on) with an explicit field on the project
service:

```yaml
services:
  foundry-project:
    host: azure.ai.project
    resourceId: ${AZURE_AI_PROJECT_ID}   # presence → existing-project mode
    config:
      toolboxes: { ... }
```

When `resourceId:` is set, synthesis omits the Foundry project ARM resource;
generates references to wire `AZURE_AI_PROJECT_ENDPOINT`,
`AZURE_AI_PROJECT_ID`, `AZURE_RESOURCE_GROUP`, tenant/subscription/location;
still synthesizes ARM-backed children (e.g., additional model deployments);
and routes data-plane resources to the existing project's deploy verb. The
`useExistingAiProject` ternary collapses to a single field-presence check.

## Eject Command (`azd ai agent init --infra`)

Infra-only operation. Four contexts:

| Context                                   | Behavior                                                                                       |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Empty directory                           | Run init normally + write `./infra/` from synthesis.                                           |
| Existing Bicep-less azd agent project     | Synthesize current `azure.yaml`; write `./infra/`. Do not re-prompt; do not touch agent code. Do not modify `azure.yaml` (`infra.provider:` stays `azure.ai.agents`). |
| Existing on-disk project (`./infra/` exists) | Refuse to overwrite. Print: *"`./infra/` already exists. To regenerate from `azure.yaml`, delete the `infra/` directory and run the command again."* |
| Not an azd agent project                  | Refuse: "no `azure.ai.*` services found in `azure.yaml`; nothing to eject."                    |

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

### 1. Surface `uses` and `runtime` to extensions (RFC Core Ask #2)

`Uses` already exists on the core `ServiceConfig`
(`cli/azd/pkg/project/service_config.go:58`) and in the v1 schema under
`services.<svc>` (`schemas/v1.0/azure.yaml.json:234`). The gap is proto-only:
`models.proto`'s `ServiceConfig` message (`cli/azd/grpc/proto/models.proto:87-100`)
has no `uses` field, so extensions can't read it from typed proto.

`Runtime` is a bigger gap — it doesn't exist on `ServiceConfig` at all. It
lives only on `AppServiceProps` (`cli/azd/pkg/project/resources.go:283`,
compose side). `AppServiceRuntime` hard-restricts `Stack` to `node`/`python`
(`schemas/v1.0/azure.yaml.json:1490-1493`) — too narrow for Foundry agents,
so a new neutral `ServiceRuntime` type is added rather than reused.

Changes:

| File                                              | Change                                                                                  |
| ------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `cli/azd/pkg/project/service_runtime.go` (new)    | Define `type ServiceRuntime struct { Stack string; Version string }` — no Stack enum    |
| `cli/azd/pkg/project/service_config.go`           | Add `Runtime *ServiceRuntime \`yaml:"runtime,omitempty"\`` (Uses already present at :58) |
| `cli/azd/grpc/proto/models.proto`                 | Add `repeated string uses = 13` and `ServiceRuntime runtime = 14` to `ServiceConfig`    |
| `cli/azd/pkg/project/mapper_registry.go:102-162`  | Populate `Uses` and `Runtime` in forward + reverse `ServiceConfig`↔proto mappers        |
| `schemas/v1.0/azure.yaml.json` (services branch)  | Add typed `runtime` under `services.<svc>` (uses already at :234). Distinct from `appServiceResource.runtime` at lines 1477-1493, which stays as-is. |

Extension reads `serviceConfig.Uses` and `serviceConfig.Runtime` from typed
proto fields instead of `additional_properties`.

> **Note for #7962:** that RFC assumes `services.<svc>.uses` and
> `services.<svc>.runtime` exist. `uses` already does; this spec adds
> `runtime`.

### 2. Relax `infra.provider` enum in schemas

| File                                  | Change                                                         |
| ------------------------------------- | -------------------------------------------------------------- |
| `schemas/v1.0/azure.yaml.json:44-52`  | Change `enum: ["bicep","terraform"]` → `examples: [...]`       |
| `schemas/alpha/azure.yaml.json:44-52` | Same                                                           |

Without this, `infra.provider: azure.ai.agents` fails IDE schema validation
despite being runtime-valid.

### 3. (Optional, deferred) Auto-install for `provisioning-provider` extensions

Today: `cli/azd/cmd/auto_install.go:511-578` auto-installs extensions for
unknown `service-target-provider` host kinds. No equivalent for
`provisioning-provider`. Tracked as `#7502`.

Acceptable to defer — developers writing `infra.provider: azure.ai.agents`
have opted in explicitly. `azd ai agent init` force-installs the extension at
init time anyway. The failure mode is `git clone` + `azd up` on a fresh
machine where the README is the install instruction.

## Extension Changes Required

### Schemas

Two schemas owned by the extension (per #7962):

- `azure.ai.agent.json` — agent runtime config block (already exists; trimmed
  per #7962)
- `azure.ai.project.json` — project-scoped data-plane state (new per #7962)

Both `additionalProperties: true` for forward-compatibility with future
resources (eval datasets, vector indexes).

### Embedded templates

`cli/azd/extensions/azure.ai.agents/internal/synthesis/*.tmpl` — Go-embedded
Bicep templates, versioned with the extension. Templates are tailored: ACR
only included when at least one agent has a `docker:` block; monitoring only
when explicitly added via `azd ai agent add monitoring` (per #8049). Replaces
`Azure-Samples/azd-ai-starter-basic` Bicep entirely.

### Provider implementation

`internal/project/provisioning.go` implements
`azdext.ProvisioningProvider` (`cli/azd/pkg/azdext/provisioning_manager.go:23-36`).
Registered via `WithProvisioningProvider("azure.ai.agents", factory)` in
`internal/cmd/listen.go`.

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
| `infra.provider: azure.ai.agents` confuses developers       | Documented in extension README; removed once service-host auto-routing lands                     |
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
2. Schema branch for typed `host: azure.ai.agent` / `azure.ai.project`
   validation (per #7962) — does it land in this RFC's PRs or #7962's?
   **Proposal:** #7962 owns the schema branches, since it defines the field
   shapes. Extension validates against its own embedded schema at runtime, so
   IDE schema lag during the gap is cosmetic.

## Test Plan

- Unit: synthesizer determinism (same input → byte-equal output)
- Unit: validation pipeline error paths (all five steps)
- Unit: `ResolveNamed("azure.ai.agents")` returns extension provider
- Integration: `azd ai agent init` produces no `./infra/`
- Integration: `azd provision` succeeds with synthesized templates
- Integration: `azd ai agent init --infra` writes `./infra/`; next
  `azd provision` reads from disk (verified via extension log)
- Integration: brownfield `resourceId:` skips ARM project creation
- E2E: `init` → `provision` → `deploy` → `down` on a single-agent project
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
