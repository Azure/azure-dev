# Microsoft-authored extension guidance

<!-- cspell:ignore exterrors userinfo -->

Use this guide together with the entire [CLI handbook](../AGENTS.md) and the
affected extension's local `AGENTS.md`, when present. Local guides describe
extension-specific contracts, ownership, and exceptions.

## Unit test authoring

When writing or reviewing Go unit tests for `azure.ai.agents`,
`azure.ai.connections`, `azure.ai.inspector`, `azure.ai.projects`,
`azure.ai.routines`, `azure.ai.skills`, or `azure.ai.toolboxes`, read and follow
the [unit-testing skill](.github/skills/azd-extension-unit-testing/SKILL.md).
This is extension-local guidance; clients may need this explicit link rather
than automatically discovering a nested skill directory.

## Security and safe operations

- Redact credentials from URLs before terminal output, logs, error messages,
  artifacts, or uploaded CI outputs. Clear `URL.User`, `RawQuery`, and
  `Fragment`; query strings may contain SAS tokens or `sig` parameters.
  `redactURL` in `cli/azd/pkg/azdext/pagination.go` and `redactURLForDebug` in
  `cli/azd/extensions/azure.ai.training/pkg/client` illustrate the shape, but
  also clear userinfo. Add a non-disclosure test with a credential-bearing URL.
- When accessing a `Subscription` from `PromptSubscription()`, use
  `Subscription.UserTenantId` (user access tenant), not `Subscription.TenantId`
  (resource tenant), for credential creation. They differ for multi-tenant and
  guest users; `LookupTenant()` returns the correct user access tenant.
- For destructive commands, verify the service API contract and local cleanup
  end-to-end: check the supported delete scope, handle empty successful delete
  responses, avoid redundant pre-checks, and clean up persisted state such as
  conversation or session IDs. Use the resource identity persisted by
  deployment, including custom or adopted names, and respect ownership.
- Validate locally checkable inputs and conflicts before authentication or
  mutations. Invalid inputs must leave project files and persisted environment
  values unchanged.

## Required PR review pass

Before general review, read and apply the
[extension user contract review skill](../../../.github/skills/extension-user-contract-review/SKILL.md)
for every PR that changes extension files, including fork PRs. Inspect every
changed extension file. For each distinct added or removed command, positional
parameter, flag, or YAML schema component, add an inline comment with
experiences spec guidance or match it to an existing thread. Deduplicate against
existing threads and track approved experiences specs per thread. Do not flag
behavior-only changes such as defaults, validation, prompts, output, or runtime
semantics. Before finishing, check that coverage and comments survived triage.
Report completion status and counts in the review summary or supported result
channel. If evidence or output capacity is unavailable, report `blocked`, not
"no changes".

## Core dependency changes

Use two PRs when an extension needs a new core change:

1. Land the core change in `cli/azd`.
2. Update the extension's azd dependency with
   `go get github.com/azure/azure-dev/cli/azd && go mod tidy`, then land the
   extension change.

For larger changes that build on each other, especially across core and
extension code, use
[stacked PRs](https://docs.github.com/en/pull-requests/how-tos/stacked-pull-requests)
to keep reviews small. Stacked PRs must be opened from `Azure/azure-dev`;
PRs from forks are not supported.

For local development, draft work, or joint validation before the core PR
merges, you may temporarily add this directive to the extension's `go.mod`:

```go
replace github.com/azure/azure-dev/cli/azd => ../../
```

It points to the local `cli/azd` checkout. Remove it before merging the
extension PR.

## Shared extension rules

- Follow the [extension style guide](../docs/extensions/extensions-style-guide.md).
  If work violates a principle, include a link to the guide so the user can
  understand the issue.
- Reject explicitly supplied flags that cannot take effect. Use
  `cmd.Flags().Changed("<flag>")` so explicit empty values are validated.
  Explicit selectors take precedence over persisted values and defaults. Name
  conflicting inputs in the error and use
  `exterrors.Validation(exterrors.CodeConflictingArguments, message, suggestion)`
  where available, or the extension's validation-error pattern otherwise.
- Keep output on an injected writer when a command handler or helper receives
  one, including debug and diagnostic paths; do not write directly to
  `os.Stdout` or `os.Stderr`. Extensions without an injected writer must follow
  their local `AGENTS.md` output conventions. See the CLI handbook's
  [writer guidance](../AGENTS.md#testing-best-practices).
- When behavior narrows, narrow help text and doc comments with it.
- Keep schema, decoding, request mapping, and runtime validation consistent for
  inline and `$ref` definitions. Reject unsupported fields rather than silently
  dropping authored configuration.
