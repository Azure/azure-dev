# Changelog Audit Report

**Generated**: 2026-04-11 14:54 UTC
**Repo SHA**: fa704e4c0
**Releases audited**: 20
**Rules applied**: F1 (dual PR numbers), F2 (PR link validation), F3 (cross-release dedup), F4 (alpha/beta gating), F5 (borderline inclusion), F6 (phantom entries)

## Summary

| Release | Entries | Commits | Errors | Warnings | Info |
|---------|---------|---------|--------|----------|------|
| 1.23.15 | 14 | 33 | 0 | 0 | 0 |
| 1.23.14 | 18 | 33 | 0 | 2 | 1 |
| 1.23.13 | 15 | 13 | 6 | 3 | 0 |
| 1.23.12 | 4 | 10 | 0 | 1 | 0 |
| 1.23.11 | 11 | 24 | 0 | 1 | 0 |
| 1.23.10 | 2 | 7 | 0 | 0 | 0 |
| 1.23.9 | 14 | 25 | 3 | 0 | 0 |
| 1.23.8 | 18 | 32 | 0 | 1 | 0 |
| 1.23.7 | 19 | 30 | 0 | 1 | 0 |
| 1.23.6 | 9 | 15 | 0 | 1 | 0 |
| 1.23.5 | 4 | 9 | 0 | 0 | 0 |
| 1.23.4 | 8 | 16 | 0 | 1 | 0 |
| 1.23.3 | 2 | 7 | 0 | 2 | 0 |
| 1.23.2 | 4 | 3 | 0 | 1 | 0 |
| 1.23.1 | 7 | 18 | 0 | 2 | 1 |
| 1.23.0 | 19 | 34 | 0 | 2 | 0 |
| 1.22.5 | 2 | 4 | 0 | 0 | 0 |
| 1.22.4 | 5 | 7 | 0 | 0 | 0 |
| 1.22.3 | 2 | 4 | 0 | 0 | 0 |
| 1.22.2 | 4 | 0 | 0 | 0 | 0 |
| **Total** | | | **9** | **18** | **2** |

## Findings by Rule

| Rule | Description | Count |
|------|-------------|-------|
| F1 | Dual PR number extraction | 1 |
| F2 | Missing PR link on entry | 9 |
| F2b | Issue link instead of PR link | 1 |
| F3b | Intra-release duplicate | 1 |
| F4 | Alpha/beta feature gating | 2 |
| F5 | Borderline excluded commit | 9 |
| F6 | Phantom entry (PR not in range) | 6 |

## Per-Release Detail

### 1.23.15 (2026-04-10)

