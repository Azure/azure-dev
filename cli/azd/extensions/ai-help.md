<!-- cspell:ignore helpformat -->
# AI extension help

All first-party AI extension command trees use the help conventions introduced
by `azd ai agent`: bold, underlined headings without trailing colons; highlighted
public command paths and flags; aligned live command and flag lists; and
copyable examples. The host-owned `azd ai --help` page is not changed here.

## Ownership and generation

The canonical renderer and its regression tests live in
[`azure.ai.agents/internal/helpformat`](azure.ai.agents/internal/helpformat/helpformat.go).
The other 13 AI extensions, including the builder, keep generated local copies.
Each extension imports its own package and builds against its existing SDK
dependency. No core source change, new SDK API, module release, local module
replacement, or Go workspace is required.

From `cli/azd/extensions/azure.ai.agents`, run:

```bash
go generate ./internal/helpformat
go test ./internal/helpformat/...
```

The generator discovers AI namespaces from sibling extension manifests, skips
the canonical agent source, and writes only sibling `internal/helpformat`
packages. The drift test checks every copy, including missing files, and guards
the extension inventory. Edit the canonical source, not generated copies.

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
`go test ./internal/cmd -run '^(TestAIHelp|TestAgentHelp|TestAIHelpDisabled)$'`.
Review every changed snapshot, then rerun with snapshot updates disabled.
The builder belongs to the core Go module; from `cli/azd`, build and test
`./extensions/microsoft.azd.ai.builder/...`.

Tests cover all public descendants, color/no-color output, SDK flag overrides,
metadata, aliases, hidden commands, examples, and injected output.
Manually inspect light/dark terminal output when appropriate. Agent interactive
scenarios, including `tier0/0.02-help-root.yaml`, are opt-in via the
`foundry-extension-scenario-orchestrator`; they are not run automatically.
