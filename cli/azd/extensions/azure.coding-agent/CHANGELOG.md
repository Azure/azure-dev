# Release History

## 0.6.1 (2026-03-13)

### Bugs Fixed

- [[#7078]](https://github.com/Azure/azure-dev/pull/7078) Fix credential resolution to use `UserTenantId` instead of `TenantId`, preventing `AADSTS70043`/`AADSTS700082` authentication errors for multi-tenant and guest users.
- [[#6966]](https://github.com/Azure/azure-dev/pull/6966) Update `go.opentelemetry.io/otel/sdk` to v1.40.0 to address CVE-2026-24051 (arbitrary code execution via PATH hijacking on macOS/Darwin systems).

### Other Changes

- [[#7031]](https://github.com/Azure/azure-dev/pull/7031) Update extension to Go 1.26 and add `golangci-lint` configuration.
- [[#7064]](https://github.com/Azure/azure-dev/pull/7064) Apply Go 1.26 code modernizations via `go fix` across the extension.

## 0.6.0

### Features Added

- Add metadata capability
- Support `AZD_EXT_DEBUG=true` for debugging

## 0.5.2

### Bugs fixed

- Managed identity creation code was running multiple times, instead of a single time. This should not have broken anything, as the call itself is idempotent but calling it repeatedly is unnecessary.
- `--managed-identity-name`, when specified, now assumes you're creating a credential, eliminating a question from the list.

## 0.5.1

### Bugs fixed

- Updated message, and help message, when prompting for the Azure subscription to be more descriptive.

## 0.5.0

### Bugs fixed

- Browser now launches properly on Windows

## 0.4.0

### Features Added

- Small improvements: underlining hyperlinks in the console, and improving error messages.

### Bugs fixed

- Do more prerequisite checks, like checking if any git remotes are registered, up front.

## 0.3.0

### Bugs fixed

- No longer require an azd project. You only need a GitHub repository.

## 0.2.0

### Features Added

- Use the existing remotes on the git repo when asking for the coding agent repository.

### Bugs Fixed

- Coding agent rule is scoped to the resource group that was created, and not the entire subscription.
- Pull request has a description on what the "next steps" are to enable MCPs for the GitHub repo.

## 0.1.0

### Features Added

- Initial Release
