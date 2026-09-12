# Inspect state and validate contracts

Run commands from `cli/azd`.

## Establish the current state

1. Inspect both source trees:

   ```bash
   find grpc/proto/azd/extensions/v1 -maxdepth 1 -name '*.proto' -printf '%f\n' | sort
   find grpc/proto/azd/extensions/v1beta -maxdepth 1 -name '*.proto' -printf '%f\n' | sort
   ```

2. Identify beta-only files and services. Today, `ComposeService`,
   `CopilotService`, and `TelemetryService` are beta-only. Verify the repository
   rather than assuming this list remains current.

3. For shared files, compare messages, fields, enum values, services, methods,
   and streaming shapes. Ignore expected package and `go_package` differences.

4. Inspect `internal/grpcserver/versioned_services.go` for native beta service
   registrations and configured overrides. Inspect the generated adapter file
   only to understand output; never edit it.

5. Summarize the delta in three groups:
   - Beta-only services and files.
   - Additive beta members on otherwise shared contracts.
   - Stable members missing or incompatible in beta. The last group is always
     an error.

## Fast validation

Run the current-tree invariant directly:

```bash
make proto-version-compatibility
```

This checks generated descriptors and reports the first incompatible symbol,
including missing fields or methods, changed field kinds/cardinality/presence,
oneof changes, enum renumbering, changed request or response types, and changed
streaming shape.

After editing source protos, regenerate before relying on descriptor tests:

```bash
go tool mage generateProtos
git diff --exit-code
```

`git diff --exit-code` is expected to fail while intentionally changed
generated files are not committed. Review those changes; do not discard them.
It must pass in CI after generated output is committed.

Run the remaining contract checks:

```bash
make proto-lint
make proto-version-compatibility
git fetch origin main
make proto-breaking \
  BUF_BREAKING_AGAINST='../../.git#branch=origin/main,subdir=cli/azd/grpc'
go test ./internal/grpcserver/... ./pkg/azdext/...
```

The checks answer different questions:

| Check | Protects |
|---|---|
| `proto-lint` | Proto structure and style |
| `proto-version-compatibility` | Current `v1` remains a compatible subset of current `v1beta` |
| `proto-breaking` | Neither channel breaks its previously merged contract |
| Generated diff | Checked-in bindings, facade aliases, adapters, and scaffolds match sources |
| Go tests | Registration, transcoding, overrides, status details, and SDK behavior |

Do not weaken a compatibility rule to make a proposed contract pass. Correct
the contract or explicitly design a new major stable version.
