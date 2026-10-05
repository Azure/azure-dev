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

- **Reject explicitly-set flags that cannot take effect; never silently drop them.** When a flag is
  explicitly supplied (check via `cmd.Flags().Changed("<flag>")`) but the selected code path ignores
  it — for example, `--inspector-port` with `--no-client`, or any agent-creation flag during a
  reuse flow, or init-only flags during a standalone-eject run — return a clear error that names the
  conflicting inputs. Automation scripts that pass explicit flags and receive a success exit code
  must be able to trust that those flags were honored. Treat an explicitly empty value such as
  `--environment=` or `--version=` as supplied input: validate and reject it rather than falling back
  to an implicit default. In extensions that provide `internal/exterrors`,
  report flag conflicts with `exterrors.Validation(exterrors.CodeConflictingArguments, message,
  suggestion)`. Otherwise, follow the extension's established validation-error pattern.

  _Source: #10106, #10140, #10181_

- **Validate the complete input set before any observable side effect.** Run project-wide and
  cross-field validation before authentication, prompts, filesystem writes, environment or local
  state updates, service calls, or other mutations. Validation must fail closed: do not continue on
  malformed, incomplete, unauthorized, or transiently unreadable input, and do not treat unknown
  data as valid. Add regression coverage that asserts invalid input leaves files, environment state,
  persisted state, and remote resources unchanged.

  _Source: #10137, #10140, #10148_

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
