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
into stable after they have been validated. `ComposeService` and
`CopilotService` are currently beta-only. Removing or renumbering fields,
changing field types, and reusing reserved names or numbers remain breaking
changes in either channel.

The original unversioned `azdext` protobuf package remains available only as a
temporary frozen runtime bridge for already-built extensions. It is not a
source contract or generated SDK package for new development.

## Generate Go contracts

Run generation from `cli/azd`:

```console
make proto
```

The Makefile checks the pinned `protoc`, `protoc-gen-go`, and
`protoc-gen-go-grpc` versions already used by this repository. It generates
only under `pkg/azdext/contracts/v1` and
`pkg/azdext/contracts/v1beta`. It also regenerates the stable forwarding
surface used by the handwritten `pkg/azdext` SDK facade and the beta service
adapters in
`internal/grpcserver/versioned_services_generated.go`. `make clean` removes
only these generated outputs.

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

The first versioned source-contract change is intentionally incompatible with
the old unversioned package, while the temporary runtime bridge preserves its
frozen service addresses during migration. Use the first versioned commit as
the compatibility baseline for later source changes. For example:

```console
make proto-breaking \
  BUF_BREAKING_AGAINST='../../.git#branch=main,subdir=cli/azd/grpc'
```
