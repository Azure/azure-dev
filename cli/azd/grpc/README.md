# Extension protobuf contracts

The extension wire contracts have two public channels:

- `proto/azd/extensions/v1` is the stable contract. Its protobuf package is
  `azd.extensions.v1`, and its generated Go package is
  `pkg/azdext/contracts/v1`.
- `proto/azd/extensions/v1beta` is the long-lived beta contract. Its protobuf
  package is `azd.extensions.v1beta`, and its generated Go package is
  `pkg/azdext/contracts/v1beta`.

The beta channel initially mirrors stable. New additive contract fields and
methods can incubate in beta and graduate additively into stable after they
have been validated. Removing or renumbering fields, changing field types, and
reusing reserved names or numbers remain breaking changes in either channel.

This versioned hierarchy intentionally replaces the original unversioned
`azdext` protobuf package in a one-time breaking migration. The azd host does
not register the old `/azdext.*` endpoints.

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
Beta-only methods are generated with a focused beta override hook and an
`Unimplemented` fallback. Do not edit the generated adapter file directly.

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
methods are allowed, but shared field and method shapes must remain compatible
with the generated adapters.

The first versioned-contract change is intentionally incompatible with the
old unversioned package. Use its merged commit as the compatibility baseline
for later changes. For example, a later change can compare with:

```console
make proto-breaking \
  BUF_BREAKING_AGAINST='../../.git#branch=main,subdir=cli/azd/grpc'
```
