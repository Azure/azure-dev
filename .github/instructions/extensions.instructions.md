applyTo:
  - cli/azd/extensions/**
---
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
  
- Follow extension guidelines in: cli/azd/docs/extensions/extensions-style-guide.md. If the work
  violates any of these principles, include a link to the guide so the user can read it and get
  ahead of some of the problems.

- **Reject explicitly supplied inputs that cannot take effect; never silently drop them.** For flags,
  check `cmd.Flags().Changed("<flag>")` so an explicit empty value is not mistaken for an omitted
  value. Also reject conflicting structured inputs, such as duplicate names with different version
  pins, before deduplication or defaulting. Return a clear error that names the conflicting inputs.
  Automation scripts that receive a success exit code must be able to trust that every explicit
  override was honored. In extensions that provide `internal/exterrors`, report flag conflicts with
  `exterrors.Validation(exterrors.CodeConflictingArguments, message, suggestion)`. Otherwise,
  follow the extension's established validation-error pattern.

  _Source: #10104, #10134, #10155_

- Incubate unvalidated extension contract capabilities in `v1beta`; do not add them to stable `v1`
  contracts or the root `azdext` facade until both host and extension implementations validate the
  API shape. Beta-only methods without an override return `codes.Unimplemented`, while shared
  methods continue delegating to stable behavior unless preview fields require a focused override.

  _Source: #10017, #10055_

- Permission and configuration diagnostics must evaluate the resources, scopes, identities, and
  effective permissions the service actually uses. Do not turn unrelated inherited roles or
  unbound connections into failures. When the available API cannot rule out a valid configuration,
  report an unknown or warning result rather than a confirmed failure, and cover supported scoped
  configurations plus unrelated-resource cases with regressions.

  _Source: #10127_

- In interactive-tester scenarios, move repeated setup or verification logic into parameterized
  fixture scripts. Keep pass/fail checks deterministic in hooks instead of asking the model to
  interpret shell commands or inspect files in a goal, and avoid rerunning mutating commands only
  to verify their first result.

  _Source: #10190_

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
