# Guiding design principles for `azd`

## Overview

The Azure Developer CLI (`azd`) follows a systematic approach to command structure design that ensures discoverability, consistency, and extensibility across all features and future enhancements. These principles provide a foundation for building any new functionality while maintaining user familiarity and operational efficiency.

Start with the user's workflow and existing `azd` capabilities. Consider an extension before adding functionality to core. Keep shared orchestration in core and service-specific behavior in extensions where the supported contracts allow it.

## Choose between core and an extension

Check [project configuration and hooks](../../../../docs/reference/azure-yaml-schema.md) first. For example, a project-specific script that runs after deployment can use a `postdeploy` hook. Use the decision tree to choose a boundary, and discuss core changes or exceptions with maintainers.

1. **Can existing configuration, templates, or hooks meet the need?**
   - Yes: reuse them. Record the decision.
   - No: continue to question 2.
2. **Must existing host-owned behavior change, such as selection, authentication, or dispatch?**
   - Yes: fix the host boundary and keep domain logic in an extension where possible. Record the decision.
   - No: continue to question 3.
3. **Can supported extension contracts implement the workflow?**
   - Yes: prefer an extension and reuse lifecycle events or providers. Record the decision.
   - No: identify the host API or orchestration gap and continue to question 4.
4. **Can a reusable host capability close that gap?**
   - Yes: propose a small core API or extension point plus an extension. Record the decision.
   - No: propose a core exception with maintainers. Record the decision.

For each outcome, record ownership, compatibility, and tradeoffs in the design or PR.

Choose the boundary based on the workflow and ownership, even when a feature serves many users. Keep shared environment selection, authentication, and deployment orchestration in the host. Compare installation and update costs, independent release needs, core binary dependencies, and long-term ownership before choosing.

### Reuse the extension host

