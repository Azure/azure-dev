# Extension Design Principles

This document describes design principles for `azd` extensions: when to build one, what shape it should take, and how it should behave alongside other extensions and `azd` core. Where the [Extension Style Guide](./extensions-style-guide.md) covers command syntax, flag conventions, and error handling, this guide covers earlier decisions -- whether a feature belongs as an extension at all, and which category it fits into.

These principles are guidance, not enforcement. They reflect the team's current thinking on how the extension ecosystem stays coherent as it grows. Treat them as defaults to apply unless you have a clear reason to deviate.

> **Scope:** This guide focuses on whether a feature should be an extension and what category it fits. It does not specify the exact platform-extension API surface, the telemetry schema, or project-context resolution mechanics -- those are covered in the implementation guides linked at the end.

---

## Table of Contents

- [Quick Checklist](#quick-checklist)
- [Identity Principles](#identity-principles)
  - [P1: Workload depth, not API breadth](#p1-workload-depth-not-api-breadth)
  - [P2: A workload fits in `azure.yaml` services](#p2-a-workload-fits-in-azureyaml-services)
  - [P3: Two extension categories -- Workload and Platform](#p3-two-extension-categories----workload-and-platform)
  - [P4: One canonical extension per workload](#p4-one-canonical-extension-per-workload)
- [Behavior Principles](#behavior-principles)
  - [P5: `azure.yaml` declares the workload; data-plane commands stand alone](#p5-azureyaml-declares-the-workload-data-plane-commands-stand-alone)
  - [P6: Inherit `azd` context through the SDK](#p6-inherit-azd-context-through-the-sdk)
  - [P7: Workload extensions depend on platform extensions or core](#p7-workload-extensions-depend-on-platform-extensions-or-core)
  - [P8: Telemetry as a first-class concern](#p8-telemetry-as-a-first-class-concern)
  - [P9: Versioning tracks workload maturity](#p9-versioning-tracks-workload-maturity)
  - [P10: Dev registry for exploration, official registry for commitment](#p10-dev-registry-for-exploration-official-registry-for-commitment)
- [Decision Rubric](#decision-rubric)
- [Related Guides](#related-guides)
- [Open Considerations](#open-considerations)

---

## Quick Checklist

Use the following questions as a gut check when designing a new extension or feature:

1. Does it add workload depth, or is it a thin wrapper over an Azure API? Single-call API shims and direct resource creation are usually a better fit for the [Azure CLI](https://learn.microsoft.com/cli/azure/). The `azd`-native approach is to declare resources in `azure.yaml` and let the lifecycle bring them up. A thin command may still earn its place if removing it would force the developer out of `azd` partway through a journey the workload family owns.
2. Can what it owns be expressed as an entry under `services:` in `azure.yaml`? If not, it likely isn't a workload.
3. Does it fit one of the two recognized categories -- workload extension (owns a `host` type) or platform extension (cross-cutting scaffolding shared by two or more workload extensions in the same family)?
4. Is it the canonical extension for its workload? An ecosystem with one extension per workload, and at most one platform extension per family, is easier to navigate and maintain.
5. Does `azure.yaml` declare everything the workload needs, both control plane and data plane? Do data-plane commands work without a project -- accepting an explicit `--endpoint`-style flag -- so they can serve consumers as well as producers?
6. Does it inherit `azd` context through the gRPC services (Project, Environment, Account, Prompt, ...) instead of reinventing project resolution, env parsing, or prompts?
7. Does it depend only on platform extensions or `azd` core, never on sibling workload extensions?
8. Does it emit structured telemetry -- typed errors, funnel events, and attribution that distinguishes user from service from unknown failures?
9. Does its version reflect workload maturity? `0.x` for evolving contracts, `1.0` for committed contracts, `2.0+` for redesigns.
10. Is it being shipped through the registry that matches its maturity? Dev registry for exploration, official registry for committed workloads.

---

## Identity Principles

The first set of principles addresses **identity**: deciding whether something belongs as an extension, and which category it fits.

### P1: Workload depth, not API breadth

The Azure CLI provides breadth -- every Azure service, thinly wrapped. `azd` provides depth for a workload -- apps, agents, ML, data. A command that issues a single HTTP call and returns the response is generally a better fit for the Azure CLI. A command that coordinates multiple services, packages opinions, scaffolds projects, or hooks into a lifecycle is generally a better fit for `azd`.

For Azure resources specifically, the `azd`-native approach is to **declare** the resource in `azure.yaml` (or in IaC the workload extension manages) and let `azd provision` or `azd up` create it. A command that creates a Foundry Project Connection by calling ARM REST is closer to an Azure CLI command. A command that adds a project-connection entry to `azure.yaml` so it is wired up at provision time is closer to `azd`. `azd` composes and ships workloads; it is not intended as a second front door for the management API.

The carve-out is **journey continuity**. Consider a hypothetical `azd ai project` command: the Azure CLI can already create and connect Foundry projects, but those projects are the foundation that other extensions (`ai.agents`, `ai.models`, `ai.evals`, `ai.finetune`) build on. Bouncing between `az` and `azd` mid-workflow breaks the seam the family is meant to provide. A useful test: would removing this command force the developer out of `azd` partway through a journey their workload extensions own? If yes, keep it -- usually as part of a platform extension (see [P3](#p3-two-extension-categories----workload-and-platform)).

### P2: A workload fits in `azure.yaml` services

A workload is something expressible under `services:` -- a `host`, a `language`, lifecycle hooks. If it cannot be expressed there, it is probably one of three other things:

- **Supporting infrastructure** (storage, networking, key vault) -- better expressed in IaC.
- **A cross-cutting concern** (auth, telemetry) -- better placed in a platform extension or `azd` core.
- **Something outside the scope of `azd`** -- a standalone tool, or a candidate for a different system.

"I want to deploy a key vault" does not by itself make a key vault a workload. Key vaults exist to support workloads.

### P3: Two extension categories -- Workload and Platform

| Category | Owns | Examples (real or proposed) | Test |
|---|---|---|---|
| **Workload extension** | A service `host` type in `azure.yaml`. End-to-end DX for that workload. | `azure.ai.agents`, `azure.ai.models`, `azure.ai.finetune` | Does it add a new workload type, or significantly enrich an existing one? |
| **Platform extension** | Cross-cutting scaffolding shared by two or more workload extensions in the same family. | `azure.ai` (proposed base extension hosting `project connect`, env-var contract, shared flags) | Does it serve at least two workload extensions today? |

When something does not fit either category, it usually belongs elsewhere:

- A "platform" extension serving only one workload is a candidate for folding back into that workload extension.
- Universal helpers belong in `azd` core.
- API shims with no workload context belong in the Azure CLI.
- Standalone tools ship standalone.

### P4: One canonical extension per workload

When two extensions both claim the same workload (for example, "agents"), consolidation is usually preferable to coexistence. When a workload extension grows scaffolding another would also want, lift it into the platform extension. When a platform extension only ever has one consumer, consider inlining it back -- the abstraction is not earning its keep.

One workload, one extension. At most one platform extension per family. These are defaults; deviations should be deliberate and documented.

---

## Behavior Principles

The second set of principles addresses **behavior**: how an extension should integrate with `azd`, sibling extensions, and the rest of the ecosystem.

### P5: `azure.yaml` declares the workload; data-plane commands stand alone

`azure.yaml` is the workload's declaration -- *everything I need for this thing*, where the *thing* is an agent, a model endpoint, an eval suite. That includes:

- **Control-plane resources** (the Foundry project, hosted compute, storage) expressed in IaC and provisioned by `azd provision`.
- **Data-plane configuration** (the model deployment to bind, the agent definition to push, the index to seed) applied by the workload extension through lifecycle hooks (`postprovision`, `predeploy`, `deploy`).

This preserves the boundary established in [P1](#p1-workload-depth-not-api-breadth): direct REST creation inside a command is closer to the Azure CLI; declaring the resource in `azure.yaml` and letting the lifecycle bring it up is the `azd`-native approach.

**Data-plane commands** are different. Anything that talks to a deployed workload -- invoking an agent, querying a model, listing runs -- should work without a project. These commands take an explicit endpoint or read process environment variables, and behave more like `curl` than like `azd up`.

#### The producer/consumer pattern

A well-shaped data-plane command serves two audiences:

- The **producer** declared and provisioned the workload and has full project context.
- The **consumer** wants to hit a deployed endpoint and may have no project at all.

The same command should serve both: leaning on project state when present (resolving an agent endpoint from `.azure/<env>/`, picking up keys, defaulting the right deployment), and accepting an explicit flag such as `--agent-endpoint` to bypass project context. `azd ai agent invoke` is a representative example. The same person is often both at different moments -- deploying their own agent in the morning, smoke-testing a teammate's prod endpoint in the afternoon.

A useful test: can a developer talk to a deployed thing with no project, no `azure.yaml`, and no `.azure/` folder? If not, the command may be over-coupled to project context.

### P6: Inherit `azd` context through the SDK

When `azd` provides a capability, the extension should generally use it rather than reimplementing it.

- **Project context** comes from the Project gRPC service.
- **Environment** comes from the Environment service -- no homegrown `.env` parser.
- **Account information** comes from the Account service -- no reimplemented subscription resolution.
- **Prompt UX** comes from the Prompt service -- no custom TUI.
- **Deployment, Compose, Workflow, Container, AI Model, Copilot** services -- the gRPC surface is the contract.

Reimplementing what `azd` already provides is a useful signal: either `azd`'s surface needs upstream work (and that's a fair conversation to have), or the extension may be doing something that does not require an extension at all. An extension that touches none of the gRPC surface is worth a second look -- there may be a simpler home for it.

For a complete reference of the gRPC services available, see the [Extension Framework](./extension-framework.md#grpc-services) guide and [Extension Framework Services](./extension-framework-services.md).

### P7: Workload extensions depend on platform extensions or core

```
workload extension --(depends on)--> platform extension --(depends on)--> azd core
                                            ^
                                            |
                  another workload extension /
```

Workload-to-workload dependencies are not supported. Shared state and shared scaffolding belong in the platform extension, or in `azd` core when truly universal. Keeping the dependency graph as a forest surfaces the question "does this belong in the platform layer?" explicitly, instead of letting it slip in through a sibling import.

### P8: Telemetry as a first-class concern

An extension that does not surface lifecycle events and errors to `azd`'s telemetry pipeline is effectively invisible to product decision-making, and invisible extensions are harder to prioritize, fix, or roadmap.

Each extension is encouraged to provide:

- **Structured error classification.** Use `azdext.ServiceError` and `azdext.LocalError` instead of opaque error strings.
- **Funnel events** for the workload's key lifecycle stages.
- **Attribution** that separates user errors from service errors from unknown failures.

For implementation details -- error types, telemetry code patterns, error chain precedence, and recommended layering -- see the [Extension Style Guide: Error Handling](./extensions-style-guide.md#error-handling-in-extensions) section.

### P9: Versioning tracks workload maturity

The version is a statement about the workload contract, not a changelog of internal refactors:

| Version range | Meaning |
|---|---|
| `0.x` | Contract evolving. Breaking changes allowed with documented migration paths. |
| `1.0` | Contract committed. Supported for a stated horizon. Breaking changes require a new major. |
| `2.0+` | Workload-defining redesign. Telegraphed loudly, ideally with a migration tool. |

Workload deprecation is extension deprecation. Retired workloads should not keep their extensions on indefinite life support purely for backward-compat optics.

For the broader semver guidance applied across all extensions, see [Extension Resolution and Versioning](./extension-resolution-and-versioning.md).

### P10: Dev registry for exploration, official registry for commitment

The two registries serve different purposes:

| Registry | Intended for | Guarantees |
|---|---|---|
| Development | Workloads still being explored | Unsigned binaries, no semver guarantees, no support horizon. |
| Official | Workloads the team is committing to | Signed, semver-guaranteed, documented support horizon, telemetry-instrumented per [P8](#p8-telemetry-as-a-first-class-concern). |

Promotion is a product decision -- does this workload deserve a long-term bet? -- rather than purely a code-quality gate. A polished extension for an uncommitted workload may stay in dev. A rough extension for a committed workload gets polished and promoted. When a workload retires, the extension retires with it.

---

## Decision Rubric

The following table provides quick guidance for common design questions:

| Proposed feature | Belongs as | Why |
|---|---|---|
| Wrap an Azure Storage REST API one-to-one | Azure CLI | Breadth, not depth. No workload. |
| Create a Foundry Project Connection via ARM REST | Azure CLI | Direct resource creation ([P1](#p1-workload-depth-not-api-breadth)). |
| Add a Foundry Project Connection entry to `azure.yaml` so `azd up` provisions it | Workload or platform extension | Creation runs through the lifecycle ([P1](#p1-workload-depth-not-api-breadth), [P5](#p5-azureyaml-declares-the-workload-data-plane-commands-stand-alone)). |
| New `host` kind: Foundry hosted agent | Workload extension | New service type in `azure.yaml`. |
| Shared `project connect` across AI extensions | Platform extension (`azure.ai`) | Serves two or more workload extensions. |
| `project connect` shared by only one extension | Inside that workload extension | Does not yet earn a platform extension. |
| Standalone YAML formatter for `agent.yaml` | Standalone tool | No `azd` context, no workload. |
| Live chat REPL or `invoke` for a deployed agent | Data-plane command in `ai.agents` | Serves producer (project state) and consumer (`--endpoint`, no project) ([P5](#p5-azureyaml-declares-the-workload-data-plane-commands-stand-alone)). |
| Universal `azd encrypt-secret` for all environments | `azd` core | Not workload-specific. |
| Brownfield connect to an existing Foundry project | Platform extension data-plane command | Cross-workload data-plane. |
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

## Open Considerations

The following questions are still being worked through. They are documented here so authors and reviewers can track them and contribute as the answers firm up.

- **Audit of existing extensions.** Where does each current `azd` extension (including `azure.ai.agents`) land against P1-P10, and what changes -- if any -- would bring it into alignment?
- **Promotion process.** What is the formal review for moving an extension from the development registry to the official registry, and who approves it?
- **Deprecation process.** When a workload retires, who calls it and what is the user-facing communication?
- **Azure CLI overlap.** Are there extensions today that would be a better fit as Azure CLI commands, and what would the off-ramp look like?
- **Resource-creation carve-outs.** Are there resources `azd` should create directly -- for example, a Foundry project itself, before any `azure.yaml` exists -- where forcing the user into `az` would break the journey? If so, what is the criterion, and does it map to the [P1](#p1-workload-depth-not-api-breadth) journey-continuity exception?
- **Producer/consumer flag convention.** Should the `--endpoint`-style escape hatch be a shared platform-extension contract -- consistent flag name, env-var fallback, auth resolution -- so consumers see the same pattern across `agent`, `model`, `eval`?
- **Workload taxonomy.** Is there a canonical list of workload types `azd` recognizes, or is it open-ended and extension-defined?
- **Platform extension threshold.** [P3](#p3-two-extension-categories----workload-and-platform) suggests two or more workload extensions as the threshold for justifying a platform extension. Is that the right bar, or should it be three or more?
- **Core vs. platform extension.** What pushes a capability from a platform extension up into `azd` core, and is there an explicit promotion path?

---

*For tactical implementation guidance -- command structure, flag conventions, error handling, and telemetry integration -- see the [Extension Style Guide](./extensions-style-guide.md).*
