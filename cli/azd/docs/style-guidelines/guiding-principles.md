# Guiding principles for `azd`

## Overview

Start with the workflow people need to complete. Build on familiar `azd` commands, and decide which parts belong in core and which parts an extension can own.

## Choose between core and an extension

Use these steps to shape a design proposal. They are recommendations for discussion, not a new approval gate. [Issue #6244](https://github.com/Azure/azure-dev/issues/6244) tracks the extension-first direction; maintainers confirm the boundary and any exceptions for a proposed change.

1. Describe the user need. Who needs this workflow, and does it serve one service or domain, or many kinds of azd projects?
2. Consider an extension first for service-specific or domain-specific work. Check whether lifecycle events or providers for building code, deploying services, or provisioning infrastructure can support it through existing commands. Prefer those connections over a parallel command tree that repeats `init`, `provision`, or `deploy`.
3. Consider core for shared workflow requirements. Environment selection and coordination across provisioning and deployment affect many projects. A missing shared capability may need a core change so multiple extensions can use it.
4. Check the API boundary. If an extension cannot meet the need through the current host APIs, discuss the gap with maintainers. Compare adding a shared API, trying a preview API, and implementing the feature in core. Maintainers confirm exceptions when the available APIs cannot support the design.
5. Bring the choices to an issue or design review. Explain the affected users, existing extension points, ownership, compatibility needs, and tradeoffs. Link that discussion from the pull request.

### Examples and tradeoffs

The [Azure AI projects extension](../../extensions/azure.ai.projects/README.md) owns the `microsoft.foundry` provisioning provider and `host: azure.ai.project` services. It handles Foundry-specific configuration while users continue with `azd provision` and `azd deploy`. A similar service integration is a good starting point for an extension.

Core owns `azd env select`, which selects the environment that shared workflows use. Improvements to that shared selection behavior belong in core; a service-specific command tree should not need to repeat it.

Extensions can ship service-specific changes on their own schedule and keep those dependencies outside the core binary. Users also need to install and update them, and authors need to support the host versions they declare. Core changes reach all CLI users, but they add shared maintenance work and follow the core release schedule. Discuss these costs along with the user benefit.

### API ownership and compatibility

Core owns the host APIs, which let extensions call into azd, and shared workflow behavior. Extension authors own their service-specific logic and their use of those APIs. When adding a shared workflow, consider which operations future extensions may need through APIs, lifecycle events, or providers. When proposing an API, describe the shared need and discuss its contract and maintenance with core maintainers.

Follow [extension contract versioning](../extensions/contract-versioning.md): stable `v1` changes preserve existing behavior and add capabilities compatibly. New capabilities can start in `v1beta` and move to `v1` after validation when they can do so compatibly. Beta clients move to stable deliberately; graduation does not automatically migrate them.

Pin a published [Go SDK version](../sdk-versioning.md) that contains the APIs you use, and declare `requiredAzdVersion` for the earliest host release that supports the required behavior. Check the contract documentation for each capability's minimum versions and migration guidance. For example, stable `Account().GetCurrentPrincipal` requires host and SDK 1.36.0 or later, while stable deployment preview requires 1.37.0 or later. Compatibility for existing extension binaries can differ from compatibility for Go source, so review both when upgrading.

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