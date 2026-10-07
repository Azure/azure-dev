# Extension Framework

Architecture of the gRPC-based extension system in azd.

## Overview

Extensions are external processes that communicate with azd via gRPC. They allow third parties and first-party teams to add new capabilities — languages, hosting targets, event handlers, and more — without modifying the core CLI.

## Architecture

```text
azd (host)
  ├── Extension Registry (discovery)
  ├── Extension Manager (lifecycle)
  └── gRPC Broker (communication)
        ↕ gRPC
      Extension Process
        ├── Capability Handlers
        └── Service Implementations
```

### Discovery

Extensions are discovered from registries — JSON manifests that list available extensions with their versions, capabilities, and download URLs.

- **Official registry:** `https://aka.ms/azd/extensions/registry` — Stable, signed, production-ready extensions vetted by the azd team.
- **Dev registry:** `https://aka.ms/azd/extensions/registry/dev` — Experimental and pre-release extensions (unsigned builds, backed by `cli/azd/extensions/registry.dev.json`). Not configured by default; users opt in with `azd extension source add`.
- **Local sources:** File-based manifests for development

The dev registry serves as a staging area for extensions before they graduate to the main registry. Extensions installed from the dev registry are automatically promoted to the main registry when a newer stable version becomes available there. See the [Extension Resolution and Versioning](../../cli/azd/docs/extensions/extension-resolution-and-versioning.md#devexperimental-extension-registry) guide for detailed criteria, stability expectations, and submission guidelines.

### Lifecycle

1. User installs an extension: `azd extension install <name>`
2. Extension binary is downloaded and cached locally
3. When needed, azd spawns the extension process
4. gRPC connection is established via the broker
5. azd invokes capability methods on the extension
6. Extension responds via gRPC

### Communication

The gRPC broker (`pkg/grpcbroker`) manages bidirectional communication. Extensions can both:

- **Receive calls** from azd (e.g., "build this service")
- **Make calls** back to azd (e.g., "prompt the user", "read environment config")

## Capabilities

Extensions declare their capabilities in `extension.yaml`:

| Capability | Description |
|---|---|
| `custom-commands` | Expose new command groups and commands to azd |
| `lifecycle-events` | Subscribe to azd project and service lifecycle events (pre/post provision, deploy, etc.) |
| `mcp-server` | Provide Model Context Protocol tools for AI agents |
| `framework-service-provider` | Add build/restore support for new languages |
| `service-target-provider` | Add deployment support for new hosting targets |
| `provisioning-provider` | Add custom infrastructure provisioning support |
| `metadata` | Provide metadata about commands and capabilities |

## Available gRPC Services

Extensions can access these azd services via gRPC:

- **Project** — Read project configuration
- **Environment** — Read/write environment values and secrets
- **User Config** — Read user-level azd configuration
- **Deployment** — Access deployment information
- **Account**. List subscriptions and resolve access tenants. The `v1beta` client also retrieves the current principal's resource-tenant object ID and type for role assignments. See [GetCurrentPrincipal](../../cli/azd/docs/extensions/extension-framework.md#getcurrentprincipal).
- **Prompt** — Display prompts and collect user input
- **AI Model** — Query AI model availability and quotas
- **Event** — Subscribe to and emit events
- **Container** — Container registry operations
- **Framework** — Framework service operations
- **Service Target** — Deployment target operations

## Error Handling

Extensions use two structured error types:

- **`ServiceError`** — For Azure API or remote service errors
- **`LocalError`** — For client-side validation or configuration errors

Error precedence: ServiceError → LocalError → azcore.ResponseError → gRPC auth → fallback

For directly invoked extension commands, the host preserves the extension process's exit code, including when a structured error is reported. Invocation failures without an available exit code remain code `1`. See [Invoking Extension Commands](../../cli/azd/docs/extensions/extension-framework.md#invoking-extension-commands).

### Project service save acknowledgment

The discoverable preview contract is `v1beta.ProjectService.GetAddServiceCapabilities`.
This read-only RPC advertises whether the active `AddService` implementation
supports `AddServiceRequest.operation_id` and `AddServiceAcknowledgment` status
details. Stable `Project.AddService` calls return ordinary errors and do not
provide completion acknowledgments.

When supported, set `AddServiceRequest.operation_id` to a fresh
per-call identifier of at most 64 bytes (not characters). Longer identifiers
are rejected with `InvalidArgument` before project mutation or saving.
The host returns an `AddServiceAcknowledgment` with that
identifier as a `google.rpc.Status` detail on the same completed failures.
Existing host-error codes, messages, and structured
details are preserved alongside the acknowledgment. The stable protobuf shape
is unchanged.

`GetAddServiceCapabilitiesResponse.acknowledgment_supported` is true for the
built-in implementation, including when an unrelated focused method is
overridden. A custom `AddService` override is unsupported unless it explicitly
implements the capability override and guarantees the acknowledgment contract.
The probe does not load or save project configuration.

Only an explicit unsupported response or `Unimplemented` from this probe
permits an older stable-SDK path. Authentication, cancellation, and transient
probe errors abort before mutation. Once a mutation path is selected, do not
fall back or replay on any mutation error, including `Unimplemented`.
The stable path cannot establish typed completion; do not infer support from
legacy trailers or the presence of a beta route.

Require exactly one matching acknowledgment before considering compensation
of local edits. Missing, mismatched, duplicate, or lost acknowledgments remain
uncertain outcomes. Cancellation or a transport error can reach the caller
while host work is still running. Completion does not prove that no bytes were
written before a failure: compare root-file bytes and check local ownership
before compensation. This protocol is not a cross-file transaction.

The beta API requires a published SDK containing the accessor, request field,
and status-detail type, and a host containing the focused beta override.
Earlier beta hosts can discard an unknown request field, so route availability
alone does not prove completion-acknowledgment support. Pin a verified containing
SDK and establish the compatible host requirement only after that release
exists. Local builds, replacements, and source ancestry do not establish
published availability.

## Deployment Preview

`azd deploy --preview` calls an optional `Preview` on each selected service target
instead of packaging, publishing, and deploying. Service targets are not initialized
and deploy hooks do not run; hosts without preview support are reported and skipped.
The built-in App Service, Container Apps, Functions, Static Web Apps, AKS, and AI
endpoint hosts report the resolved Azure target and the deployment operation they
would perform.

Extensions opt in with `WithBetaServiceTargetPreview` and
`preview.ServiceTargetPreviewProvider`. The contract is **v1beta-only**: the host
stays on the stable service target stream for normal deployments and also registers
on a dedicated v1beta stream that carries only preview registration and preview
messages. Each preview runs on a fresh provider without `Initialize`.
See [Deployment Preview](../../cli/azd/docs/extensions/extension-framework.md#deployment-preview)
for registration and provider requirements.

## First-Party Extensions

First-party extensions live in `cli/azd/extensions/` and are registered in `cli/azd/extensions/registry.json`.

## Command Documentation Routing

The host creates intermediate command groups for dotted extension namespaces. For example,
extensions registered as `ai.eval` and `ai.dataset` share the host-owned `azd ai` group.
`azd ai --docs` opens the [extensions overview](https://learn.microsoft.com/azure/developer/azure-developer-cli/extensions/overview),
not a generated anchor in the built-in command reference. Existing built-in command groups
retain their reference anchors even when an extension adds a child command.

Commands at and below the extension's own namespace are delegated to the extension process.
Their flags, including whether they support `--docs`, are determined by that extension.
Use `--help` for its command-specific usage.

## Detailed Reference

- [Extension Framework Guide](../../cli/azd/docs/extensions/extension-framework.md) — Getting started
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines
- [Creating an Extension](../guides/creating-an-extension.md) — Step-by-step guide
