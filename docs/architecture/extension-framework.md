# Extension Framework

<!-- cspell:ignore azdext -->

Architecture of the gRPC-based extension system in azd.

## Overview

Extensions are external processes that communicate with azd via gRPC. They allow third parties and first-party teams to add new capabilities — languages, hosting targets, event handlers, and more — without modifying the core CLI.

The extension framework itself is **generally available (GA)** and considered stable. See the [Azure Developer CLI extension framework is GA](https://devblogs.microsoft.com/azure-sdk/azd-extension-framework-ga/) announcement for release context. Individual extensions, registries, or specific capabilities may still carry their own experimental or pre-release status independent of the framework's GA status.

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

Extension command metadata can set `"launch": {"noAzdEnvironment": true}` on a command.
For that command only (not its descendants), azd skips loading the selected
environment, resolving its Key Vault references, and injecting its variables
before starting the extension. Inherited parent-process environment variables
and azd launch/protocol variables are still passed. Explicit Environment gRPC
calls are unaffected.

### Lifecycle follow-up contributions

The beta event stream supplies an invocation ID to each subscribed project
handler. The extension calls the beta `CommandResultService` with that ID over
gRPC; both services use a host-owned invocation store. Only successful
project `post*` invocations are committed to the command's follow-up
collector. The UX middleware appends the resolved text to human-readable
completion output without changing JSON output. Within a custom workflow,
later command steps take precedence over earlier steps.

The host's [command-result collector](../../cli/azd/internal/commandresult/follow_up.go)
is separate from the [gRPC service and invocation store](../../cli/azd/internal/grpcserver/command_result_service.go).

See the [SDK reference](../../cli/azd/docs/extensions/extension-sdk-reference.md#project-lifecycle-follow-up)
for the API, host compatibility, and contribution ordering rules.

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
- **Account**. List subscriptions and resolve access tenants. Since azd 1.36.0, stable `Account().GetCurrentPrincipal` retrieves the current principal's resource-tenant object ID and type for role assignments. The equivalent beta API is deprecated. See [GetCurrentPrincipal](../../cli/azd/docs/extensions/extension-framework.md#getcurrentprincipal).
- **Telemetry**. Since azd 1.36.0, stable `Telemetry().ReportUsage` records eligible extension usage events. Its deprecated beta wire route remains available to released binaries. See [contract graduation and SDK migration](../../cli/azd/docs/extensions/contract-versioning.md#october-2026-graduation-azd-1360).
- **Prompt** — Display prompts and collect user input
- **AI Model** — Query AI model availability and quotas
- **Event** — Subscribe to and emit events
- **Follow-up (beta)** — Contribute command-level guidance from project lifecycle handlers; see the [SDK reference](../../cli/azd/docs/extensions/extension-sdk-reference.md#project-lifecycle-follow-up)
- **Container** — Container registry operations
- **Framework** — Framework service operations
- **Service Target** — Deployment target operations

Structured deploy messages use the beta event stream. Go extensions opt in
with `ExtensionHost.WithBetaServiceEventHandler` for service `predeploy` and
`postdeploy` events; the stable event contract and default language scaffolds
remain unchanged. See
[contract versioning](../../cli/azd/docs/extensions/contract-versioning.md)
for beta stream routing and legacy-client behavior.

## Error Handling

Extensions use two structured error types:

- **`ServiceError`** — For Azure API or remote service errors
- **`LocalError`** — For client-side validation or configuration errors

Error precedence: ServiceError → LocalError → azcore.ResponseError → gRPC auth → fallback

For directly invoked extension commands, the host preserves the extension process's exit code, including when a structured error is reported. Invocation failures without an available exit code remain code `1`. See [Invoking Extension Commands](../../cli/azd/docs/extensions/extension-framework.md#invoking-extension-commands).

### Service configuration persistence

The existing `ProjectService.AddService` RPC replaces one complete service;
service-specific setters change only their selected field or section. The host
holds the existing mutation lock during each operation and preserves dispatcher
and cache behavior. Setters reload raw configuration, validate a typed view,
normalize known path fields, and atomically save the raw mapping. AddService
serializes only the replacement service into the freshly loaded raw project.
It parses that file snapshot and validates the merged structure using the
existing typed-save rules. Path normalization and schema selection use the same
snapshot rather than cached project data.
Untouched fields, including explicit `env: {}` or `infra: {}`, large integers,
and literal templates, do not round-trip through protobuf or typed YAML.

Service names are literal keys, including dots. Paths within services retain
their existing dot-notation meaning. Multi-RPC edits are not transactions;
there is no compare-and-swap precondition. Caller-provided protobuf numbers
retain their existing precision limits. Prefer targeted setters over resending
unknown configuration.

### Project service save acknowledgment

The discoverable preview contract is `v1beta.ProjectService.GetAddServiceCapabilities`.
This read-only RPC advertises whether the active `AddService` implementation
supports `AddServiceRequest.operation_id` and `AddServiceAcknowledgment` status
details. An unrelated focused beta override does not disable the built-in
implementation. A custom `AddService` override must explicitly advertise its
own support; the built-in capability response otherwise reports false.

When supported, the caller supplies a fresh operation ID of at most 64 bytes
(not characters) and makes exactly one beta `AddService` call. Longer identifiers
are rejected with `InvalidArgument` before project mutation or saving.
A failed operation that acquired the project mutation
lock attaches an `AddServiceAcknowledgment` detail echoing that ID. Completion,
including synchronous save retries, cleanup, and cache restoration, happens
under the mutation lock. The status detail is serialized after the shared
mutation helper returns and releases that lock. Success, panics, and errors
rejected before the lock, cancellation, and deadline expiration do not carry a
failure acknowledgment. Existing host-error codes, messages, suggestions, and
structured details are preserved alongside the acknowledgment. Detail-attachment
failures are explicit internal errors, not confirmed completion.

Require exactly one well-formed matching detail before compensating local
edits. Missing, malformed, stale, wrong-type, or duplicate details are uncertain
outcomes. Cancellation, deadlines, authentication/authorization and transport
failures are not safe completion signals. Completion is not proof that the
root file stayed unchanged: retain root-byte comparisons, local locks, and
ownership checks before rollback.

The authentication/authorization exclusions above are a consumer recovery
policy, not additional host acknowledgment suppression.

An explicit false capability response or `Unimplemented` from the read-only
RPC selects the stable host path. Other capability errors stop before
mutation; errors from the subsequent mutation never trigger a fallback replay.
Stable calls can succeed normally, but failed saves retain the scaffold
with manual inspection guidance.

The evaluations extension uses a private dynamic descriptor registry
generated from the canonical beta schema by `grpc/generateprojectclient`.
This permits reproducible builds with its released SDK pin, without a local
replace, pseudo-version, duplicate schema, or conflicting global protobuf
registration. Its private client supplies the project fields absent from its
released SDK pin. Regeneration is part of `make proto`.
Completion acknowledgments exist only on the typed beta API.
Stable callers receive ordinary errors and retain uncertain
scaffolds for manual recovery, without trusting custom metadata or trailers.

## Deployment Preview

`azd deploy --preview` calls an optional `Preview` on each selected service target
instead of packaging, publishing, and deploying. Service targets are not initialized
and deploy hooks do not run; hosts without preview support are reported and skipped.
The built-in App Service, Container Apps, Functions, Static Web Apps, AKS, and AI
endpoint hosts report the resolved Azure target and the deployment operation they
would perform.

Extensions opt in with `WithServiceTargetPreview` and
`azdext.ServiceTargetPreviewProvider`. The contract graduates to **stable v1**,
targeting host and SDK 1.37.0: the host
stays on the stable service target stream for normal deployments and also registers
on a dedicated v1 stream that carries only preview registration and preview
messages. Each preview runs on a fresh provider without `Initialize`.
Deprecated beta registrations, provider interfaces, and wire routes remain
supported without a removal date. The generated beta adapter delegates to the
same stable host implementation; the frozen unversioned bridge is not extended.
The [second graduation audit](https://github.com/Azure/azure-dev/issues/10625)
tracks actual release completion, and [consumer migration](https://github.com/Azure/azure-dev/issues/10626)
waits for the published SDK before updating the Foundry extensions.
See [Deployment Preview](../../cli/azd/docs/extensions/extension-framework.md#deployment-preview)
for registration and provider requirements.

## First-Party Extensions

First-party extensions live in `cli/azd/extensions/` and are registered in `cli/azd/extensions/registry.json`.

## Detailed Reference

- [Extension Framework Guide](../../cli/azd/docs/extensions/extension-framework.md) — Getting started
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines
- [Creating an Extension](../guides/creating-an-extension.md) — Step-by-step guide
