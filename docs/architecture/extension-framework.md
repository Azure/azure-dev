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

- **Official registry:** `https://aka.ms/azd/extensions/registry`
- **Dev registry:** `https://aka.ms/azd/extensions/registry/dev` (unsigned builds)
- **Local sources:** File-based manifests for development

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
| `metadata` | Provide metadata about commands and capabilities |

## Available gRPC Services

Extensions can access these azd services via gRPC:

- **Project** — Read project configuration
- **Environment** — Read/write environment values and secrets
- **User Config** — Read user-level azd configuration
- **Deployment** — Access deployment information
- **Account** — Access Azure account details
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

## First-Party Extensions

First-party extensions live in `cli/azd/extensions/` and are registered in `cli/azd/extensions/registry.json`.

## Detailed Reference

- [Extension Framework Guide](../../cli/azd/docs/extensions/extension-framework.md) — Getting started
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines
- [Creating an Extension](../guides/creating-an-extension.md) — Step-by-step guide
