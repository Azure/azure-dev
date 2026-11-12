# Graduate functionality from v1beta to v1

Graduation is an additive change to the existing stable major. Do not create
`v2` unless the desired contract cannot preserve `v1` compatibility.

## Before editing

1. Identify the complete beta capability: messages, fields, enums, methods,
   service definitions, error details, and SDK surface.
2. Confirm its wire shape is suitable for permanent support. Graduation freezes
   field numbers and compatible semantics in `v1`.
3. Decide whether all related beta functionality graduates together. Do not
   copy only part of a shape when the remainder is required for useful behavior.
4. For a beta-only service exposed through an existing Go convenience accessor,
   stop and decide how to preserve source compatibility. Changing an accessor's
   return type from `v1beta` to `v1` breaks callers even when the protobuf shape
   is wire-compatible.

## Graduate

1. Add the capability to the corresponding `v1` proto using the same names,
   field and enum numbers, types, cardinality, oneofs, request/response types,
   and streaming shape.
2. Keep the capability in `v1beta`. Existing beta clients must continue to use
   the beta route.
3. Move the default implementation into the stable service.
4. Remove a focused beta override only after the stable implementation provides
   equivalent behavior. Regeneration will make the beta adapter delegate to the
   stable implementation.
5. If an entire service graduates:
   - Register the stable implementation.
   - Let the generated adapter serve the existing beta route.
   - Regenerate the checked-in artifacts so generation creates the stable SDK
     facade aliases; never edit generated aliases directly.
   - Update the SDK documentation.
   - Preserve or deliberately migrate any existing beta client accessor.
6. Regenerate all checked-in artifacts with `go tool mage generateProtos`.
7. Update contract lifecycle docs, SDK references, examples, and minimum host
   versions.

## Record the graduation and consumer migration

Create a public `Azure/azure-dev` graduation audit issue for each graduation
batch. Record the target and actual release dates/version, complete capabilities,
implementation PR/commit, deprecation and retention decisions, validation, and
any explicitly approved SDK source breaks. Distinguish already-built binary
compatibility from recompilation against a new SDK.

Create a follow-up issue per graduated capability listing verified first-party
consumers, source/test changes, published SDK pin, minimum `requiredAzdVersion`,
and extension release checklist. Include a prompt invoking `/azd-grpc-contracts`
to migrate consumers to the already-graduated stable API without changing
contracts or removing retained beta routes. Do not publish SDK pins before the
target SDK version exists.

The first October 2026 batch targets azd 1.36.0. `Telemetry()` intentionally changes
to stable without adding `TelemetryBeta()`; deprecated beta wire routes remain
available. `AccountBeta()` is deprecated in favor of `Account()`. Future
source-breaking accessor changes still require an explicit decision.

The second October batch targets azd 1.37.0 for deployment preview:
[batch audit](https://github.com/Azure/azure-dev/issues/10625),
[consumer migration](https://github.com/Azure/azure-dev/issues/10626).
Keep deprecated beta preview Go helpers source-compatible and preserve their beta
route while adding the new stable helpers. The consumer PR #10571 stays draft
until the graduation merges and SDK v1.37.0 is published, then migrates preview
consumers and aligns all affected extension SDK pins/minimum hosts on 1.37.0.
Do not pin an unpublished version or merge local SDK replacements.

## Verify both client populations

Add tests proving:

- A stable client can call the graduated capability.
- An existing beta client can still call the same capability.
- Both routes reach equivalent business behavior.
- Beta-only members that did not graduate remain beta-only.
- Removing the old override did not discard beta-only request data still needed
  by another capability.

Run every command in `inspect-and-validate.md` before considering graduation
complete.
