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
  must be able to trust that those flags were honored. Use `exterrors.CodeConflictingArguments` for
  flag/flag conflicts and `exterrors.Validation` for semantic violations.

  _Source: [#9366](https://github.com/Azure/azure-dev/pull/9366), [#9404](https://github.com/Azure/azure-dev/pull/9404), [#9407](https://github.com/Azure/azure-dev/pull/9407), [#9422](https://github.com/Azure/azure-dev/pull/9422)_

- **Redact credentials from URLs before printing to terminal, logs, or error messages.** URLs may
  carry credentials in the userinfo component (`user:pass@host`) or in the query string (SAS
  tokens, `sig=` parameters). Clear `URL.User` and drop `RawQuery` and `Fragment` before passing a
  URL to any output function, error message, or log call. `redactURL` in
  `cli/azd/pkg/azdext/pagination.go` and `redactURLForDebug` in
  `cli/azd/extensions/azure.ai.training/pkg/client` show the shape, but both only strip the query
  and fragment, so clear the userinfo as well. This applies to download URLs, clone URLs, registry
  addresses, and any URL stored in artifacts or uploaded CI outputs. Add a non-disclosure test that
  feeds a credential-bearing URL and asserts no sensitive portion appears in the output.

  _Source: [#9361](https://github.com/Azure/azure-dev/pull/9361), [#9415](https://github.com/Azure/azure-dev/pull/9415), [#9417](https://github.com/Azure/azure-dev/pull/9417)_

- **Route every user-visible message through the caller's `io.Writer`**; never write directly to
  `os.Stdout` or `os.Stderr` inside extension command handlers or their helpers, including debug
  and diagnostic paths. See "Keep terminal output on the injected writer" in `cli/azd/AGENTS.md`.

  _Source: [#9291](https://github.com/Azure/azure-dev/pull/9291), [#9366](https://github.com/Azure/azure-dev/pull/9366)_

- **When behavior narrows, narrow the help text and doc comments with it.** A comment that still
  says a function "runs eject" after it was restricted to projects that already declare a Foundry
  service is a recurring review finding. See "Help text consistency" in `cli/azd/AGENTS.md`.

  _Source: [#9329](https://github.com/Azure/azure-dev/pull/9329), [#9407](https://github.com/Azure/azure-dev/pull/9407), [#9422](https://github.com/Azure/azure-dev/pull/9422)_
