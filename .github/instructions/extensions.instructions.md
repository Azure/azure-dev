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

- When generating text files (Dockerfiles, Python scripts, TOML files, etc.), never force Windows
  line endings (`\r\n`) on all platforms. Use `\n` (Unix LF) exclusively. Unconditional CRLF breaks
  shebang parsing inside Linux containers, causes `exec format error`, and introduces noisy git
  diffs on Linux/macOS. If the Go `text/template` or `os.WriteFile` path writes through a
  `strings.NewReplacer` or similar that converts `\n` → `\r\n`, remove that conversion.

  _Source: [#8782](https://github.com/Azure/azure-dev/pull/8782)_

- When adding URL validation as a security check, never rely on raw `startsWith()`, `strings.HasPrefix`,
  or `strings.Contains` checks on an un-normalized URL string. Dot-segment sequences (`/../`, `%2e%2e/`)
  can bypass prefix checks: `https://allowed-host/releases/../../../attacker/repo/x.zip` starts with
  the allowed prefix but resolves to a different host after normalization. Always parse the URL into its
  components (e.g. `new URL(str)` in JS, `url.Parse` in Go) and validate the normalized `Host` +
  `Path` separately. Apply this to artifact download URLs, registry source fields, and any
  allow-list checks on user-supplied URLs.

  _Source: [#9042](https://github.com/Azure/azure-dev/pull/9042)_
