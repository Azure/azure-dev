# Add functionality to v1beta

## Classify the addition

Determine which case applies before editing:

| Addition | Source change | Runtime implementation |
|---|---|---|
| New beta-only service | Add only under `v1beta` | Native `v1beta.<Service>Server` |
| New method on a shared service | Add only under `v1beta` | Focused beta override |
| New request field used by behavior | Add only under `v1beta` | Focused override for every method that consumes it |
| New response field | Add only under `v1beta` | Focused override when stable logic cannot populate it |
| New enum value interpreted by the host | Add only under `v1beta` | Focused override; stable code must not interpret unknown preview values |
| Shared compatible message used only for transport | Add only under `v1beta` | Generated transcoding may be sufficient |

Do not modify `v1` merely to make a preview feature easier to implement.

## Implement

1. Edit the canonical files under
   `grpc/proto/azd/extensions/v1beta`.
2. Preserve all existing field numbers, enum numbers, oneof membership,
   request/response types, and streaming shapes.
3. Regenerate with `go tool mage generateProtos`.
4. For a beta-only service:
   - Implement the generated `v1beta` server interface.
   - Add it to the server's service implementation map.
   - Do not add a stable adapter or stable facade aliases.
5. For beta-only behavior on a shared service:
   - Implement only the generated
     `Beta<Service><Method>Override` interfaces needed.
   - Register the value with `WithBetaServiceOverride`.
   - Do not embed `v1beta.Unimplemented<Service>Server` in the override.
6. Add tests proving:
   - The beta route is registered.
   - The new behavior receives the real beta request.
   - The stable route remains unchanged.
   - Calls without a required override fail with `codes.Unimplemented`.
7. Update SDK examples and the extension's `requiredAzdVersion` when the new
   API requires a newer host.

## Validate

Follow `inspect-and-validate.md`. In particular, run
`make proto-version-compatibility` after generation. A new beta API may be
additive, but it may not mutate the compatible shape inherited from `v1`.
