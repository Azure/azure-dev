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
   - Add stable SDK facade aliases and documentation.
   - Preserve or deliberately migrate any existing beta client accessor.
6. Regenerate all checked-in artifacts with `go tool mage generateProtos`.
7. Update contract lifecycle docs, SDK references, examples, and minimum host
   versions.

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
