# Shared Foundry module

```go
module github.com/azure/azure-dev/cli/shared/foundry
```

This independently versioned Go library is a build-time dependency for Foundry
extensions. It is not an azd extension or gRPC capability and has no registry
entry. Its `go.mod`, implementation, and transitive build graph must not require
or import `github.com/azure/azure-dev/cli/azd` or extension-internal packages.

## Packages

- **`access/`**: ARM-first Foundry project data-plane access preparation, role
  equivalence inspection (`Foundry User`, `Cognitive Services User`,
  `Foundry Project Manager`, `Foundry Owner`), general ARM role inspection and
  idempotent assignment (`HasAnyRoleAssignment`, `AssignRole`), deterministic
  UUIDv5 `RoleAssignmentName` generation, 15-second initial settling, bounded
  propagation/sync polling (`Ensure[T]`, `PollReadiness`), and sanitized write
  error remediation (`WriteError`).
- **`ferrors/`**: Module-owned typed errors (`*ferrors.Error`, `Category`,
  `CodeAuthFailed`) that extensions translate into their local `exterrors` or
  `azdext.LocalError` / `azdext.ServiceError` representation at the extension
  boundary.

## Versioning and release

The API starts at **v0.x**. Record breaking changes explicitly in release notes;
consumers pin exact released versions. Release independently from azd core and
extension binaries using `cli/shared/foundry/v0.x.y` tags. See
[RELEASING.md](RELEASING.md).

## Development

From `cli/shared/foundry`, independent of the core and extension workspaces:

```sh
GOWORK=off go mod download
GOWORK=off go build ./...
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1  # requires a supported CGO toolchain
GOWORK=off go vet ./...
python eng/check-boundaries.py
```