**Commit range**: `azure-dev-cli_1.23.14..azure-dev-cli_1.23.15` (33 commits, 14 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.23.14 (2026-04-03)

**Commit range**: `azure-dev-cli_1.23.13..azure-dev-cli_1.23.14` (33 commits, 18 changelog entries)
**Findings**: 0 errors, 2 warnings, 1 info

#### F4: Alpha/beta feature gating

- [INFO] PR #7489 mentions alpha/feature-flag in subject. Verify gating decision.
  > `move update from alpha to beta (#7489)`

#### F6: Phantom entry (PR not in range)

- [WARN] PR #7343 in changelog but not found in commit range azure-dev-cli_1.23.13..azure-dev-cli_1.23.14 (phantom entry).
  > `- [[#7343]](https://github.com/Azure/azure-dev/pull/7343) Fix nil pointer panic when `azure.yaml` contains services, ...`
- [WARN] PR #7299 in changelog but not found in commit range azure-dev-cli_1.23.13..azure-dev-cli_1.23.14 (phantom entry).
  > `- [[#7299]](https://github.com/Azure/azure-dev/pull/7299) Add command-specific telemetry attributes for `auth login`,...`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.14 (2026-04-03)
  
  ### Features Added
  
  - [[#7489]](https://github.com/Azure/azure-dev/pull/7489) Promote `azd update` to public preview; the command no longer requires enabling an alpha feature flag and displays a preview notice on first use.
  - [[#7382]](https://github.com/Azure/azure-dev/pull/7382) Add per-layer hook support in `azure.yaml`; hooks defined under `infra.layers[].hooks` now execute during `azd provision`, and `azd hooks run` supports a new `--layer` flag for targeted execution.
  - [[#7392]](https://github.com/Azure/azure-dev/pull/7392) Add `--non-interactive` as a global flag alias for `--no-prompt` and support the `AZD_NON_INTERACTIVE` environment variable for enabling non-interactive mode. Thanks @spboyer for the contribution!
  - [[#7361]](https://github.com/Azure/azure-dev/pull/7361) Add `docker.network` option to `azure.yaml` service configuration, passing `--network` to `docker build` for services that require host networking (e.g., behind corporate proxies). Thanks @spboyer for the contribution!
  - [[#7384]](https://github.com/Azure/azure-dev/pull/7384) `azd auth token` now prints the raw access token by default; use `--output json` for structured output including expiration metadata.
  - [[#7296]](https://github.com/Azure/azure-dev/pull/7296) `azd pipeline config` no longer prompts for parameters that are outputs of earlier provisioning layers, reducing unnecessary user prompts in multi-layer deployments.
  - [[#7401]](https://github.com/Azure/azure-dev/pull/7401) Add a "Fix this error" option to the Copilot-assisted error troubleshooting flow, allowing the agent to directly apply a fix and collect user feedback.
  - [[#7397]](https://github.com/Azure/azure-dev/pull/7397) Add `allowed_locations` filter to the `PromptLocation` extension framework API and improve AI model capacity resolution to fall back to the highest valid capacity within remaining quota.
  - [[#7043]](https://github.com/Azure/azure-dev/pull/7043) Add Key Vault secret resolver to the extension framework, automatically resolving `@Microsoft.KeyVault(...)` references in environment variables before passing them to extensions.
  
  ### Breaking Changes
  
  ### Bugs Fixed
  
  - [[#7314]](https://github.com/Azure/azure-dev/pull/7314) Fix environment variable leak and broken `--debug`, `--cwd`, and `-e`/`--environment` flag propagation to extension commands.
- - [[#7343]](https://github.com/Azure/azure-dev/pull/7343) Fix nil pointer panic when `azure.yaml` contains services, resources, or hooks with empty definitions; reports all issues in a single actionable error message.  ← F6: remove phantom entry
  - [[#7356]](https://github.com/Azure/azure-dev/pull/7356) Fix panic when `azd auth token` is called with an unsupported `--output` format.
  - [[#7417]](https://github.com/Azure/azure-dev/pull/7417) Improve `azd update` error message when the installation is managed by an administrator, with guidance to suppress update notifications via `AZD_SKIP_UPDATE_CHECK=1`.
  - [[#7298]](https://github.com/Azure/azure-dev/pull/7298) Add code-signing verification for Windows MSI installs performed via `azd update`.
  - [[#7362]](https://github.com/Azure/azure-dev/pull/7362) Remove unsafe global `os.Chdir` call from Aspire server initialization that could cause concurrency issues in concurrent operations. Thanks @spboyer for the contribution!
  
  ### Other Changes
  
  - [[#7456]](https://github.com/Azure/azure-dev/pull/7456) Update bundled GitHub CLI to v2.89.0.
- - [[#7299]](https://github.com/Azure/azure-dev/pull/7299) Add command-specific telemetry attributes for `auth login`, `env list`, `hooks run`, `pipeline config`, and `infra generate` commands.  ← F6: remove phantom entry
  - [[#7396]](https://github.com/Azure/azure-dev/pull/7396) Add telemetry instrumentation for preflight validation with unique rule and diagnostic IDs, tracking outcomes and warning and error counts per run.
  
```

### 1.23.13 (2026-03-26)

**Commit range**: `azure-dev-cli_1.23.12..azure-dev-cli_1.23.13` (13 commits, 15 changelog entries)
**Findings**: 6 errors, 3 warnings, 0 info

#### F2: Missing PR link on entry

- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add `ConfigHelper` for typed, ergonomic access to azd user and environment configuration through gRPC services, wit...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add `Pager[T]` generic pagination helper with SSRF-safe nextLink validation, `Collect` with `MaxPages`/`MaxItems` b...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add `ResilientClient` hardening: exponential backoff with jitter, upfront body seekability validation, and `Retry-A...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add `SSRFGuard` standalone SSRF protection with metadata endpoint blocking, private network blocking, HTTPS enforce...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add atomic file operations (`WriteFileAtomic`, `CopyFileAtomic`, `BackupFile`, `EnsureDir`) with crash-safe write-t...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add runtime process utilities for cross-platform process management, tool discovery, and shell execution helpers.`

#### F2b: Issue link instead of PR link

- [WARN] Entry uses issue link (#2743) instead of PR link. Use /pull/ not /issues/.
  > `- [[#2743]](https://github.com/Azure/azure-dev/issues/2743) Support deploying Container App Jobs (`Microsoft.App/jobs...`

#### F6: Phantom entry (PR not in range)

- [WARN] PR #2743 in changelog but not found in commit range azure-dev-cli_1.23.12..azure-dev-cli_1.23.13 (phantom entry).
  > `- [[#2743]](https://github.com/Azure/azure-dev/issues/2743) Support deploying Container App Jobs (`Microsoft.App/jobs...`
- [WARN] PR #7330 in changelog but not found in commit range azure-dev-cli_1.23.12..azure-dev-cli_1.23.13 (phantom entry).
  > `- [[#7330]](https://github.com/Azure/azure-dev/pull/7330) Add `azure.yaml` schema metadata to enable automatic schema...`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.13 (2026-03-26)
  
  ### Features Added
  
  - [[#7247]](https://github.com/Azure/azure-dev/pull/7247) Add actionable suggestion to set `remoteBuild: true` for Container Apps and AKS services when Docker is not installed or not running. Thanks @spboyer for the contribution!
  - [[#7236]](https://github.com/Azure/azure-dev/pull/7236) Improve `azd auth status --output json` to exit non-zero when unauthenticated and include an `expiresOn` field, making it suitable as an auth validation endpoint for AI agents. Thanks @spboyer for the contribution!
- - [[#2743]](https://github.com/Azure/azure-dev/issues/2743) Support deploying Container App Jobs (`Microsoft.App/jobs`) via `host: containerapp`. The Bicep template determines whether the target is a Container App or Container App Job.  ← F6: remove phantom entry
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add `ConfigHelper` for typed, ergonomic access to azd user and environment configuration through gRPC services, with validation support, shallow/deep merge, and structured error types (`ConfigError`).
- - Add `ConfigHelper` for typed, ergonomic access to azd user and environment configuration through gRPC services, with validation support, shallow/deep merge, and structured error types (`ConfigError`).  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add `Pager[T]` generic pagination helper with SSRF-safe nextLink validation, `Collect` with `MaxPages`/`MaxItems` bounds, and `Truncated()` detection for callers.
- - Add `Pager[T]` generic pagination helper with SSRF-safe nextLink validation, `Collect` with `MaxPages`/`MaxItems` bounds, and `Truncated()` detection for callers.  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add `ResilientClient` hardening: exponential backoff with jitter, upfront body seekability validation, and `Retry-After` header cap at 120 s.
- - Add `ResilientClient` hardening: exponential backoff with jitter, upfront body seekability validation, and `Retry-After` header cap at 120 s.  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add `SSRFGuard` standalone SSRF protection with metadata endpoint blocking, private network blocking, HTTPS enforcement, DNS fail-closed, IPv6 embedding extraction, and allowlist bypass.
- - Add `SSRFGuard` standalone SSRF protection with metadata endpoint blocking, private network blocking, HTTPS enforcement, DNS fail-closed, IPv6 embedding extraction, and allowlist bypass.  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add atomic file operations (`WriteFileAtomic`, `CopyFileAtomic`, `BackupFile`, `EnsureDir`) with crash-safe write-temp-rename pattern.
- - Add atomic file operations (`WriteFileAtomic`, `CopyFileAtomic`, `BackupFile`, `EnsureDir`) with crash-safe write-temp-rename pattern.  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add runtime process utilities for cross-platform process management, tool discovery, and shell execution helpers.
- - Add runtime process utilities for cross-platform process management, tool discovery, and shell execution helpers.  ← F2: add missing PR link
  
  ### Breaking Changes
  
  ### Bugs Fixed
  
  - [[#7329]](https://github.com/Azure/azure-dev/pull/7329) Fix nil panic and incorrect workflow continuation when user declines preflight validation warnings; `azd provision` and `azd up` now exit cleanly with exit code 0.
  - [[#7346]](https://github.com/Azure/azure-dev/pull/7346) Fix extension startup failures on Windows caused by IPv4/IPv6 address mismatch in the gRPC server address, and increase extension startup timeout from 5s to 15s. Thanks @spboyer for the contribution!
  - [[#7311]](https://github.com/Azure/azure-dev/pull/7311) Fix `.funcignore` parsing failures caused by UTF-8 BOM and incorrect negation pattern handling in zip packaging. Thanks @jongio for the contribution!
  - [[#7250]](https://github.com/Azure/azure-dev/pull/7250) Add targeted error suggestions for common Container Apps and ARM deployment failures including `ContainerAppOperationError`, `InvalidTemplateDeployment`, `RoleAssignmentExists`, and `InvalidResourceGroupLocation`. Thanks @spboyer for the contribution!
  ### Other Changes
  
  - [[#7235]](https://github.com/Azure/azure-dev/pull/7235) Fix auth error telemetry classification to properly categorize `login_required`, `not_logged_in`, and authentication failures under the `aad` service name. Thanks @spboyer for the contribution!
- - [[#7330]](https://github.com/Azure/azure-dev/pull/7330) Add `azure.yaml` schema metadata to enable automatic schema association in JetBrains IDEs, Neovim, and other editors via the SchemaStore catalog.  ← F6: remove phantom entry
  
```

### 1.23.12 (2026-03-24)

**Commit range**: `azure-dev-cli_1.23.11..azure-dev-cli_1.23.12` (10 commits, 4 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F1: Dual PR number extraction

- [WARN] Dual PR numbers detected: commit references #7223 and #7291. Changelog uses #7223 but should use #7291 (last = canonical).
  > `Add funcignore handling fix (#7223) to 1.23.12 changelog (#7291)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.12 (2026-03-24)
  
  ### Bugs Fixed
  
- - [[#7223]](https://github.com/Azure/azure-dev/pull/7223) Improve `.funcignore` handling for flex-consumption function apps by inferring `remoteBuild` from file contents and failing fast on incompatible configurations.  ← F1: use canonical PR #7291
+ - [[#7291]](https://github.com/Azure/azure-dev/pull/7291) Improve `.funcignore` handling for flex-consumption function apps by inferring `remoteBuild` from file contents and failing fast on incompatible configurations.
  - [[#7274]](https://github.com/Azure/azure-dev/pull/7274) Revert env-flag change from v1.23.11 to fix regression where the `-e` shorthand for `--environment` conflicted with extension commands that use `-e` for their own flags (e.g., `--project-endpoint` in `azure.ai.models` and `azure.ai.finetune`), restoring compatibility with those extensions.
  
  ### Other Changes
  
  - [[#7241]](https://github.com/Azure/azure-dev/pull/7241) Improve telemetry error classification by routing MCP tool, Copilot agent, and container/extension error spans through `MapError` to reduce unclassified error entries. Thanks @spboyer for the contribution!
  - [[#7253]](https://github.com/Azure/azure-dev/pull/7253) Fix `copilot.session.id` telemetry field classification to use the correct PII category.
  
```

### 1.23.11 (2026-03-20)

**Commit range**: `azure-dev-cli_1.23.10..azure-dev-cli_1.23.11` (24 commits, 11 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #7213 matches borderline keyword "ux". New rules recommend including it.
  > `implement install script and brew cask for linux/macos (#7213)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.11 (2026-03-20)
  
  ### Features Added
  
  - [[#7045]](https://github.com/Azure/azure-dev/pull/7045) Add `--timeout` flag to `azd deploy` and a `deployTimeout` service configuration field in `azure.yaml` for user-configurable deployment timeouts (CLI flag > `azure.yaml` > default 1200 seconds). Thanks @spboyer for the contribution!
  - [[#7162]](https://github.com/Azure/azure-dev/pull/7162) Add git dirty working directory check when running `azd init` with "Set up with GitHub Copilot (Preview)", prompting for confirmation before modifying uncommitted changes, and add upfront MCP server tool consent prompt.
  - [[#7172]](https://github.com/Azure/azure-dev/pull/7172) Add `CopilotService` gRPC service to the extension framework, enabling extensions to programmatically interact with GitHub Copilot agent capabilities (initialize sessions, send messages, retrieve usage metrics and file changes).
  - [[#7194]](https://github.com/Azure/azure-dev/pull/7194) Rebrand `azd init` agent experience to "Set up with GitHub Copilot (Preview)" with improved prompt quality and system message guidance.
  - [[#7216]](https://github.com/Azure/azure-dev/pull/7216) Improve AI-assisted error troubleshooting with a multi-step flow: select a troubleshooting category (explain, guidance, troubleshoot, or skip), optionally allow the agent to apply a fix, and retry the failed command.
  
  ### Bugs Fixed
  
  - [[#7035]](https://github.com/Azure/azure-dev/pull/7035) Fix default environment variables leaking into extension processes when a different environment is specified with `-e`.
  - [[#7171]](https://github.com/Azure/azure-dev/pull/7171) Fix "context cancelled" errors when retrying `azd` commands after GitHub Copilot agent troubleshooting, by rebuilding the cobra command tree on each workflow re-execution.
  - [[#7174]](https://github.com/Azure/azure-dev/pull/7174) Fix preflight role assignment permission check producing false-positive warnings for B2B/guest users by resolving the principal ID against the resource tenant, and fix per-role RBAC evaluation to match Azure semantics.
  - [[#7175]](https://github.com/Azure/azure-dev/pull/7175) Fix security issues: path traversal in extensions and templates, WebSocket origin validation, JWT hardening, and sensitive data redaction. Thanks @jongio for the contribution!
  - [[#7193]](https://github.com/Azure/azure-dev/pull/7193) Fix extension `PromptSubscription` not masking subscription IDs when `AZD_DEMO_MODE` is enabled.
  
  ### Other Changes
  
  - [[#7199]](https://github.com/Azure/azure-dev/pull/7199) Add telemetry instrumentation for Copilot agent flows, including session, initialization, usage metrics, and consent fields.
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#7213]](https://github.com/Azure/azure-dev/pull/7213) implement install script and brew cask for linux/macos
```

### 1.23.10 (2026-03-16)

**Commit range**: `azure-dev-cli_1.23.9..azure-dev-cli_1.23.10` (7 commits, 2 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.23.9 (2026-03-13)

**Commit range**: `azure-dev-cli_1.23.8..azure-dev-cli_1.23.9` (25 commits, 14 changelog entries)
**Findings**: 3 errors, 0 warnings, 0 info

#### F2: Missing PR link on entry

- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add Extension SDK Reference documentation covering `NewExtensionRootCommand`, `MCPServerBuilder`, `ToolArgs`, `MCPS...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add Extension Migration Guide with before/after examples for migrating from legacy patterns to SDK helpers. See [Ex...`
- [ERROR] Entry is missing a [[#PR]] link.
  > `- Add Extension End-to-End Walkthrough demonstrating root command setup, MCP server construction, lifecycle event han...`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.9 (2026-03-13)
  
  ### Features Added
  
  - [[#7041]](https://github.com/Azure/azure-dev/pull/7041) Automatically fall back to a local Docker/Podman build when `remoteBuild: true` and the remote ACR build fails. Thanks @spboyer for the contribution!
  - [[#7053]](https://github.com/Azure/azure-dev/pull/7053) Add local preflight validation before Bicep deployment to detect parameter and configuration issues before submitting the deployment to ARM.
  - [[#7018]](https://github.com/Azure/azure-dev/pull/7018) Improve extension startup failure warnings with categorized, actionable messages distinguishing extensions needing an upgrade from timeout failures, and include a `--debug` hint for details.
  
  ### Bugs Fixed
  
  - [[#7040]](https://github.com/Azure/azure-dev/pull/7040) Fix `azd pipeline config` failing when the GitHub repository name contains dots (e.g., `my-org/my.app`) because dots were not sanitized out of federated identity credential names. Thanks @spboyer for the contribution!
  - [[#7046]](https://github.com/Azure/azure-dev/pull/7046) Fix error message when no Azure subscriptions are found to include actionable guidance for multi-tenant and MFA scenarios, suggesting `azd auth login --tenant-id`.
  - [[#7047]](https://github.com/Azure/azure-dev/pull/7047) Fix progress log previewer outputting blank lines on start and not respecting no-tty mode.
  - [[#7062]](https://github.com/Azure/azure-dev/pull/7062) Fix `azd deploy` silently removing externally-configured Dapr settings when performing a Container App update; Dapr configuration is now preserved when not present in the deployment YAML.
  - [[#7072]](https://github.com/Azure/azure-dev/pull/7072) Fix remote state blob client not falling back to the default subscription from user config when `state.remote.config.subscriptionId` is not explicitly set.
  - [[#7076]](https://github.com/Azure/azure-dev/pull/7076) Fix race condition in `ux.TaskList` when concurrently accessing the completed task count. Thanks @richardpark-msft for the contribution!
  
  ### Other Changes
  
  - [[#7044]](https://github.com/Azure/azure-dev/pull/7044) Improve `--no-prompt` support for resource-group deployments by defaulting the resource group prompt to the `AZURE_RESOURCE_GROUP` environment variable value.
  - [[#7051]](https://github.com/Azure/azure-dev/pull/7051) Improve telemetry error classification with typed sentinel errors, replacing opaque `errors_errorString` result codes with descriptive error type codes across command domains.
- - Add Extension SDK Reference documentation covering `NewExtensionRootCommand`, `MCPServerBuilder`, `ToolArgs`, `MCPSecurityPolicy`, `BaseServiceTargetProvider`, and all SDK helpers introduced in [#6856](https://github.com/Azure/azure-dev/pull/6856). See [Extension SDK Reference](docs/extensions/extension-sdk-reference.md).  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add Extension SDK Reference documentation covering `NewExtensionRootCommand`, `MCPServerBuilder`, `ToolArgs`, `MCPSecurityPolicy`, `BaseServiceTargetProvider`, and all SDK helpers introduced in [#6856](https://github.com/Azure/azure-dev/pull/6856). See [Extension SDK Reference](docs/extensions/extension-sdk-reference.md).
- - Add Extension Migration Guide with before/after examples for migrating from legacy patterns to SDK helpers. See [Extension Migration Guide](docs/extensions/extension-migration-guide.md).  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add Extension Migration Guide with before/after examples for migrating from legacy patterns to SDK helpers. See [Extension Migration Guide](docs/extensions/extension-migration-guide.md).
- - Add Extension End-to-End Walkthrough demonstrating root command setup, MCP server construction, lifecycle event handlers, and security policy usage. See [Extension End-to-End Walkthrough](docs/extensions/extension-e2e-walkthrough.md).  ← F2: add missing PR link
+ - [[#???]](https://github.com/Azure/azure-dev/pull/???) Add Extension End-to-End Walkthrough demonstrating root command setup, MCP server construction, lifecycle event handlers, and security policy usage. See [Extension End-to-End Walkthrough](docs/extensions/extension-e2e-walkthrough.md).
  
```

### 1.23.8 (2026-03-06)

**Commit range**: `azure-dev-cli_1.23.7..azure-dev-cli_1.23.8` (32 commits, 18 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6939 matches borderline keyword "output". New rules recommend including it.
  > `Silence extension process stderr logs to prevent call-stack-like error output (#6939)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.8 (2026-03-06)
  
  ### Features Added
  
  - [[#7001]](https://github.com/Azure/azure-dev/pull/7001) Add support for deploying Container App Jobs (`Microsoft.App/jobs`) via `host: containerapp`. The Bicep template determines whether the target is a Container App or Container App Job. Thanks @jongio for the contribution!
  - [[#6968]](https://github.com/Azure/azure-dev/pull/6968) Add `Microsoft.App/agents` (SRE Agent) resource type recognition so provisioning progress output correctly displays SRE Agent resources. Thanks @dm-chelupati for the contribution!
  - [[#7016]](https://github.com/Azure/azure-dev/pull/7016) Add sensible defaults for `azd env new` and `azd init` in `--no-prompt` mode: auto-generate environment name from the working directory, auto-select subscription when only one is available, and remove the hard `--environment` requirement. Thanks @spboyer for the contribution!
  - [[#6962]](https://github.com/Azure/azure-dev/pull/6962) Improve `--no-prompt` error guidance for `azd init` and `azd provision` to report all missing inputs at once with actionable resolution commands and environment variable mappings.
  
  ### Bugs Fixed
  
  - [[#6790]](https://github.com/Azure/azure-dev/pull/6790) Fix the azd user-agent string not flowing to authentication HTTP calls (Azure Identity SDK and MSAL), making azd-originated auth traffic identifiable in Azure telemetry. Thanks @spboyer for the contribution!
  - [[#6920]](https://github.com/Azure/azure-dev/pull/6920) Fix `Retry-After` header not being applied correctly in Azure Functions flex consumption deployment polling, and improve cancellation responsiveness in Static Web Apps deployment verification. Thanks @spboyer for the contribution!
  - [[#6922]](https://github.com/Azure/azure-dev/pull/6922) Fix Ctrl+C cancellation not being respected during remote ACR build source upload and log streaming. Thanks @spboyer for the contribution!
  - [[#6914]](https://github.com/Azure/azure-dev/pull/6914) Fix `azd extension install`, `show`, and `upgrade` potentially selecting the wrong version when the registry returns versions in descending order.
  
  ### Other Changes
  
  - [[#7019]](https://github.com/Azure/azure-dev/pull/7019) Improve provisioning progress polling with concurrent nested deployment traversal and a terminal-operation cache to reduce redundant ARM API calls and decrease spinner flicker.
  - [[#7017]](https://github.com/Azure/azure-dev/pull/7017) Update azd core to Go 1.26.
  - [[#7004]](https://github.com/Azure/azure-dev/pull/7004) Improve provisioning completion responsiveness by replacing channel-based cancellation with context cancellation in the progress display goroutine.
  - [[#6977]](https://github.com/Azure/azure-dev/pull/6977) Improve AI-assisted error troubleshooting by categorizing errors (Azure, machine, or user context) and tailoring automated fix suggestions to appropriate error types.
  - [[#6978]](https://github.com/Azure/azure-dev/pull/6978) Improve auth error classification in the extension gRPC server so extensions receive `Unauthenticated` status codes instead of `Unknown` for login-required errors.
  - [[#6963]](https://github.com/Azure/azure-dev/pull/6963) Improve provisioning performance by caching resource type display name lookups to reduce redundant API calls during progress polling.
  - [[#6954]](https://github.com/Azure/azure-dev/pull/6954) Add extension SDK primitives for token provider, scope detection, resilient HTTP client, and pagination to simplify azd extension authoring. Thanks @jongio for the contribution!
  - [[#6953]](https://github.com/Azure/azure-dev/pull/6953) Update Bicep minimum required version to 0.41.2.
  - [[#6941]](https://github.com/Azure/azure-dev/pull/6941) Simplify AI-assisted error troubleshooting to a two-step flow: explain the error, then optionally generate step-by-step fix guidance.
  - [[#6912]](https://github.com/Azure/azure-dev/pull/6912) Improve storage blob client performance by verifying container existence only once per session instead of on every operation. Thanks @spboyer for the contribution!
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6939]](https://github.com/Azure/azure-dev/pull/6939) Silence extension process stderr logs to prevent call-stack-like error output
```

### 1.23.7 (2026-02-27)

**Commit range**: `azure-dev-cli_1.23.6..azure-dev-cli_1.23.7` (30 commits, 19 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6859 matches borderline keyword "prompt". New rules recommend including it.
  > `Add `default_value` to AI prompt request messages (#6859)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.7 (2026-02-27)
  
  ### Features Added
  
  - [[#6826]](https://github.com/Azure/azure-dev/pull/6826) Add local filesystem directory support for `azd init --template` to enable iterating on templates without pushing to a remote repository. Thanks @jongio for the contribution!
  - [[#6827]](https://github.com/Azure/azure-dev/pull/6827) Add YAML-driven error handling pipeline that matches Azure deployment errors against known patterns and surfaces actionable messages, suggestions, and reference links.
  - [[#6848]](https://github.com/Azure/azure-dev/pull/6848) Add `pyproject.toml` detection and `pip install .` support for Python projects using modern project packaging. Thanks @spboyer for the contribution!
  - [[#6852]](https://github.com/Azure/azure-dev/pull/6852) Add `provision.preflight` config option to skip ARM preflight validation (`azd config set provision.preflight off`) and show a spinner during preflight runs.
  - [[#6856]](https://github.com/Azure/azure-dev/pull/6856) Add Extension SDK helpers for command scaffolding, MCP server utilities, typed argument parsing, and SSRF security policy to simplify azd extension authoring. Thanks @jongio for the contribution!
  - [[#6894]](https://github.com/Azure/azure-dev/pull/6894) Add automatic detection of pnpm and yarn package managers for JavaScript/TypeScript services, with explicit override support via `config.packageManager` in `azure.yaml`. Thanks @jongio for the contribution!
  - [[#6904]](https://github.com/Azure/azure-dev/pull/6904) Add `website` field to extension registry schema and display it in `azd extension show` output. Thanks @jongio for the contribution!
  - [[#6905]](https://github.com/Azure/azure-dev/pull/6905) Add azd environment variables to all framework service build subprocesses (Node.js, .NET, Java, Python, SWA) to support build-time environment variable injection. Thanks @jongio for the contribution!
  - [[#6906]](https://github.com/Azure/azure-dev/pull/6906) Add `azd extension source validate` command to validate extension registry sources against required fields, version format, capabilities, and checksum rules. Thanks @jongio for the contribution!
  
  ### Bugs Fixed
  
  - [[#6847]](https://github.com/Azure/azure-dev/pull/6847) Fix `azd env get-values` to reject unexpected positional arguments instead of silently succeeding. Thanks @spboyer for the contribution!
  - [[#6857]](https://github.com/Azure/azure-dev/pull/6857) Fix duplicated `Suggestion:` prefix appearing in error output when the suggestion text already included the prefix.
  - [[#6862]](https://github.com/Azure/azure-dev/pull/6862) Fix preflight validation errors for standard deployments being misclassified in telemetry and displayed with degraded formatting.
  - [[#6907]](https://github.com/Azure/azure-dev/pull/6907) Fix missing IPv6 CIDR blocks (`fc00::/7`, `0.0.0.0/8`, `::/128`) in MCP extension security policy that could allow SSRF bypasses. Thanks @jongio for the contribution!
  
  ### Other Changes
  
  - [[#6768]](https://github.com/Azure/azure-dev/pull/6768) Normalize user-facing CLI output to consistent lowercase `azd` branding.
  - [[#6835]](https://github.com/Azure/azure-dev/pull/6835) Improve extension error telemetry and support rich error rendering with suggestions.
  - [[#6845]](https://github.com/Azure/azure-dev/pull/6845) Add Container App-specific error guidance for secret, image pull, and template parameter failures. Thanks @spboyer for the contribution!
  - [[#6846]](https://github.com/Azure/azure-dev/pull/6846) Add RBAC and authorization error guidance for permission, policy, and role assignment failures. Thanks @spboyer for the contribution!
  - [[#6888]](https://github.com/Azure/azure-dev/pull/6888) Improve Container Apps deployment performance by reducing ARM API round-trips, saving up to 3 calls per deployment. Thanks @spboyer for the contribution!
  - [[#6902]](https://github.com/Azure/azure-dev/pull/6902) Improve AI-assisted troubleshooting with scope selection options (explain, guide, summarize) and persistent user preferences.
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6859]](https://github.com/Azure/azure-dev/pull/6859) Add `default_value` to AI prompt request messages
```

### 1.23.6 (2026-02-20)

**Commit range**: `azure-dev-cli_1.23.5..azure-dev-cli_1.23.6` (15 commits, 9 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6778 matches borderline keyword "help text". New rules recommend including it.
  > `Update azd up/provision help text with env var configuration docs (#6778)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.6 (2026-02-20)
  
  ### Features Added
  
  - [[#6777]](https://github.com/Azure/azure-dev/pull/6777) Add `--subscription` and `--location` flags to `azd provision` and `azd up` commands. Thanks @spboyer for the contribution!
  
  ### Bugs Fixed
  
  - [[#6766]](https://github.com/Azure/azure-dev/pull/6766) Fix remote build 404 error when Azure Container Registry is in a different resource group than the service.
  - [[#6770]](https://github.com/Azure/azure-dev/pull/6770) Fix subscription cache overwrite issue to preserve tenant-to-subscription mappings when tenants are temporarily inaccessible.
  - [[#6779]](https://github.com/Azure/azure-dev/pull/6779) Fix `azd init` to fail fast when `--environment` is missing in non-interactive mode with `--template`. Thanks @spboyer for the contribution!
  - [[#6789]](https://github.com/Azure/azure-dev/pull/6789) Fix `azd env config set` to support non-string types (booleans, numbers, arrays, objects). Thanks @spboyer for the contribution!
  
  ### Other Changes
  
  - [[#6771]](https://github.com/Azure/azure-dev/pull/6771) Improve provisioning error messages with Region SKU Capacity Unavailable error guidance and consent message clarity.
  - [[#6803]](https://github.com/Azure/azure-dev/pull/6803) Improve error classification for context cancellation, timeouts, and network errors. Thanks @spboyer for the contribution!
  - [[#6808]](https://github.com/Azure/azure-dev/pull/6808) Improve delegated auth experience with mode-aware authentication messaging and guidance. Thanks @scottaddie for the contribution!
  - [[#6810]](https://github.com/Azure/azure-dev/pull/6810) Add soft-delete conflict detection hints for deployment errors with guidance to run `azd down --purge`. Thanks @spboyer for the contribution!
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6778]](https://github.com/Azure/azure-dev/pull/6778) Update azd up/provision help text with env var configuration docs
```

### 1.23.5 (2026-02-13)

**Commit range**: `azure-dev-cli_1.23.4..azure-dev-cli_1.23.5` (9 commits, 4 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.23.4 (2026-02-09)

**Commit range**: `azure-dev-cli_1.23.3..azure-dev-cli_1.23.4` (16 commits, 8 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F6: Phantom entry (PR not in range)

- [WARN] PR #6698 in changelog but not found in commit range azure-dev-cli_1.23.3..azure-dev-cli_1.23.4 (phantom entry).
  > `- [[#6698]](https://github.com/Azure/azure-dev/pull/6698) Fix telemetry bundling issues.`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.4 (2026-02-09)
  
  ### Features Added
  
  - [[#6664]](https://github.com/Azure/azure-dev/pull/6664) Add JMESPath query support for JSON output in CLI commands using `--query` flag.
  - [[#6627]](https://github.com/Azure/azure-dev/pull/6627) Add App Service deployment slot routing based on deployment history.
  
  ### Bugs Fixed
  
  - [[#6674]](https://github.com/Azure/azure-dev/pull/6674) Fix duplicate `azd-service-name` tag error message to identify which resources conflict and filter validation to host resources only.
  - [[#6671]](https://github.com/Azure/azure-dev/pull/6671) Fix extension namespace handling to prevent conflicts and enable auto-install for sibling namespaces.
  - [[#6694]](https://github.com/Azure/azure-dev/pull/6694) Fix environment variable substitution for array and object Bicep parameters.
- - [[#6698]](https://github.com/Azure/azure-dev/pull/6698) Fix telemetry bundling issues.  ← F6: remove phantom entry
  
  ### Other Changes
  
  - [[#6690]](https://github.com/Azure/azure-dev/pull/6690) Improve provisioning error messages with targeted troubleshooting steps for common issues.
  - [[#6649]](https://github.com/Azure/azure-dev/pull/6649) Refactor container helper to accept environment explicitly, preventing environment confusion bugs.
  
```

### 1.23.3 (2026-01-30)

**Commit range**: `azure-dev-cli_1.23.2..azure-dev-cli_1.23.3` (7 commits, 2 changelog entries)
**Findings**: 0 errors, 2 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6654 matches borderline keyword "error message". New rules recommend including it.
  > `Clarify error messages for Azure CLI auth delegation mode (#6654)`
- [WARN] Excluded commit #6605 matches borderline keyword "prompt". New rules recommend including it.
  > `Add external prompting support to Azure Developer CLI (#6605)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.3 (2026-01-30)
  
  ### Features Added
  
  - [[#6633]](https://github.com/Azure/azure-dev/pull/6633) Add automatic detection of AI coding agents to enable no-prompt mode for seamless automation.
  
  ### Bugs Fixed
  
  - [[#6619]](https://github.com/Azure/azure-dev/pull/6619) Fix missing configuration keys in `azd config options` output.
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6654]](https://github.com/Azure/azure-dev/pull/6654) Clarify error messages for Azure CLI auth delegation mode
+ - [[#6605]](https://github.com/Azure/azure-dev/pull/6605) Add external prompting support to Azure Developer CLI
```

### 1.23.2 (2026-01-26)

**Commit range**: `azure-dev-cli_1.23.1..azure-dev-cli_1.23.2` (3 commits, 4 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F3b: Intra-release duplicate

- [WARN] PR #6604 appears 3 times within this release.

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.2 (2026-01-26)
  
  ### Bugs Fixed
  
  - [[#6610]](https://github.com/Azure/azure-dev/pull/6610) Fix Bicep CLI uninitialized path causing container app deployments to fail.
  - [[#6604]](https://github.com/Azure/azure-dev/pull/6604) Fix extension commands failing after update notification is displayed.
  - [[#6604]](https://github.com/Azure/azure-dev/pull/6604) Fix extension update notification cooldown being recorded even when warning is not shown.
  
  ### Other Changes
  
  - [[#6604]](https://github.com/Azure/azure-dev/pull/6604) Improve `azd ext list` output to better indicate when extension updates are available.
  
```

### 1.23.1 (2026-01-23)

**Commit range**: `azure-dev-cli_1.23.0..azure-dev-cli_1.23.1` (18 commits, 7 changelog entries)
**Findings**: 0 errors, 2 warnings, 1 info

#### F4: Alpha/beta feature gating

- [INFO] PR #6499 mentions alpha/feature-flag in subject. Verify gating decision.
  > `make azd init alpha feature more discoverable (#6499)`

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6583 matches borderline keyword "prompt". New rules recommend including it.
  > `Expose more configuration for PromptResourceGroup (#6583)`
- [WARN] Excluded commit #6537 matches borderline keyword "prompt". New rules recommend including it.
  > `Debug prompt fixes/improvements (#6537)`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.1 (2026-01-23)
  
  ### Features Added
  
  - [[#6511]](https://github.com/Azure/azure-dev/pull/6511) Add `azd env remove` command for deleting local environment configuration files.
  - [[#6499]](https://github.com/Azure/azure-dev/pull/6499) Improve discoverability of alpha `azd init` feature by adding hints in error messages and command output.
  
  ### Bugs Fixed
  
  - [[#6527]](https://github.com/Azure/azure-dev/pull/6527) Fix Azure DocumentDB (mongoClusters) resources not being displayed in provisioning output.
  - [[#6517]](https://github.com/Azure/azure-dev/pull/6517) Fix panic on middleware construction failure when loading invalid configuration files.
  - [[#6536]](https://github.com/Azure/azure-dev/pull/6536) Fix context cancellation issue causing subsequent operations to fail after command steps complete.
  - [[#6588]](https://github.com/Azure/azure-dev/pull/6588) Improve extension error messages by including error suggestion text.
  
  ### Other Changes
  
  - [[#6579]](https://github.com/Azure/azure-dev/pull/6579) Update GitHub CLI tool version to 2.86.0.
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6583]](https://github.com/Azure/azure-dev/pull/6583) Expose more configuration for PromptResourceGroup
+ - [[#6537]](https://github.com/Azure/azure-dev/pull/6537) Debug prompt fixes/improvements
```

### 1.23.0 (2026-01-14)

**Commit range**: `azure-dev-cli_1.22.5..azure-dev-cli_1.23.0` (34 commits, 19 changelog entries)
**Findings**: 0 errors, 2 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6368 matches borderline keyword "ux". New rules recommend including it.
  > `UX changes for concurX extension to be TUI (#6368)`

#### F6: Phantom entry (PR not in range)

- [WARN] PR #6444 in changelog but not found in commit range azure-dev-cli_1.22.5..azure-dev-cli_1.23.0 (phantom entry).
  > `- [[#6444]](https://github.com/Azure/azure-dev/pull/6444) Fix AKS deployment schema to allow Helm deployments without...`

#### Side-by-Side Diff

Shows how this release section would differ under the new rules.
Lines prefixed with `-` are removed/changed; `+` lines are the corrected version.

```diff
  ## 1.23.0 (2026-01-14)
  
  ### Features Added
  
  - [[#6390]](https://github.com/Azure/azure-dev/pull/6390) Add `azd config options` command to list all available configuration settings with descriptions.
  - [[#6348]](https://github.com/Azure/azure-dev/pull/6348) Add `azd env config` commands for environment-specific configuration management.
  - [[#6441]](https://github.com/Azure/azure-dev/pull/6441) Add support for cross-tenant authentication when using remote environment state in Azure Blob Storage.
  - [[#6436]](https://github.com/Azure/azure-dev/pull/6436) Add Podman support as fallback container runtime when Docker is unavailable.
  - [[#6418]](https://github.com/Azure/azure-dev/pull/6418) Add file-based caching to `azd show` for approximately 60x performance improvement.
  - [[#6461]](https://github.com/Azure/azure-dev/pull/6461) Add auto-detection of infrastructure provider (Bicep/Terraform) from infra directory files when not explicitly specified.
  - [[#6377]](https://github.com/Azure/azure-dev/pull/6377) Add `azd auth status` command to display current authentication status.
  - [[#6262]](https://github.com/Azure/azure-dev/pull/6262) Add property-level change details in `azd provision --preview` output for Bicep deployments.
  - [[#5536]](https://github.com/Azure/azure-dev/pull/5536) Add support for non-Aspire projects in Visual Studio connected services.
  
  ### Breaking Changes
  
  - [[#6395]](https://github.com/Azure/azure-dev/pull/6395) Remove deprecated `azd login` and `azd logout` commands in favor of `azd auth login` and `azd auth logout`.
  - [[#6369]](https://github.com/Azure/azure-dev/pull/6369) Remove Azure Spring Apps support.
  
  ### Bugs Fixed
  
  - [[#6481]](https://github.com/Azure/azure-dev/pull/6481) Fix extension configuration properties support by adding AdditionalProperties fields to project and service configurations.
  - [[#6478]](https://github.com/Azure/azure-dev/pull/6478) Fix GitHub URL parsing to check authentication before branch resolution.
  - [[#5954]](https://github.com/Azure/azure-dev/pull/5954) Improve authentication handling when not using built-in auth methods.
  - [[#6452]](https://github.com/Azure/azure-dev/pull/6452) Fix `azd down` to dynamically resolve resource display names.
  - [[#6446]](https://github.com/Azure/azure-dev/pull/6446) Fix context cancelled errors in workflow steps.
- - [[#6444]](https://github.com/Azure/azure-dev/pull/6444) Fix AKS deployment schema to allow Helm deployments without project field.  ← F6: remove phantom entry
  - [[#6267]](https://github.com/Azure/azure-dev/pull/6267) Fix `azd down` to handle deployment state correctly when resources are manually deleted.
  - [[#6435]](https://github.com/Azure/azure-dev/pull/6435) Fix `azd ext install --force` to properly reinstall extensions when version matches.
  
+ 
+ <!-- F5: borderline changes that new rules would include -->
+ - [[#6368]](https://github.com/Azure/azure-dev/pull/6368) UX changes for concurX extension to be TUI
```

### 1.22.5 (2025-12-18)

**Commit range**: `azure-dev-cli_1.22.4..azure-dev-cli_1.22.5` (4 commits, 2 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.22.4 (2025-12-17)

**Commit range**: `azure-dev-cli_1.22.3..azure-dev-cli_1.22.4` (7 commits, 5 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.22.3 (2025-12-15)

**Commit range**: `azure-dev-cli_1.22.2..azure-dev-cli_1.22.3` (4 commits, 2 changelog entries)

> **No findings** — this release is clean under the new rules.

### 1.22.2 (2025-12-12)

**Commit range**: `..azure-dev-cli_1.22.2` (0 commits, 4 changelog entries)

> **No findings** — this release is clean under the new rules.

