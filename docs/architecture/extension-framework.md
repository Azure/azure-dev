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

### Project service save acknowledgment

`Project.AddService` supports an optional completion acknowledgment for callers
that compensate their own local edits after a failed root save. The caller sends
one fresh `azd-project-add-service-operation` metadata value (at most 64 bytes).
Only after synchronous `project.Save` returns an error, including all file-write
retries and cleanup, the host restores the previous service entry in its cache
and echoes that value in the `azd-project-add-service-save-failed` trailer.
Success and failures before the save attempt do not carry this acknowledgment.

A caller must capture fresh trailers for this invocation and require exactly one
matching value before considering compensation. Status codes or trailer presence
alone are not proof: cancellation, transport failures and malformed responses
can be observed before a host write finishes. The acknowledgment is not a
cross-file transaction; callers must still protect their own edits with locks
and ownership checks. Missing, mismatched or duplicate values are uncertain
outcomes, including when running on an older host, and require safe retention
and explicit recovery guidance.

This optional metadata contract adds no RPC or protobuf field and requires no
SDK-version bump. An extension using an older released SDK can use ordinary
gRPC metadata and trailer call options. The wire names are intentionally
duplicated across the host and such extensions; keep them aligned.

## First-Party Extensions

First-party extensions live in `cli/azd/extensions/` and are registered in `cli/azd/extensions/registry.json`.

## Detailed Reference

- [Extension Framework Guide](../../cli/azd/docs/extensions/extension-framework.md) — Getting started
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines
- [Creating an Extension](../guides/creating-an-extension.md) — Step-by-step guide
