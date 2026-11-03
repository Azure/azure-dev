applyTo:
  - cli/azd/extensions/**
---
- **Required PR review pass:** Before general review lenses, read and apply
  `.github/skills/extension-user-contract-review/SKILL.md` for every PR changing extension files, including fork PRs.
  Inspect every changed extension file, not just apparent command or schema files. Audit that each distinct added or
  removed command, positional parameter, flag, or YAML schema component becomes an inline comment with experiences
  spec guidance, or matches an existing thread. Do not flag behavior-only changes such as defaults, validation,
  prompts, output, or runtime semantics.
  Report completion status and counts; unavailable evidence or output capacity means `blocked`, not "no changes".

- When accessing a `Subscription` from `PromptSubscription()`, always use
  `Subscription.UserTenantId` (user access tenant) for credential creation,
  NOT `Subscription.TenantId` (resource tenant). For multi-tenant/guest users
  these differ, and using `TenantId` causes authentication failures.
  The `LookupTenant()` API already returns the correct user access tenant.

- When adding or reviewing destructive extension commands, verify the service
  API contract and local cleanup behavior end-to-end: confirm whether the API
  supports the requested delete scope, handle empty successful delete responses,
  avoid redundant pre-checks that later operations already cover, and clean up
  any persisted local state such as conversation or session IDs.
  Use the resource identity persisted by deployment, including custom/adopted names,
  and respect ownership when deciding what to delete.
  
- Follow extension guidelines in: cli/azd/docs/extensions/extensions-style-guide.md. If the work
  violates any of these principles, include a link to the guide so the user can read it and get
  ahead of some of the problems.

- **Reject explicitly supplied flags that cannot take effect.** Use `cmd.Flags().Changed("<flag>")`
  so explicit empty values are validated rather than treated as omitted. Explicit selectors take
  precedence over persisted values and defaults. Name conflicting inputs in the error and use
  `exterrors.Validation(exterrors.CodeConflictingArguments, message, suggestion)` where available,
  otherwise follow the extension's validation-error pattern.

- **Redact credentials from URLs before printing to terminal, logs, or error messages.** URLs may
  carry credentials in the userinfo component (`user:pass@host`) or in the query string (SAS
  tokens, `sig=` parameters). Clear `URL.User` and drop `RawQuery` and `Fragment` before passing a
  URL to any output function, error message, or log call. `redactURL` in
  `cli/azd/pkg/azdext/pagination.go` and `redactURLForDebug` in
  `cli/azd/extensions/azure.ai.training/pkg/client` show the shape, but both only strip the query
  and fragment, so clear the userinfo as well. This applies to download URLs, clone URLs, registry
  addresses, and any URL stored in artifacts or uploaded CI outputs. Add a non-disclosure test that
  feeds a credential-bearing URL and asserts no sensitive portion appears in the output.

- **Keep output on the injected writer when a command handler or helper receives one**; do not bypass
  it with direct writes to `os.Stdout` or `os.Stderr`, including in debug and diagnostic paths.
  Extensions that do not expose a writer must follow their local `AGENTS.md` output conventions.
  See "Keep terminal output on the injected writer" in `cli/azd/AGENTS.md`.

- When behavior narrows, narrow the help text and doc comments with it.

- Validate locally checkable inputs and conflicts before authentication or mutations. Invalid
  inputs must leave project files and persisted environment values unchanged.

- Keep schema, decoding, request mapping, and runtime validation consistent for inline and `$ref`
  definitions. Reject unsupported fields instead of silently dropping authored configuration.
