# Guiding design principles for `azd`

## Overview

Start with the workflow people need to complete. Consider an extension before adding functionality to core, and build on familiar `azd` commands. Keep shared orchestration in core and service-specific behavior in extensions where the supported contracts allow it.

This is the authoritative home for extension-versus-core design decisions. Use the linked implementation and contract references for API details rather than treating this page as a second API specification.

## Choose between core and an extension

Use this decision tree to shape a proposal, not as an automatic approval gate. [Issue #6244](https://github.com/Azure/azure-dev/issues/6244) tracks the extension-first direction and engineering review; this document does not establish that review has happened. Maintainers confirm the boundary and any exceptions for a proposed change.

```text
Describe the user workflow and check existing capabilities.
|
+-- Can existing configuration, templates, or hooks meet the need?
|   +-- Yes: reuse them; no new extension or core feature is needed.
|   +-- No: consider an extension.
|
+-- Must existing host-owned behavior change (selection/auth/dispatch)?
|   +-- Yes: fix the core boundary; keep domain logic in extensions.
|   +-- No: evaluate the supported extension contracts.
|
+-- Can supported extension contracts implement the workflow?
|   +-- Yes: prefer an extension; reuse lifecycle events or providers.
|   +-- No: identify the specific host API or orchestration gap.
|
+-- Can a reusable host capability close that gap?
|   +-- Yes: propose a small core API/extension point plus an extension.
|   +-- No: propose a justified core exception with maintainers.
|
+-- Record ownership, compatibility, and tradeoffs in the design/PR.
```

Being useful to many users does not by itself require a feature to live in core. Conversely, an extension should not copy shared environment selection, authentication, or deployment orchestration just to avoid a necessary host change. Compare installation and update costs, independent release needs, core binary dependencies, and long-term ownership before choosing.

### Reuse the extension host

The [extension framework](../extensions/extension-framework.md#capabilities) provides lifecycle events, framework service providers, service target providers, and provisioning providers. In Go, use the SDK's `ExtensionHost` to register the relevant handlers and providers and manage their connection to azd. Reuse host services for project/environment access, accounts, and prompts instead of maintaining parallel state or prompt flows.

Prefer these integrations when extending `init`, `provision`, `package`, or `deploy`. Custom commands are appropriate for distinct domain operations that those workflows do not express; they are not a reason to duplicate the core command tree. Follow the [extensions style guide](../extensions/extensions-style-guide.md) for their user experience.

### Design extension points and public APIs

When designing a shared core feature, identify the operations an extension would need to participate in it. Keep orchestration separate from service-specific implementations, and check the existing [host services](../extensions/extension-framework.md#grpc-services) before proposing a new API.

- **Expose a bounded capability.** Prefer a reusable operation with typed inputs and results over exporting internal objects or adding a service-specific branch to core. An exported Go symbol alone is not a versioned cross-process extension contract; use the supported SDK and gRPC boundary.
- **Make composition explicit.** Define which provider or handler owns each phase, which configuration and artifacts it consumes and produces, and what happens when another extension participates. Specify ordering requirements, cancellation, errors, and state ownership; do not rely on undocumented handler ordering or assume multiple RPCs form a transaction.
- **Keep optional capabilities optional.** Define discovery and unsupported behavior before adding an extension point. For example, deployment preview is an opt-in service-target capability, not a requirement that every existing provider implement it. A required capability that is unavailable needs an actionable error, not silent success.
- **Plan the contract with consumers.** Describe an initial consumer and other plausible uses, the minimum host/SDK versions, compatibility tests, and an owner. Use a preview contract when the shape needs validation rather than promising stable behavior prematurely.

### Examples and tradeoffs

For example, a language extension restores dependencies, builds the app, and returns a package during `azd deploy`. A compatible service-target extension prepares that package for its target, publishes it, and deploys it. Core coordinates the workflow and passes results between providers. The authors agree on the package format and test the handoff together.

| Need | Preferred boundary and reason | Tradeoff or exception |
|---|---|---|
| Provision Microsoft Foundry resources | The [Foundry Projects extension](../../extensions/azure.ai.projects/README.md#azureyaml-ownership) owns `microsoft.foundry` provisioning and `host: azure.ai.project` services, keeping domain configuration outside core while using `azd provision` and `azd deploy`. | Users install an extension and need a compatible host; service-specific dependencies can evolve independently. |
| Add support for a language/build system | Use a [framework service provider](../extensions/extension-framework-services.md) for restore, build, and package operations rather than a second deployment pipeline. | First check whether existing providers or project hooks suffice; a new provider must work with the selected service target. |
| Add deployment preview across targets | Core coordinates `azd deploy --preview`; extension targets opt into the [stable preview contract](../extensions/extension-framework.md#deployment-preview). | This is a shared core capability plus target-owned implementations, not a reason to move every target into core. Unsupported targets are reported and skipped. |
| Fix environment selection shared by workflows | Core owns `azd env select` and the selected environment used by built-in commands and extensions. | A core fix preserves one source of truth; an extension-specific environment selector would split state and behavior. |
| Fix host authentication or extension dispatch | Correct the core host boundary used before or during extension invocation. | An extension cannot reliably repair the host that launches or authenticates it. Keep unrelated domain logic outside the fix. |

A missing API is a design gap to discuss, not automatic permission to put the entire integration in core. A core exception should explain why existing extension points or a small reusable host addition cannot meet the requirement safely, and identify the compatibility and maintenance costs. Core changes follow the core release schedule and affect all CLI users.

### API ownership and compatibility

Core maintainers own the host APIs and shared workflow behavior. Extension authors own their domain logic, commands, configuration, release versions, and declared host requirements. Review core changes for their effect on already-published extension binaries as well as clients built with the new SDK.

Include configuration, defaults, lifecycle timing, and shared-state behavior in that review. Core changes can break an extension even when the protobuf contract stays the same. Test affected extension workflows and document how consumers should adapt when behavior must change.

Distinguish these stability boundaries:

- **Framework:** The [extension framework is generally available](../../../../docs/architecture/extension-framework.md#overview). That does not make every extension or preview API stable.
- **Host contract:** Follow [extension contract versioning](../extensions/contract-versioning.md#channel-policy). Stable `v1` changes are additive and preserve existing semantics; do not reuse removed field names or numbers. New capabilities can incubate in `v1beta` and graduate compatibly to `v1`; beta is a preview channel, not the stable compatibility promise. A breaking stable change requires a new major contract. Beta clients migrate deliberately, and any deprecation or removal needs an explicit migration policy. The frozen unversioned bridge is for existing binaries, not new development.
- **Extension release:** Follow [extension semantic versioning](../extensions/extension-resolution-and-versioning.md#semantic-versioning-guidance) for commands, configuration, and behavior. Publishing to a dev registry does not confer production support or stability guarantees.

Pin a published [Go SDK version](../sdk-versioning.md) that contains the APIs you use, and declare `requiredAzdVersion` for the earliest host release that supports the required behavior. Check the contract documentation for each capability's minimum versions and migration guidance. For example, stable `Account().GetCurrentPrincipal` requires host and SDK 1.36.0 or later, while stable deployment preview requires 1.37.0 or later. Compatibility for existing extension binaries can differ from compatibility for Go source, so review both when upgrading.

For core API changes, cover older clients on the new host and the required behavior at the declared minimum host version. Include unsupported-capability behavior and a migration plan for affected consumers. Do not equate adding a protobuf field with implementing its behavior: beta additions to shared services may require a focused host override, as described in the contract guide.

### Design and PR review checklist

Use this checklist in the linked issue or design review, then link that decision from the PR. Authors and reviewers can mark an item not applicable with a reason for changes that do not affect these boundaries. This is discussion guidance, not a new automated merge gate or evidence of engineering approval.

- [ ] Describe the user workflow and why existing configuration, templates, or hooks are insufficient.
- [ ] Record the extension-first decision, the host APIs/providers/events to reuse, and the rationale for any core exception.
- [ ] Identify ownership, composition with other providers/handlers, and any missing public API with its proposed contract channel.
- [ ] State extension stability, published SDK and minimum host requirements, backward-compatibility coverage, and any consumer migration.
- [ ] Link the design discussion and unresolved decisions; request maintainer review rather than implying agreement.

## Command design principles

### 1. Verb-first command structure

- Keep verb selection minimal to avoid user confusion: `up`, `add`, `deploy`
- Use simple, common words that users already understand
- Each command group should deal with one type of object to maintain clarity

### 2. Build on existing foundations

- Use established `azd` verbs rather than creating parallel command structures (e.g., `azd init` vs `azd foo init`)
- Extend existing workflows to accommodate new functionality (eg `init`, `up`, `down`, workflow with pickers)
- Maintain backward compatibility with current usage patterns
- Avoid ambiguous or similarly-named verbs; build on core verbs instead of creating new ones

### 3. Progressive disclosure

- Basic commands work simply, advanced features are available when needed
- Users should naturally discover advanced capabilities within familiar command flows
- Maintain graceful degradation when advanced features are unavailable

### 4. Consistency across all operations

- Maintain consistent parameter patterns across similar commands
- Use established naming conventions and flag structures
- Ensure similar operations follow similar command patterns

## Command structure framework

### Primary command categories

#### View (information and status)

- Purpose: Display current state, configuration, and system information
- Examples: `list`, `show`, `version`
- Principle: Read-only operations that help users understand current state

#### Edit (configuration and management)

- Purpose: Modify configuration, manage resources, and setup operations
- Examples: `set`, `reset`, `unset`, `config`, `refresh`, `select`, `add`, `remove`, `install`, `uninstall`, `new`
- Principle: Operations that change system state or configuration

#### Run (actions and operations)

- Purpose: Execute workflows, deploy resources, and perform active operations
- Examples: `up`, `down`, `init`, `package`, `provision`, `deploy`, `run`
- Principle: Operations that perform work or execute processes

#### Other (specialized operations)

- Purpose: Specialized tasks that don't fit standard categories
- Examples: `restore`, `monitor`, `generate`, `login`, `logout`
- Principle: Unique operations specific to particular workflows

## Extension development

For extension user experience and code standards, see the [extensions style guide](../extensions/extensions-style-guide.md). For implementation steps, see [creating an extension](../../../../docs/guides/creating-an-extension.md).