The [extension framework](../extensions/extension-framework.md#capabilities) provides lifecycle events, framework service providers, service target providers, and provisioning providers. In Go, use the SDK's `ExtensionHost` to register the relevant handlers and providers and manage their connection to azd. Reuse host services for project/environment access, accounts, and prompts instead of maintaining parallel state or prompt flows.

Prefer these integrations when extending `init`, `provision`, `package`, or `deploy`. Add custom commands for distinct domain operations that those workflows do not express. Keep their scope within the extension's domain, and follow the [extensions style guide](../extensions/extensions-style-guide.md) for their user experience.

### Design extension points and public APIs

When designing a shared core feature, identify the operations an extension would need to participate in it. Keep orchestration separate from service-specific implementations, and check the existing [host services](../extensions/extension-framework.md#grpc-services) before proposing a new API.

- **Expose a bounded capability.** Prefer a reusable operation with typed inputs and results over exporting internal objects or adding a service-specific branch to core. An exported Go symbol alone does not define a cross-process contract. Use the supported SDK and versioned gRPC boundary.
- **Make composition explicit.** Define which provider or handler owns each phase, which configuration and artifacts it consumes and produces, and what happens when another extension participates. Specify ordering requirements, cancellation, errors, and state ownership; do not rely on undocumented handler ordering or assume multiple RPCs form a transaction.
- **Keep optional capabilities optional.** Define how consumers discover support and handle unavailable capabilities. For example, existing service-target providers can keep working without opting into deployment preview. Return an actionable error when a required capability is unavailable.
- **Plan the contract with consumers.** Describe an initial consumer and other plausible uses, the minimum host/SDK versions, compatibility tests, and an owner. Use a preview contract when the shape needs validation rather than promising stable behavior prematurely.

### Examples and tradeoffs

For example, a language extension restores dependencies, builds the app, and returns a package during `azd deploy`. A compatible service-target extension prepares that package for its target, publishes it, and deploys it. Core coordinates the workflow and passes results between providers. The authors agree on the package format and test the handoff together.

| Need | Preferred boundary and reason | Tradeoff or exception |
|---|---|---|
| Provision Microsoft Foundry resources | The [Foundry Projects extension](../../extensions/azure.ai.projects/README.md#azureyaml-ownership) owns `microsoft.foundry` provisioning and `host: azure.ai.project` services, keeping domain configuration outside core while using `azd provision` and `azd deploy`. | Users install an extension and need a compatible host; service-specific dependencies can evolve independently. |
| Add support for a language/build system | Use a [framework service provider](../extensions/extension-framework-services.md) for restore, build, and package operations rather than a second deployment pipeline. | First check whether existing providers or project hooks suffice; a new provider must work with the selected service target. |
| Add deployment preview across targets | Core coordinates `azd deploy --preview`; extension targets opt into the [stable preview contract](../extensions/extension-framework.md#deployment-preview). | Targets own their preview implementations and opt in independently. Core reports and skips unsupported targets. |
| Fix environment selection shared by workflows | Core owns `azd env select` and the selected environment used by built-in commands and extensions. | A core fix preserves one source of truth; an extension-specific environment selector would split state and behavior. |
| Fix host authentication or extension dispatch | Correct the core host boundary used before or during extension invocation. | An extension cannot reliably repair the host that launches or authenticates it. Keep unrelated domain logic outside the fix. |

A core exception should explain why existing extension points or a small reusable host addition cannot meet the requirement safely, and identify the compatibility and maintenance costs. Core changes follow the core release schedule and affect all CLI users.

### API ownership and compatibility

Core maintainers own the host APIs and shared workflow behavior. Extension authors own their domain logic, commands, configuration, release versions, and declared host requirements. Review core changes for their effect on already-published extension binaries as well as clients built with the new SDK.

Include configuration, defaults, lifecycle timing, and shared-state behavior in that review. Core changes can break an extension even when the protobuf contract stays the same. Test affected extension workflows and document how consumers should adapt when behavior must change.

Distinguish these stability boundaries:

- **Framework:** The [extension framework is generally available](../../../../docs/architecture/extension-framework.md#overview). That does not make every extension or preview API stable.
- **Host contract:** Follow [extension contract versioning](../extensions/contract-versioning.md#channel-policy). Stable `v1` changes are additive and preserve existing semantics. Use `v1beta` to validate preview capabilities before compatible graduation to `v1`. A breaking stable change requires a new major contract. Plan consumer migration for any deprecation or removal, including beta APIs. New development uses the versioned contracts.
- **Extension release:** Follow [extension semantic versioning](../extensions/extension-resolution-and-versioning.md#semantic-versioning-guidance) for commands, configuration, and behavior. Publishing to a dev registry does not confer production support or stability guarantees.

Pin a published [Go SDK version](../sdk-versioning.md) that contains the APIs you use, and declare `requiredAzdVersion` for the earliest host release that supports the required behavior. Check the contract documentation for each capability's minimum versions and migration guidance. Review both existing extension binaries and Go source when upgrading; their compatibility requirements can differ.

For core API changes, test older clients on the new host, required behavior at the declared minimum host version, and unsupported-capability behavior. Verify that the host implements each new contract capability. See [host registration and adaptation](../extensions/contract-versioning.md#host-registration-and-adaptation) for beta additions that need a focused host override.

### Design and PR review checklist

Use this checklist to summarize the design for maintainers and other contributors. Link the design discussion from the PR, and mark items that do not apply with a reason.

- [ ] Describe the user workflow and why existing configuration, templates, or hooks are insufficient.
- [ ] Record the extension-first decision, the host APIs/providers/events to reuse, and the rationale for any core exception.
- [ ] Identify ownership, composition with other providers/handlers, and any missing public API with its proposed contract channel.
- [ ] State extension stability, published SDK and minimum host requirements, backward-compatibility coverage, and any consumer migration.
- [ ] Identify unresolved decisions for maintainer review.

## Command design principles

### 1. Verb-first command structure

- Reuse familiar verbs such as `up`, `add`, and `deploy`
- Use simple, common words that users already understand
- Group commands around one type of object, such as environments or extensions

### 2. Build on existing foundations

- Extend familiar workflows such as `azd init`, `azd up`, and `azd down` when the new functionality serves the same task
- Reuse existing pickers for shared choices such as environment and subscription
- Maintain backward compatibility with current usage patterns
- Avoid ambiguous or similarly named verbs

### 3. Progressive disclosure

- Keep the common workflow simple, and expose advanced options through flags, help, and relevant prompts
- Help users discover advanced capabilities within familiar command flows
- Explain unavailable optional features and how users can continue; fail clearly when the workflow requires them

### 4. Consistency across all operations

- Match parameter names, defaults, and flag structures across similar commands
- Follow the [core CLI style guide](azd-style-guide.md) for core prompts, output, and errors
- Support scripts and CI with deterministic inputs and the command's documented non-interactive behavior

## Command structure framework

### Primary command categories

Use these categories to check what a command does and whether its name matches the user's task. They describe operations within the command tree.

#### View (information and status)

Read-only operations help users inspect state, configuration, and system information.

- Examples: `list`, `show`, and `version`

#### Edit (configuration and management)

These operations change configuration, manage resources, or set up environments and tools.

- Examples: `set`, `reset`, `unset`, `config`, `refresh`, `select`, `add`, `remove`, `install`, `uninstall`, and `new`

#### Run (actions and operations)

These operations execute workflows, such as preparing an app or deploying resources.

- Examples: `up`, `down`, `init`, `package`, `provision`, `deploy`, and `run`

#### Other (specialized operations)

Specialized operations use names familiar to users of that workflow.

- Examples: `restore`, `monitor`, `generate`, `login`, and `logout`

## Extension development

For extension user experience and code standards, see the [extensions style guide](../extensions/extensions-style-guide.md). For implementation steps, see [creating an extension](../../../../docs/guides/creating-an-extension.md).