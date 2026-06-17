# azd Conventions

Reference file for azd-specific patterns and conventions. Load this file when reviewing PRs in the `Azure/azure-dev` or `Azure/azure-dev-pr` repos.

This is a skeleton -- it will be filled in over time as reviews surface recurring patterns.

## Project Structure

```
cli/azd/
├── cmd/          # Cobra command definitions
├── pkg/          # Public packages
├── internal/     # Internal packages
├── test/         # Integration tests and test utilities
```

TODO: Document key packages and their responsibilities.

## CLI Patterns

- Commands are registered using Cobra
- Flag naming conventions: TODO
- Help text conventions: TODO
- Output formatting: TODO (stdout for machine-readable, stderr for human messages)

## Go Patterns

- Error handling: TODO (wrapping conventions, sentinel errors)
- Context propagation: TODO
- Dependency injection: TODO
- Interface conventions: TODO

## Testing

- Unit tests: `go test ./...` from `cli/azd/`
- Integration tests: TODO
- Test helpers: TODO
- Mocking patterns: TODO

## CI/CD

- Required checks: TODO
- Branch policies: TODO
- Release process: TODO

## Common Review Findings

This section captures recurring patterns found during reviews. Add entries as patterns emerge.

TODO: Populate from actual review findings over time.
