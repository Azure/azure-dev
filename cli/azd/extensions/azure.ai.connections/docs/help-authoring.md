<!-- cspell:ignore helpformat -->
# Foundry extension help

The connections, projects, routines, skills, and toolboxes command trees use the help conventions introduced
by `azd ai agent`: bold, underlined headings without trailing colons; highlighted
public command paths and flags; aligned live command and flag lists; and
copyable examples. The host-owned `azd ai --help` page and the already-merged
agent help implementation are unchanged.

## Ownership and generation

The canonical renderer and its regression tests live in
[`azure.ai.connections/internal/helpformat`](../internal/helpformat/helpformat.go).
This implementation derives from the agent formatter introduced in
[Azure/azure-dev#10057](https://github.com/Azure/azure-dev/pull/10057).
Projects, routines, skills, and toolboxes keep generated local copies.
Each extension imports its own package and builds against its existing SDK
dependency. No core source change, new SDK API, module release, local module
replacement, or Go workspace is required.

From `cli/azd/extensions/azure.ai.connections`, run:

```bash
go generate ./internal/helpformat
go test ./internal/helpformat/...
```

The generator has a fixed allowlist of those four sibling extensions. It never
discovers or writes agent or other extension files. The drift test checks all
four copies, including missing files. Edit the connections source, not generated
copies. Treat the agent formatter as a read-only reference: changes there do not
automatically update this implementation.

## Authoring help

Keep descriptions in plain Cobra `Short` and `Long` fields and examples in
`Example`. Standalone title-case headings ending in `:` are styled at render
time. Quote inline command references with single quotes or backticks. Use
`# ` captions above example commands, preserving quoted spaces, command order,
and multiline shell continuations.

Install inherited templates on the extension root with `Install(root, "azd ai",
footer)`. Keep rendering routed through `.UsageString` so the SDK's `UsageFunc`
can apply command-specific output defaults and allowed values. Do not replace
that wrapper. Preserve aliases, hidden flags and commands, and special help
content. Apply colors at render time and write through Cobra's injected writer.
Help must not require authentication or read project/environment state.

Root context sections must describe the extension's actual resolution rules.
Named azd environments and their persisted deployment values are not service
runtime environment variables. Do not copy another extension's variable names
or endpoint precedence without checking the implementation.

## Validation

From each independently released extension directory, use its published
dependencies:

```bash
GOWORK=off GOTOOLCHAIN=go1.26.4 go build ./...
GOWORK=off GOTOOLCHAIN=go1.26.4 go test ./internal/cmd ./internal/helpformat/... -short
```

To update help snapshots, set `UPDATE_SNAPSHOTS=true` and run
`go test ./internal/cmd -run '^TestAIHelp$'`.
Review every changed snapshot, then rerun with snapshot updates disabled.

Tests cover all public descendants, color/no-color output, SDK flag overrides,
metadata, aliases, hidden commands, examples, and injected output.
Manually inspect light/dark terminal output when appropriate.
