# Extension protobuf contracts

The extension wire contracts have two public channels:

- `proto/azd/extensions/v1` is the stable contract. Its protobuf package is
  `azd.extensions.v1`, and its generated Go package is
  `pkg/azdext/contracts/v1`.
- `proto/azd/extensions/v1beta` is the long-lived beta contract. Its protobuf
  package is `azd.extensions.v1beta`, and its generated Go package is
  `pkg/azdext/contracts/v1beta`.

The beta channel is a superset of stable. New additive contract fields,
methods, and beta-only services can incubate there and graduate additively
into stable after they have been validated. `ComposeService`,
`CommandResultService` and `CopilotService` are currently
beta-only. Removing or renumbering fields, changing field types, and reusing
reserved names or numbers remain breaking changes in either channel.

`TelemetryService.ReportUsage` and `AccountService.GetCurrentPrincipal`
graduate to stable in azd 1.36.0 (October 2026). Their deprecated beta routes
continue delegating to stable behavior; removing them requires a separately
approved policy. See the [graduation and SDK migration policy](../docs/extensions/contract-versioning.md#october-2026-graduation-azd-1360).

Deployment preview graduates on the existing stable `ServiceTargetService.Stream`,
targeting azd 1.37.0. Its `supports_preview` opt-in, preview envelope fields, and
request/response/result messages retain their beta wire shapes. Beta preview
members and SDK helpers are deprecated but retained; the generated beta adapter
delegates to stable behavior without a focused override. The frozen unversioned
bridge is unchanged. See the [second graduation policy and release tracking](../docs/extensions/contract-versioning.md#october-2026-second-graduation-target-azd-1370).

The original unversioned `azdext` protobuf package remains available only as a
temporary frozen runtime bridge for already-built extensions. It is not a
source contract or generated SDK package for new development.

Structured deploy messages are beta-only. Service `predeploy` and
`postdeploy` handlers can return non-blocking info or warning messages in
`ServiceHandlerStatus.messages`. Go extensions register them with
`ExtensionHost.WithBetaServiceEventHandler`; the default language scaffolds
and stable event contracts remain unchanged.

Messages use the correlated beta event stream. Its first subscription must
include a `request_id`, and each service status carrying messages must echo
the invocation's ID. Legacy beta streams retain their existing behavior but
cannot send structured messages. See the extension contract versioning guide
for host routing and compatibility behavior.

## Generate contracts

Run generation from `cli/azd`:

```console
go tool mage generateProtos
```

The Mage target runs the pinned protobuf toolchain in a container and
regenerates the stable Python and JavaScript extension scaffold bindings plus
the Go contracts under `pkg/azdext/contracts/v1` and
`pkg/azdext/contracts/v1beta`. The Go generation step also regenerates the
stable forwarding surface used by the handwritten `pkg/azdext` SDK facade and
the beta service adapters in
`internal/grpcserver/versioned_services_generated.go`.
It also snapshots the canonical beta project descriptor set for the evaluations
extension's released-SDK-compatible private dynamic client with
`grpc/generateprojectclient`. Do not edit this generated snapshot or duplicate
the protobuf schema in the extension.
Before generating bindings, it compiles descriptor sets for the stable
scaffold protos and canonical `v1` protos and verifies that every scaffold
message, enum, service, and method remains a wire-compatible subset of the
canonical contract.

The container invokes `make proto` for the Go artifacts. That target checks
the pinned `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` versions and can
be used directly when only Go contracts need regeneration and those tools are
already installed. `make clean` removes only the generated Go outputs.

The adapter generator reads the generated `v1` and `v1beta` server interfaces.
Shared methods transcode protobuf messages to reuse stable business logic.
Beta-only methods on a shared service use a focused beta override hook and an
`Unimplemented` fallback. Services that exist only in beta are registered
directly with their native `v1beta` implementation. Do not edit the generated
adapter file directly.

The vendored `google/protobuf/struct.proto` is shared by both channels from
`grpc/include`.

## Buf checks

`buf.yaml` uses the v2 configuration format and applies `STANDARD` lint plus
`FILE` compatibility rules to both channels.

```console
make proto-lint
make proto-breaking BUF_BREAKING_AGAINST='<buf module, image, or Git source>'
```

The Go contract tests add a cross-channel rule that Buf does not express:
stable must remain a wire-compatible subset of beta. Additive beta fields and
methods and beta-only services are allowed, but shared field and method shapes
must remain compatible with the generated adapters.

Run this cross-channel check directly with:

```console
make proto-version-compatibility
```

The check names the first missing or incompatible message, field, enum,
service, or method. CI runs it as a dedicated gate after regenerating the
contracts, so a `v1beta` change cannot silently break the shape inherited from
stable `v1`.

The first versioned source-contract change is intentionally incompatible with
the old unversioned package, while the temporary runtime bridge preserves its
frozen service addresses during migration. Use the first versioned commit as
the compatibility baseline for later source changes. For example:

```console
make proto-breaking \
  BUF_BREAKING_AGAINST='../../.git#branch=main,subdir=cli/azd/grpc'
```
