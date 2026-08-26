# Extension contract versioning

The protobuf contract between azd core and extensions has stable and beta
channels. The channel is part of both the protobuf package name and every gRPC
full method name.

| Channel | Proto source | Wire package | Generated Go package |
|---|---|---|---|
| Stable | `grpc/proto/azd/extensions/v1` | `azd.extensions.v1` | `pkg/azdext/contracts/v1` |
| Beta | `grpc/proto/azd/extensions/v1beta` | `azd.extensions.v1beta` | `pkg/azdext/contracts/v1beta` |

The handwritten `pkg/azdext` package remains the public Go SDK facade for the
stable v1 channel. It forwards its generated contract types, clients, and
server interfaces from `pkg/azdext/contracts/v1`; protobuf-generated files do
not share the facade package with handwritten SDK functionality. Go clients
that intentionally target beta import `pkg/azdext/contracts/v1beta` directly.

## Channel policy

`v1` is the compatibility-protected stable contract. Changes must be additive:
add new fields with new numbers, add enum values where consumers tolerate
unknown values, or add new methods without changing existing request and
response semantics. Never reuse removed field names or numbers.

`v1beta` is a long-lived preview channel, not a sequence of short-lived
`v2beta1` packages. New additive contract capabilities can incubate there.
After validation, graduate a capability by adding the same compatible shape
to `v1`. A beta client continues to use the beta package until it deliberately
moves to stable.

The move from the original `azdext` wire package to
`azd.extensions.v1` is an intentionally accepted one-time breaking change.
azd does not register legacy `/azdext.*` runtime endpoints. Compatibility
checks should use the first merged versioned-contract commit as their baseline,
not the old unversioned sources.

## Host registration and adaptation

The azd gRPC host registers the generated `v1` and `v1beta` service
descriptors and handlers on the same server. The generated beta servers in
`internal/grpcserver/versioned_services_generated.go` satisfy the real
`v1beta` server interfaces; the host does not clone or rewrite stable
descriptors.

For a method shared by both channels, the beta server:

1. Calls a focused beta override when one is configured for that method.
2. Otherwise transcodes the beta request by protobuf wire data, calls the
   stable implementation with the same context, and transcodes the stable
   response back to beta.

This keeps stable business logic as the default implementation without
decoding a beta route directly into a stable message. Conversion failures are
returned with the service and method context. The same bridge supports unary
and bidirectional streaming methods.

Additive beta request fields are not exposed to the stable implementation.
The shared adapter discards fields unknown to the stable request type. A
preview field therefore must not be documented as functional until that
method has a beta override. An override implements one or more generated
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

An additive beta-only method does not require a matching stable method.
`make proto` generates a beta handler that calls a focused override when
present and otherwise returns `codes.Unimplemented`. Adding the method cannot
make host registration fail. Wire the override through the existing
`NewServer` options rather than adding another constructor dependency. After
the capability graduates to stable, the regenerated adapter automatically
uses stable business logic when no override is configured.

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

See [`grpc/README.md`](../../grpc/README.md) for generation, lint, and Buf
compatibility commands.
