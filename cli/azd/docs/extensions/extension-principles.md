# Extension Design Principles

This guide is about the upstream questions: should the thing you're building be an `azd` extension at all, and if so, what kind? The [Extension Style Guide](./extensions-style-guide.md) handles the downstream stuff -- command syntax, flag names, error handling. Read this one first.

None of what follows is enforced by tooling. It's where the team has landed so far on keeping the extension ecosystem coherent as more extensions show up. Use these as your default; deviate when you have a real reason.

> **Scope:** We're focused on the "is this an extension, and what category?" question. The platform-extension API surface, the telemetry schema, and the mechanics of project-context resolution all live in the implementation guides linked at the bottom.

---

## Table of Contents

- [Quick Checklist](#quick-checklist)
- [Identity Principles](#identity-principles)
  - [P1: Workload depth, not API breadth](#p1-workload-depth-not-api-breadth)
  - [P2: A workload fits in `azure.yaml` services; resources declare themselves](#p2-a-workload-fits-in-azureyaml-services-resources-declare-themselves)
  - [P3: Three extension categories -- Workload, Platform, and Resource](#p3-three-extension-categories----workload-platform-and-resource)
  - [P4: One canonical extension per workload and per resource type](#p4-one-canonical-extension-per-workload-and-per-resource-type)
- [Behavior Principles](#behavior-principles)
  - [P5: Declared workloads use the lifecycle; everything else stands alone](#p5-declared-workloads-use-the-lifecycle-everything-else-stands-alone)
  - [P6: Inherit `azd` context through the SDK](#p6-inherit-azd-context-through-the-sdk)
  - [P7: Dependencies flow downward through the platform layer](#p7-dependencies-flow-downward-through-the-platform-layer)
  - [P8: Telemetry as a first-class concern](#p8-telemetry-as-a-first-class-concern)
  - [P9: Versioning tracks workload maturity](#p9-versioning-tracks-workload-maturity)
  - [P10: Dev registry for exploration, official registry for commitment](#p10-dev-registry-for-exploration-official-registry-for-commitment)
  - [P11: Shared primitives live in the platform extension](#p11-shared-primitives-live-in-the-platform-extension)
  - [P12: Telemetry and conventions are a family contract](#p12-telemetry-and-conventions-are-a-family-contract)
- [Decision Rubric](#decision-rubric)
- [Related Guides](#related-guides)
- [Open Considerations](#open-considerations)

---

## Quick Checklist

Run through these before you commit to a design:

1. Are you adding real workload depth, or just wrapping an Azure API? Thin shims over a single REST call usually belong in the [Azure CLI](https://learn.microsoft.com/cli/azure/). The `azd` way is to declare resources in `azure.yaml` and let the lifecycle handle creation. A thin command can still earn its keep if it passes the [journey-continuity test](#journey-continuity-test) -- i.e., yanking it would push the user out of `azd` mid-flow on a journey the family owns.
2. Can the thing it manages be expressed as a `services:` entry in `azure.yaml`? If not, it's probably not a workload -- though it might be a *resource* inside a workload family ([P3](#p3-three-extension-categories----workload-platform-and-resource)).
3. Does it map to one of the three categories: workload (owns a `host`), platform (shared scaffolding for 2+ siblings), or resource (a typed entity with its own lifecycle inside a family)?
4. Is it *the* extension for its workload or resource type? Things are easier to navigate when there's one extension per workload, one per resource type per family, and at most one platform extension per family.
5. Can `azure.yaml` express everything a workload needs? Do data-plane and resource commands still work without a project, given an explicit `--endpoint`-style flag, so consumers and producers can both use them?
6. Does it pull `azd` context in through the gRPC services (Project, Environment, Account, Prompt, etc.) rather than re-rolling project resolution, env parsing, or prompts?
7. Does it depend only on its family's platform extension or on `azd` core -- and *never* on sibling workload or resource extensions?
8. Does it reuse shared primitives (endpoint resolution, credential factory, connection lookup) from the platform extension instead of keeping a local copy ([P11](#p11-shared-primitives-live-in-the-platform-extension))?
9. Does it emit structured telemetry that lines up with the family's error-code and funnel-event conventions ([P8](#p8-telemetry-as-a-first-class-concern), [P12](#p12-telemetry-and-conventions-are-a-family-contract))?
10. Does the version actually reflect workload maturity? `0.x` while the contract moves, `1.0` once it's committed, `2.0+` for redesigns.
11. Is it going to the registry that matches its maturity? And does it have at least one user-facing verb before you publish? Don't promote empty namespace-squatting shells, even to dev.

---

## Identity Principles

This first batch is about **identity**: is this thing an extension, and if so, which kind?

### P1: Workload depth, not API breadth

Azure CLI is wide -- every service, lightly wrapped. `azd` goes deep on a workload: apps, agents, ML, data. If a command just makes one HTTP call and prints the response, that's Azure CLI territory. If it stitches services together, encodes opinions, scaffolds projects, or plugs into a lifecycle, it belongs in `azd`.

For Azure resources in particular, the `azd` instinct is to **declare** the resource (in `azure.yaml`, or in IaC the workload extension owns) and let `azd provision` or `azd up` bring it up. Creating a Foundry Project Connection with a direct ARM REST call is an Azure CLI flavor of command. Adding a project-connection entry to `azure.yaml` so provisioning picks it up is an `azd` flavor of command. `azd` composes and ships workloads -- it's not meant to be a second door into the management API.

#### Journey-continuity test

The main exception is **journey continuity**. Take a hypothetical `azd ai project` command. Azure CLI can already create and wire up Foundry projects, but those projects are the substrate that the rest of the family (`ai.agents`, `ai.models`, `ai.evals`, `ai.finetune`) sits on. Hopping between `az` and `azd` mid-workflow breaks exactly the seam the family exists to provide.

The test:

> *If we drop this command, does the developer get kicked out of `azd` partway through a journey their workload family owns?*

If **yes**, it stays. Pick a home:

- the workload extension, if it's bound tightly to that workload's lifecycle;
- the family's **platform extension** ([P3](#p3-three-extension-categories----workload-platform-and-resource)), if it's shared on-ramp or off-ramp work;
- a **resource extension** ([P3](#p3-three-extension-categories----workload-platform-and-resource)), if it acts on a typed entity in the family that has its own lifecycle.

If **no**, it almost certainly belongs in Azure CLI.

This test gets cited from [P3](#p3-three-extension-categories----workload-platform-and-resource) and the [Decision Rubric](#decision-rubric). It's the single best tiebreaker when something looks like a thin API wrapper but feels essential to the journey.

### P2: A workload fits in `azure.yaml` services; resources declare themselves

A **workload** is something you can write down under `services:` -- a `host`, a `language`, some lifecycle hooks. If it doesn't fit there, it's probably one of these instead:

- A **resource** inside a workload family -- a typed entity (skill, routine, connection, toolbox, dataset, fine-tune, eval suite, model deployment) with its own lifecycle. Today these are declared and versioned through their own CRUD surface, owned by a [resource extension](#p3-three-extension-categories----workload-platform-and-resource). A resource extension *can* later graduate to a declarative form (entries in `azure.yaml`, or a future family-scoped manifest) once its schema and lifecycle settle, at which point the imperative verbs become a thin shell over the lifecycle.
- **Supporting infrastructure** (storage, networking, key vault) -- better in IaC.
- **A cross-cutting concern** (auth, telemetry) -- belongs in a platform extension or in `azd` core.
- **Something `azd` shouldn't own at all** -- a standalone tool, or a candidate for a different system entirely.

"I want to deploy a key vault" doesn't make a key vault a workload. Key vaults exist to back workloads.

### P3: Three extension categories -- Workload, Platform, and Resource

| Category | Owns | Examples (real or proposed) | Primary test |
|---|---|---|---|
| **Workload extension** | A service `host` type in `azure.yaml`. End-to-end DX for that workload. | `azure.ai.agents` (host: `azure.ai.agent`) | Does it add a new workload `host`, or significantly enrich an existing one? |
| **Platform extension** | Cross-cutting scaffolding shared by 2+ workload or resource extensions in the same family. | `azure.ai` (proposed: shared project endpoint cascade, credential factory, env-var contract, prompt patterns, telemetry conventions) | Does it serve at least two sibling extensions today? |
| **Resource extension** | A typed entity inside a workload family, with its own lifecycle and user-facing verbs. | `azure.ai.connections`, `azure.ai.skills`, `azure.ai.routines`, `azure.ai.toolboxes`, `azure.ai.models` | Does it own first-class CRUD verbs for a noun in the family's mental model, and does it pass the resource-extension gates below? |

#### Resource-extension admission gates

Resource extensions are an easier sell than workloads, which makes it tempting to spin one up for anything Foundry-shaped. Don't. A resource extension has to clear **all four** gates:

1. **Family membership.** It lives in a recognized workload family (e.g. `azure.ai.*`) that's anchored by at least one workload extension. Orphan resource extensions don't have a home to begin with.
2. **Journey-critical.** It passes the [journey-continuity test](#journey-continuity-test) -- without it, the user gets bounced out of `azd` mid-flow.
3. **Distinct entity type.** It's a real noun in the family's mental model (skill, routine, connection, toolbox, dataset, eval suite), not a flag or subcommand bolted onto something that already exists.
4. **Uses the platform layer.** It picks up endpoint resolution, the credential factory, prompts, and telemetry conventions from the family's platform extension instead of growing its own. This is what stops the resource category from devolving into "ship whatever." See [P11](#p11-shared-primitives-live-in-the-platform-extension).

Fail any gate and the candidate either folds into a parent workload extension, moves to Azure CLI, or gets cut.

#### When none of the three categories fit

- A "platform" extension with exactly one consumer should usually fold back into that consumer.
- A "resource" extension whose verbs are really just convenience flags on an existing entity belongs inside the parent workload or resource extension.
- Universal helpers go into `azd` core.
- API shims with no workload or family context go into Azure CLI.
- Standalone tools ship standalone.

### P4: One canonical extension per workload and per resource type

If two extensions both claim the same workload ("agents") or the same resource type ("connections"), pick one. Coexistence is almost never the right answer. If a workload extension grows scaffolding that a sibling would also use, lift it into the platform extension. If a platform extension only ever has one consumer, inline it -- the abstraction isn't paying for itself.

Per family, the default shape is:

- **One workload extension** per workload `host` (e.g. `azure.ai.agents`).
- **One resource extension** per resource type in the family (e.g. `azure.ai.connections`, `azure.ai.skills`).
- **At most one platform extension** per family (e.g. `azure.ai`).

A classic smell: the same resource type showing up in several workload extensions (`azd ai agent connection ...` *and* `azd ai toolbox connection ...` *and* a standalone `azure.ai.connections`). When that happens, lift it into the canonical resource extension (or into the platform extension if it's really scaffolding rather than CRUD), and have the workload extensions delegate or drop their copies.

These are defaults. Deviate when it makes sense, but be explicit about why.

---

## Behavior Principles

The second batch is about **behavior**: how an extension should plug into `azd`, into sibling extensions, and into the rest of the ecosystem.

### P5: Declared workloads use the lifecycle; everything else stands alone

`azure.yaml` is the workload's declaration -- *everything this thing needs* -- where "thing" is an agent host, a model endpoint, an eval suite. For workload extensions that covers:

- **Control-plane resources** (the Foundry project, hosted compute, storage) in IaC, provisioned by `azd provision`.
- **Data-plane configuration** (the model deployment to bind, the agent definition to push, the index to seed) applied via lifecycle hooks (`postprovision`, `predeploy`, `deploy`).

This keeps the line from [P1](#p1-workload-depth-not-api-breadth) intact: raw REST creation inside a command leans Azure CLI; declaring it in `azure.yaml` and letting the lifecycle do the work is the `azd` way.

**Data-plane commands and resource-extension commands are different beasts.** Anything that talks to a deployed workload (invoking an agent, querying a model, listing runs) or operates on a typed resource (creating a connection, bumping a toolbox version) **MUST** work without a project. These commands take an explicit endpoint or read process env vars. They feel more like `curl` than like `azd up`.

#### The producer/consumer pattern

A good data-plane or resource command has two audiences:

- The **producer** declared and provisioned the workload (or created the parent resource) and has the full project context handy.
- The **consumer** just wants to hit a deployed endpoint or poke at an existing resource, and may not have a project at all.

The same command serves both: it uses project state when it's there (resolving an endpoint from `.azure/<env>/`, picking up keys, defaulting to the right deployment), and it accepts an explicit flag like `--agent-endpoint` or `--project-endpoint` to skip project context. `azd ai agent invoke` is the canonical example. Note that the same person is often both -- deploying their own agent in the morning and poking at a teammate's prod endpoint after lunch.

This isn't optional for resource extensions. Brownfield and CI are first-class scenarios. The resolution cascade (explicit flag -> active azd env -> azd user config -> host env var -> structured error) **SHOULD** come from the family's platform extension so every sibling does it the same way. See [P11](#p11-shared-primitives-live-in-the-platform-extension).

Quick test: can a developer talk to a deployed thing -- or manage a Foundry resource -- with no project, no `azure.yaml`, and no `.azure/` folder? If not, the command is too tangled up in project context.

### P6: Inherit `azd` context through the SDK

If `azd` already does the thing, use it. Don't rebuild it.

- **Project context** -- the Project gRPC service.
- **Environment** -- the Environment service. Don't hand-roll a `.env` parser.
- **Account info** -- the Account service. Don't reinvent subscription resolution.
- **Prompts** -- the Prompt service. No bespoke TUIs.
- **Deployment, Compose, Workflow, Container, AI Model, Copilot** -- the gRPC surface is the contract.

When you find yourself reimplementing something `azd` provides, that's a useful signal. Either the existing surface needs upstream work (a fine conversation to have), or what you're building probably doesn't need to be an extension at all. An extension that never touches the gRPC surface is worth a second look -- there's likely a simpler home for it.

The full list of gRPC services is in the [Extension Framework](./extension-framework.md#grpc-services) guide and [Extension Framework Services](./extension-framework-services.md).

### P7: Dependencies flow downward through the platform layer

```
workload extension  ----\
                         \
resource extension  ------>  platform extension  -->  azd core
                         /
another sibling     ----/
```

Workload-to-workload, resource-to-resource, and workload-to-resource imports aren't allowed. Shared state and scaffolding go in the platform extension, or in `azd` core if they're truly universal. Keeping the dependency graph as a forest rooted at the platform extension forces the "should this be in the platform layer?" question out into the open, instead of letting it sneak in via a sibling import.

The rules:

- A workload extension MAY import the family's platform extension and `azd` core. It MUST NOT import a sibling workload or resource extension.
- A resource extension MAY import the family's platform extension and `azd` core. It MUST NOT import a sibling resource or workload extension.
- A platform extension MAY import `azd` core. It MUST NOT import any workload or resource extension (otherwise it stops being a leaf).

When two siblings want the same primitive, the answer is always the same: lift it into the platform extension. See [P11](#p11-shared-primitives-live-in-the-platform-extension).

### P8: Telemetry as a first-class concern

If an extension doesn't surface its lifecycle events and errors into `azd`'s telemetry pipeline, it's effectively invisible to product decisions. Invisible extensions are harder to prioritize, harder to fix, and harder to plan around.

What every extension should do:

- **Classify errors with structure.** Use `azdext.ServiceError` and `azdext.LocalError` instead of opaque strings.
- **Emit funnel events** for the workload's key lifecycle stages.
- **Attribute correctly** -- user errors, service errors, and unknown failures should be distinguishable.

Implementation specifics -- error types, telemetry patterns, chain precedence, recommended layering -- live in [Extension Style Guide: Error Handling](./extensions-style-guide.md#error-handling-in-extensions).

### P9: Versioning tracks workload maturity

The version number says something about the workload's contract -- it's not a log of internal refactors:

| Version range | Meaning |
|---|---|
| `0.x` | Contract still moving. Breaking changes OK with documented migration paths. |
| `1.0` | Contract committed. Supported for a stated horizon. Breaks require a major bump. |
| `2.0+` | A workload-level redesign. Announce it loudly, ideally with a migration tool. |

When a workload retires, its extension retires too. Don't keep dead workloads on life support just for backward-compatibility appearances.

For the broader semver guidance that applies across all extensions, see [Extension Resolution and Versioning](./extension-resolution-and-versioning.md).

### P10: Dev registry for exploration, official registry for commitment

The two registries do different jobs:

| Registry | For | Guarantees |
|---|---|---|
| Development | Workloads still being explored | Unsigned binaries, no semver, no support horizon. |
| Official | Workloads the team is committing to | Signed, semver-guaranteed, documented support horizon, telemetry-instrumented per [P8](#p8-telemetry-as-a-first-class-concern). |

Promotion is a product call -- is this workload worth a long-term bet? -- not just a code-quality check. A polished extension for an uncommitted workload can sit in dev. A rough extension for a committed workload gets polished and promoted. When the workload retires, so does the extension.

**No empty shells.** Don't publish to any registry (dev included) until the extension has at least one user-facing verb beyond `version`, `metadata`, and `context`. Squatting on a namespace with an empty shell has real costs: the entry shows up in `azd ext list`, the registry has to be maintained, and the "published but empty" signal misleads anyone browsing the family.

### P11: Shared primitives live in the platform extension

Endpoint resolution, credential factories, project-context cascades, connection lookups, prompt patterns -- anything more than one extension in a family would want -- MUST live in the family's platform extension. Duplicating these across workload and resource extensions is debt, not detail.

Things to watch for in code review:

- Two extensions in the same family ship nearly identical `*EndpointResolver`, `*CredentialFactory`, or `connectionLookup` types.
- A workload extension ships an `internal/foundry/` (or similar) package with a comment along the lines of "copied from a sibling, will lift later." The lift is the next required PR, not a someday-maybe.
- A resource extension hand-rolls the cascade `flag -> azd env -> user config -> host env var` instead of calling a shared resolver.

The fix is always the same: move the primitive into the platform extension, have the siblings import it, delete the local copies. The dependency direction in [P7](#p7-dependencies-flow-downward-through-the-platform-layer) is what makes this safe to do.

If the family doesn't have a platform extension yet and you need to share a primitive, that's your cue to create one. The "2+ sibling consumers" threshold from [P3](#p3-three-extension-categories----workload-platform-and-resource) is satisfied the moment a primitive gets duplicated.

### P12: Telemetry and conventions are a family contract

[P8](#p8-telemetry-as-a-first-class-concern) makes each extension responsible for structured errors and funnel events. P12 goes further: those errors and events MUST follow conventions agreed at the **family** level, not picked independently per extension.

Each family SHOULD publish a short conventions doc, owned by its platform extension, covering:

- **Error code namespacing.** Stable, lowercase `snake_case` codes with a consistent prefix or scope (`ai_project_endpoint_missing`, `ai_connection_invalid_target`, etc.).
- **Error categories and attribution.** Which `exterrors.*` category fits which failure class, and how to attribute user vs. service vs. unknown.
- **Funnel event names.** Shared verbs (`init`, `provision`, `deploy`, `invoke`, `create`, `update`, `delete`) emitted under consistent names so dashboards can roll up across the family.
- **Reserved flag names and cascades.** `--project-endpoint`, env-var fallbacks, no-prompt behavior, output formats -- one source of truth.

What it costs to skip this: one sibling ships bare `fmt.Errorf` strings while others ship structured errors, dashboards undercount the family, and product calls get made on incomplete data. The family ends up looking smaller and rougher than it actually is.

When in doubt, the conventions doc lives in the platform extension. Workload and resource extensions reference it; they don't roll their own.

---

## Decision Rubric

Quick guidance for the design questions that come up most often:

| Proposed feature | Belongs as | Why |
|---|---|---|
| Wrap an Azure Storage REST API one-to-one | Azure CLI | Breadth, not depth. No workload. |
| Create a Foundry Project Connection via ARM REST with no family context | Azure CLI | Direct resource creation ([P1](#p1-workload-depth-not-api-breadth)). |
| Add a Foundry Project Connection entry to `azure.yaml` so `azd up` provisions it | Workload or platform extension | Creation runs through the lifecycle ([P1](#p1-workload-depth-not-api-breadth), [P5](#p5-declared-workloads-use-the-lifecycle-everything-else-stands-alone)). |
| New `host` kind: Foundry hosted agent | Workload extension | New service type in `azure.yaml`. |
| Shared `project connect` across AI extensions | Platform extension (`azure.ai`) | Serves 2+ sibling extensions. |
| `project connect` shared by only one extension | Inside that workload extension | Does not yet earn a platform extension. |
| CRUD for a Foundry sub-resource (skill, routine, connection, toolbox) that is part of the agents journey, with no clean declarative path today | Resource extension | Passes the four resource-extension gates ([P3](#p3-three-extension-categories----workload-platform-and-resource)). |
| CRUD for a Foundry sub-resource where Bicep exists and is the natural authoring path | Platform extension entry that wires it into `azure.yaml` provisioning | Declarative works and is preferred ([P1](#p1-workload-depth-not-api-breadth), [P2](#p2-a-workload-fits-in-azureyaml-services-resources-declare-themselves)). |
| `azd ai connection` shipped *both* in `ai.agents`, `ai.toolboxes`, and as a standalone extension | Lift to a single canonical resource extension (`azure.ai.connections`) | One canonical extension per resource type ([P4](#p4-one-canonical-extension-per-workload-and-per-resource-type)). |
| Standalone YAML formatter for `agent.yaml` | Standalone tool | No `azd` context, no workload. |
| Live chat REPL or `invoke` for a deployed agent | Data-plane command in `ai.agents` | Serves producer (project state) and consumer (`--endpoint`, no project) ([P5](#p5-declared-workloads-use-the-lifecycle-everything-else-stands-alone)). |
| Universal `azd encrypt-secret` for all environments | `azd` core | Not workload-specific. |
| Brownfield connect to an existing Foundry project | Platform extension data-plane command | Cross-workload data-plane on-ramp. |
| Imperative `azd ai project create` when the family has no project yet | Platform extension command | On-ramp creation passes the [journey-continuity test](#journey-continuity-test); imperative creation is acceptable inside a platform extension (not a workload). |
| Endpoint resolver, credential factory, or connection lookup duplicated across siblings | Lift to the family's platform extension | [P11](#p11-shared-primitives-live-in-the-platform-extension). |
| Wrap one Functions provisioning quirk | `azd` core or apps team | Not a new workload, not platform-shared. |
| Per-workload IaC generator | Workload extension responsibility | Lifecycle integration for that workload. |
| Cross-workload billing or cost dashboard | Standalone tool, or Azure CLI extension | Does not fit `azd`'s lifecycle-oriented value proposition. |

---

## Related Guides

| Guide | Description |
|-------|-------------|
| [Extension Style Guide](./extensions-style-guide.md) | Command syntax, reserved flags, error handling, and discoverability conventions. |
| [Extension Framework](./extension-framework.md) | Lifecycle, capabilities, and the full gRPC service surface. |
| [Extension Framework Services](./extension-framework-services.md) | Custom language and framework support. |
| [Extension Resolution and Versioning](./extension-resolution-and-versioning.md) | How `azd` resolves extensions, semver guidance, and the official versioning model. |
| [Extension SDK Reference](./extension-sdk-reference.md) | API reference for `azdext` SDK helpers. |

---

*Tactical stuff -- command structure, flag conventions, error handling, telemetry integration -- is in the [Extension Style Guide](./extensions-style-guide.md).*
