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
  must be able to trust that those flags were honored. Resolve explicit resource IDs, environment
  names, versions, and other selectors before applying persisted environment values or defaults.
  In extensions that provide `internal/exterrors`,
  report flag conflicts with `exterrors.Validation(exterrors.CodeConflictingArguments, message,
  suggestion)`. Otherwise, follow the extension's established validation-error pattern.
  _Source: #9559, #9805, #9808_

- **Redact credentials from URLs before printing to terminal, logs, or error messages.** URLs may
  carry credentials in the userinfo component (`user:pass@host`) or in the query string (SAS
  tokens, `sig=` parameters). Clear `URL.User` and drop `RawQuery` and `Fragment` before passing a
  URL to any output function, error message, or log call. `redactURL` in
  `cli/azd/pkg/azdext/pagination.go` and `redactURLForDebug` in
  `cli/azd/extensions/azure.ai.training/pkg/client` show the shape, but both only strip the query
  and fragment, so clear the userinfo as well. This applies to download URLs, clone URLs, registry
  addresses, and any URL stored in artifacts or uploaded CI outputs. Add a non-disclosure test that
  feeds a credential-bearing URL and asserts no sensitive portion appears in the output.
  _Source: #9559, #9805, #9900_

- **Keep output on the injected writer when a command handler or helper receives one**; do not bypass
  it with direct writes to `os.Stdout` or `os.Stderr`, including in debug and diagnostic paths.
  Ensure warnings remain visible in non-interactive pipeline and pseudo-terminal paths, not only in
  an in-memory terminal renderer. Extensions that do not expose a writer must follow their local `AGENTS.md` output conventions.
  See "Keep terminal output on the injected writer" in `cli/azd/AGENTS.md`.
  _Source: #9805, #9929_

- **Keep authoring schemas, decoders, mapping, and runtime validation aligned.** Apply the same strict
  validation to inline and `$ref`-backed definitions, reject unknown or unsupported fields before
  deployment, and ensure compatibility forms normalize to the current wire contract. Never report
  success while silently dropping authored configuration. Cover each supported authoring form with
  schema and request-mapping tests.
  _Source: #9697, #9805, #9948_

- **Make multi-step project and environment mutations transactional or explicitly retryable.**
  Preflight validation, generated-name collisions, and remote prerequisites before the first write.
  If a later write fails, restore prior files and environment values; rollback I/O after cancellation
  must use a separate bounded context while preserving the original error. Do not clear persisted
  retry state until the corresponding local and remote updates have succeeded.
  _Source: #9559, #9719, #9805_

- **Test command behavior through the real wiring, not only isolated helpers.** Exercise persistence,
  HTTP failures, and fallback branches through the command or service entry point. Keep scenario
  prerequisites deterministic, bind tests to the intended source revision and resource capability,
  and fail visibly when scenario selection or required coverage is unavailable.
  _Source: #9342, #9901_

- When behavior narrows, narrow the help text and doc comments with it.
