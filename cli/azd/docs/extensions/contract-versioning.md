# Extension contract versioning

The protobuf contract between azd core and extensions has stable and beta
channels. The channel is part of both the protobuf package name and every gRPC
full method name.

| Channel | Proto source | Wire package | Generated Go package |
|---|---|---|---|
| Stable | `grpc/proto/azd/extensions/v1` | `azd.extensions.v1` | `pkg/azdext/contracts/v1` |
| Beta | `grpc/proto/azd/extensions/v1beta` | `azd.extensions.v1beta` | `pkg/azdext/contracts/v1beta` |

The handwritten `pkg/azdext` package remains the internal Go SDK facade for the
stable v1 channel. It forwards its generated contract types, clients, and
server interfaces from `pkg/azdext/contracts/v1`; protobuf-generated files do
not share the facade package with handwritten SDK functionality. Go clients
that intentionally target beta import `pkg/azdext/contracts/v1beta` directly.
`CommandResultService`, `ComposeService`, and `CopilotService`
are beta-only and therefore do not have stable `v1`
generated types or facade aliases. The corresponding `AzdClient` convenience
accessors return generated `v1beta` clients, and their request and response
types come from `contracts/v1beta`. Preview project lifecycle events use
`EventsBeta()` and `CommandResult()` with generated beta request and response
types.
Stable event handlers and default language scaffolds do not expose follow-up APIs.
See [Project lifecycle follow-up](extension-sdk-reference.md#project-lifecycle-follow-up)
for the preview SDK contract.

`AzdClient.ProjectBeta()` exposes the generated preview project client
alongside the stable `Project()` accessor. Clients whose SDK exposes it use
`ProjectBeta().GetAddServiceCapabilities` before opting into
typed `AddService` completion acknowledgments.

## Channel policy

`v1` is the compatibility-protected stable contract. Changes must be additive:
add new fields with new numbers, add enum values where consumers tolerate
unknown values, or add new methods without changing existing request and
response semantics. Never reuse removed field names or numbers.

`v1beta` is a long-lived preview channel, not a sequence of short-lived
`v2beta1` packages. New contract capabilities can incubate there. After
validation, a capability graduates into the current stable major (`v1`) when
it can be added compatibly. A future `v2` stable package is introduced only
when a change cannot preserve `v1` compatibility. A beta client continues to
use the beta package until it deliberately moves to the corresponding stable
API.

The original `azdext` wire package is retained only through a temporary frozen
runtime bridge for already-built extensions. New development must use `v1` or
`v1beta`. Compatibility checks use the first merged versioned-contract commit
as their baseline, not the old unversioned sources.

## Host registration and adaptation

The azd gRPC host registers the generated `v1` and `v1beta` service
descriptors and handlers on the same server. The generated beta servers in
`internal/grpcserver/versioned_services_generated.go` satisfy the real
`v1beta` server interfaces; the host does not clone or rewrite stable
descriptors.

A service that exists only in beta is implemented directly with the generated
`v1beta` server interface and registered without a stable adapter.
`CommandResultService`, `ComposeService`, and `CopilotService`
currently use this model. Focused beta overrides apply
only to beta additions on services that also exist in stable; registration
rejects an override for a beta-only service because its native implementation
already owns the full beta contract.

For a method shared by both channels, the beta server:

1. Calls a focused beta override when one is configured for that method.
2. Otherwise transcodes the beta request by protobuf wire data, calls the
   stable implementation with the same context, and transcodes the stable
   response back to beta.

This keeps stable business logic as the default implementation without
decoding a beta route directly into a stable message. Conversion failures are
returned with the service and method context. The same bridge supports unary
and bidirectional streaming methods.

For unary methods, additive beta request fields are not exposed to the stable
implementation. Unary adaptation discards fields unknown to the stable request
type. A preview field therefore must not be documented as functional until
that method has a beta override.

Bidirectional stream adaptation preserves unknown wire fields when converting
beta messages to stable messages. Existing error decoding relies on that
preservation, but the unknown fields are not exposed as typed fields on the
stable message. Preview stream behavior that needs typed beta data therefore
still requires a focused beta stream override.

The beta lifecycle `EventStream` override selects compatibility behavior from
the first subscription. A subscription without `request_id` is delegated to
the stable event implementation, preserving legacy correlation for existing
beta clients. This mode has no beta subscription acknowledgements or
invocation IDs and cannot use beta-only follow-up or structured deploy
messages. A subscription with `request_id` selects the current beta mode;
later subscriptions and project handler statuses must also carry IDs.
Every modern service invocation uses a unique `request_id`. A status carrying
structured messages must echo that ID. For compatibility, an ID-less status
without messages is correlated only when exactly one matching service/event
invocation is outstanding and no canceled invocation could have replied late.
An unmatched status is not assigned to a new invocation. If multiple calls or
a canceled call make the mapping ambiguous, the host returns
`InvalidArgument` instead of guessing. Clients that omit response IDs should
serialize calls for each service/event pair and open a new stream after
cancellation uncertainty. A status with a known ID for a different
service/event pair is rejected with `InvalidArgument`; unknown IDs remain
unmatched, including late canceled responses. Stable `v1` behavior is unchanged.

An override implements one or more generated
`Beta<Service><Method>Override` interfaces and is installed with
`server.WithOptions(WithBetaServiceOverride(...))`; it receives the true
generated beta request or stream type before any transcoding. Override values
should implement only the focused method interfaces they need. Do not embed the generated
`Unimplemented<Service>Server`: doing so would claim every method. Host
registration rejects whole generated beta servers, unknown service keys, and
values that do not implement a focused interface.

An additive beta enum value on an existing shared request field is different
from an additive field: proto3 preserves its numeric value in the stable
message even when stable does not define that value. Such a preview value also
requires a beta method override; stable business logic must not be expected to
interpret it.

The lifecycle `EventService.EventStream` uses one focused beta override for
project follow-up contributions and structured service deploy messages.
`ServiceHandlerStatus.messages` is collected from the final response, so a
hook can return guidance even when it also fails. The same override handles
subscription acknowledgements, invocation IDs, and follow-up
commit/discard behavior; registering a separate message override would
replace those behaviors. Stable event messages and stable handler APIs remain
unchanged. Go extensions opt in with
`ExtensionHost.WithBetaServiceEventHandler`.

An additive beta-only method does not require a matching stable method.
`make proto` generates a beta handler that calls a focused override when
present and otherwise returns `codes.Unimplemented`. Adding the method cannot
make host registration fail. Wire the override through the existing
`NewServer` options rather than adding another constructor dependency. After
the capability graduates to stable, the regenerated adapter automatically
uses stable business logic when no override is configured.

Built-in focused project overrides compose with caller overrides per method.
A caller override for `Get`, for example, does not suppress the built-in
`AddService` acknowledgment or read-only capability handler. Custom `AddService`
implementations must explicitly supply their own capability response to advertise
acknowledgment support.

For preview fields absent from released SDKs, the evaluations extension snapshots
canonical descriptors with `grpc/generateprojectclient` during `make proto` and
uses a private dynamic registry. This avoids duplicate global registration and
does not introduce a second schema. The private client selects the stable
host path when the read-only beta capability RPC explicitly reports unsupported
or returns `Unimplemented`.

Stable handlers can return gRPC statuses containing stable contract messages
in `Any` details. Before a beta response is sent, the host translates every
`azd.extensions.v1.*` detail to the matching generated
`azd.extensions.v1beta.*` message and type URL. Standard details such as
`google.rpc.ErrorInfo` are preserved unchanged. A missing or invalid beta
detail type produces an explicit internal error instead of dropping the
detail or leaking a stable type URL.

Contract tests enforce that stable is a subset of beta. They allow additive
beta files, messages, enum values, fields, and methods, while requiring every
shared field number, name, kind, cardinality, presence, oneof membership, and
message or enum type to remain compatible. Shared methods must retain their
request and response types and client/server streaming shape.

The temporary legacy bridge separately aliases the frozen pre-versioning
`azdext.ComposeService` and `azdext.CopilotService` addresses to their native beta implementations. That
compatibility alias does not make those services part of stable `v1`.

## October 2026 graduation (azd 1.36.0)

The [graduation history issue](https://github.com/Azure/azure-dev/issues/10436)
records this first batch, compatibility decisions, consumer follow-ups, and
actual release completion.

The first graduation adds `TelemetryService.ReportUsage` and
`AccountService.GetCurrentPrincipal` (including their complete message and enum
shapes) to `v1`. Their beta symbols are deprecated, but both beta routes remain
registered and delegate to the same stable implementation. No removal date is
set; removal requires a separately approved migration policy. This retention
is distinct from the temporary frozen unversioned bridge.

`AzdClient.Telemetry()` now returns a stable client. This is an intentional Go
source-compatibility break: callers upgrading the SDK must replace beta
telemetry request/response types with stable types. No `TelemetryBeta()`
accessor is introduced. Code deliberately using the deprecated beta route can
construct `v1beta.NewTelemetryServiceClient` directly.
`AccountBeta()` remains source-compatible but deprecated; use `Account()`.
The shared `pkg/foundry/telemetry.Client` interface also uses stable types.

Already-released extension binaries using beta or frozen unversioned telemetry
keep working on new hosts. Extensions pinned to older SDK versions do not need
to change until adopting the new SDK. New stable consumers must use the
published `cli/azd/v1.36.0` SDK or later and declare
`requiredAzdVersion: ">=1.36.0"`. First-party migrations and releases are
tracked separately from the host graduation, except for the demo extension:
it shares the core Go module and migrates in the host PR to keep core builds
working. Its next published version also requires azd 1.36.0.

Consumer migration and release follow-ups:
[extension usage telemetry](https://github.com/Azure/azure-dev/issues/10438)
and [current Azure principal](https://github.com/Azure/azure-dev/issues/10437).

## October 2026 second graduation (target azd 1.37.0)

The [second batch audit](https://github.com/Azure/azure-dev/issues/10625) records
deployment preview graduation, shipped in host and SDK 1.37.0 on October 21, 2026.
The [release](https://github.com/Azure/azure-dev/releases/tag/azure-dev-cli_1.37.0)
and SDK tag `cli/azd/v1.37.0` point to commit `c49286c2955a7ba5fc13487d960a723687651cc3`. The permanent
[history](https://github.com/Azure/azure-dev/issues/10436) links both October
batches.

The additive stable surface is `RegisterServiceTargetRequest.supports_preview=2`,
`ServiceTargetMessage.preview_request=21` and `preview_response=22` in the
existing `message_type` oneof, plus `ServiceTargetPreviewRequest(service_config=1)`,
`ServiceTargetPreviewResponse(result=1)` and
`ServiceDeployPreviewResult(message=1, data=2)`. It retains the existing
bidirectional `ServiceTargetService.Stream` method and all beta field types and
numbers. Stable preview registrations use a dedicated v1 stream, separate from
ordinary service target lifecycle traffic.

The beta preview fields and messages are deprecated, not removed. The focused
beta service target override is no longer needed: the generated adapter
transcodes requests/responses into the same stable preview implementation.
Already-built beta clients retain their route and behavior, including preview
opt-in, provider errors, and extension ownership checks. The frozen unversioned
bridge gains no preview capability. There is no beta removal date.

The new stable Go API is `ExtensionHost.WithServiceTargetPreview` with
`azdext.ServiceTargetPreviewProvider` and stable config/result types.
`ServiceTarget()` and `ServiceTargetEnvelope` expose the stable messages.
`WithBetaServiceTargetPreview`, `preview.ServiceTargetPreviewProvider`,
`BetaServiceTarget()`, and the beta preview envelope remain source-compatible
but deprecated. The SDK preserves their beta stream and provider types through
compatibility adapters; no Go accessor return type changes or source breaks
are introduced in this batch.

Preview remains read-only: a fresh provider runs without `Initialize`, build,
package, publish, deployment state writes, or deploy hooks. Structured
predeploy/postdeploy messages remain beta-only and are not part of this graduation.

[Consumer migration and release tracking](https://github.com/Azure/azure-dev/issues/10626)
covers the verified agents/projects consumers.
[Migration PR #10571](https://github.com/Azure/azure-dev/pull/10571) migrates preview
consumers and pins all nine affected extension modules to the published SDK v1.37.0 with
`requiredAzdVersion: ">=1.37.0"`, retaining stricter future requirements if needed.
Do not publish an SDK pin before its version exists or merge local replacements.

## Graduation audit requirements

For every graduation, record the capability, complete stable surface, target
release, beta deprecation/retention policy, SDK migration decisions,
implementation PR, validation, and per-capability consumer migration issues
in a public GitHub issue. Update the record with the actual merged commit and
release date once released; a target version is not proof of release.

See [`grpc/README.md`](../../grpc/README.md) for generation, lint, and Buf
compatibility commands.
