# Changelog Audit Report

**Generated**: 2026-04-11 16:26 UTC
**Repo SHA**: ed79e6687
**Releases audited**: 20
**Rules applied**: F1 (dual PR numbers), F2 (PR link validation), F3 (cross-release dedup), F4 (alpha/beta gating), F5 (borderline inclusion), F6 (phantom entries)

## How to Review

Each audited release has two files:

- `published/<version>.md` — the changelog section exactly as published
- `corrected/<version>.md` — the same section with deterministic corrections applied

**Compare them** to see the impact of the new rules:

```bash
# Diff a single release
diff -u published/1.23.12.md corrected/1.23.12.md

# Diff all releases at once
diff -ru published/ corrected/
```

**Releases with corrections** (5 of 20): 1.23.14, 1.23.13, 1.23.12, 1.23.4, 1.23.0

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

> No findings — this release is clean under the new rules.

### 1.23.14 (2026-04-03)

**Commit range**: `azure-dev-cli_1.23.13..azure-dev-cli_1.23.14` (33 commits, 18 changelog entries)
**Diff**: `diff -u published/1.23.14.md corrected/1.23.14.md`
**Findings**: 0 errors, 2 warnings, 1 info

#### F4: Alpha/beta feature gating

- [INFO] PR #7489 mentions alpha/feature-flag in subject. Verify gating decision.
  >
  >     move update from alpha to beta (#7489)

#### F6: Phantom entry (PR not in range)

- [WARN] PR #7343 in changelog but not found in commit range azure-dev-cli_1.23.13..azure-dev-cli_1.23.14 (phantom entry).
  >
  >     - [[#7343]](https://github.com/Azure/azure-dev/pull/7343) Fix nil pointer panic when `azure.yaml` contains services, ...
- [WARN] PR #7299 in changelog but not found in commit range azure-dev-cli_1.23.13..azure-dev-cli_1.23.14 (phantom entry).
  >
  >     - [[#7299]](https://github.com/Azure/azure-dev/pull/7299) Add command-specific telemetry attributes for `auth login`,...

### 1.23.13 (2026-03-26)

**Commit range**: `azure-dev-cli_1.23.12..azure-dev-cli_1.23.13` (13 commits, 15 changelog entries)
**Diff**: `diff -u published/1.23.13.md corrected/1.23.13.md`
**Findings**: 6 errors, 3 warnings, 0 info

#### F2: Missing PR link on entry

- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add `ConfigHelper` for typed, ergonomic access to azd user and environment configuration through gRPC services, wit...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add `Pager[T]` generic pagination helper with SSRF-safe nextLink validation, `Collect` with `MaxPages`/`MaxItems` b...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add `ResilientClient` hardening: exponential backoff with jitter, upfront body seekability validation, and `Retry-A...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add `SSRFGuard` standalone SSRF protection with metadata endpoint blocking, private network blocking, HTTPS enforce...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add atomic file operations (`WriteFileAtomic`, `CopyFileAtomic`, `BackupFile`, `EnsureDir`) with crash-safe write-t...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add runtime process utilities for cross-platform process management, tool discovery, and shell execution helpers.

#### F2b: Issue link instead of PR link

- [WARN] Entry uses issue link (#2743) instead of PR link. Use /pull/ not /issues/.
  >
  >     - [[#2743]](https://github.com/Azure/azure-dev/issues/2743) Support deploying Container App Jobs (`Microsoft.App/jobs...

#### F6: Phantom entry (PR not in range)

- [WARN] PR #2743 in changelog but not found in commit range azure-dev-cli_1.23.12..azure-dev-cli_1.23.13 (phantom entry).
  >
  >     - [[#2743]](https://github.com/Azure/azure-dev/issues/2743) Support deploying Container App Jobs (`Microsoft.App/jobs...
- [WARN] PR #7330 in changelog but not found in commit range azure-dev-cli_1.23.12..azure-dev-cli_1.23.13 (phantom entry).
  >
  >     - [[#7330]](https://github.com/Azure/azure-dev/pull/7330) Add `azure.yaml` schema metadata to enable automatic schema...

### 1.23.12 (2026-03-24)

**Commit range**: `azure-dev-cli_1.23.11..azure-dev-cli_1.23.12` (10 commits, 4 changelog entries)
**Diff**: `diff -u published/1.23.12.md corrected/1.23.12.md`
**Findings**: 0 errors, 1 warnings, 0 info

#### F1: Dual PR number extraction

- [WARN] Dual PR numbers detected: commit references #7223 and #7291. Changelog uses #7223 but should use #7291 (last = canonical).
  >
  >     Add funcignore handling fix (#7223) to 1.23.12 changelog (#7291)

### 1.23.11 (2026-03-20)

**Commit range**: `azure-dev-cli_1.23.10..azure-dev-cli_1.23.11` (24 commits, 11 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #7213 matches borderline keyword "ux". New rules recommend including it.
  >
  >     implement install script and brew cask for linux/macos (#7213)

### 1.23.10 (2026-03-16)

**Commit range**: `azure-dev-cli_1.23.9..azure-dev-cli_1.23.10` (7 commits, 2 changelog entries)

> No findings — this release is clean under the new rules.

### 1.23.9 (2026-03-13)

**Commit range**: `azure-dev-cli_1.23.8..azure-dev-cli_1.23.9` (25 commits, 14 changelog entries)
**Findings**: 3 errors, 0 warnings, 0 info

#### F2: Missing PR link on entry

- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add Extension SDK Reference documentation covering `NewExtensionRootCommand`, `MCPServerBuilder`, `ToolArgs`, `MCPS...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add Extension Migration Guide with before/after examples for migrating from legacy patterns to SDK helpers. See [Ex...
- [ERROR] Entry is missing a [[#PR]] link.
  >
  >     - Add Extension End-to-End Walkthrough demonstrating root command setup, MCP server construction, lifecycle event han...

### 1.23.8 (2026-03-06)

**Commit range**: `azure-dev-cli_1.23.7..azure-dev-cli_1.23.8` (32 commits, 18 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6939 matches borderline keyword "output". New rules recommend including it.
  >
  >     Silence extension process stderr logs to prevent call-stack-like error output (#6939)

### 1.23.7 (2026-02-27)

**Commit range**: `azure-dev-cli_1.23.6..azure-dev-cli_1.23.7` (30 commits, 19 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6859 matches borderline keyword "prompt". New rules recommend including it.
  >
  >     Add `default_value` to AI prompt request messages (#6859)

### 1.23.6 (2026-02-20)

**Commit range**: `azure-dev-cli_1.23.5..azure-dev-cli_1.23.6` (15 commits, 9 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6778 matches borderline keyword "help text". New rules recommend including it.
  >
  >     Update azd up/provision help text with env var configuration docs (#6778)

### 1.23.5 (2026-02-13)

**Commit range**: `azure-dev-cli_1.23.4..azure-dev-cli_1.23.5` (9 commits, 4 changelog entries)

> No findings — this release is clean under the new rules.

### 1.23.4 (2026-02-09)

**Commit range**: `azure-dev-cli_1.23.3..azure-dev-cli_1.23.4` (16 commits, 8 changelog entries)
**Diff**: `diff -u published/1.23.4.md corrected/1.23.4.md`
**Findings**: 0 errors, 1 warnings, 0 info

#### F6: Phantom entry (PR not in range)

- [WARN] PR #6698 in changelog but not found in commit range azure-dev-cli_1.23.3..azure-dev-cli_1.23.4 (phantom entry).
  >
  >     - [[#6698]](https://github.com/Azure/azure-dev/pull/6698) Fix telemetry bundling issues.

### 1.23.3 (2026-01-30)

**Commit range**: `azure-dev-cli_1.23.2..azure-dev-cli_1.23.3` (7 commits, 2 changelog entries)
**Findings**: 0 errors, 2 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6654 matches borderline keyword "error message". New rules recommend including it.
  >
  >     Clarify error messages for Azure CLI auth delegation mode (#6654)
- [WARN] Excluded commit #6605 matches borderline keyword "prompt". New rules recommend including it.
  >
  >     Add external prompting support to Azure Developer CLI (#6605)

### 1.23.2 (2026-01-26)

**Commit range**: `azure-dev-cli_1.23.1..azure-dev-cli_1.23.2` (3 commits, 4 changelog entries)
**Findings**: 0 errors, 1 warnings, 0 info

#### F3b: Intra-release duplicate

- [WARN] PR #6604 appears 3 times within this release.

### 1.23.1 (2026-01-23)

**Commit range**: `azure-dev-cli_1.23.0..azure-dev-cli_1.23.1` (18 commits, 7 changelog entries)
**Findings**: 0 errors, 2 warnings, 1 info

#### F4: Alpha/beta feature gating

- [INFO] PR #6499 mentions alpha/feature-flag in subject. Verify gating decision.
  >
  >     make azd init alpha feature more discoverable (#6499)

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6583 matches borderline keyword "prompt". New rules recommend including it.
  >
  >     Expose more configuration for PromptResourceGroup (#6583)
- [WARN] Excluded commit #6537 matches borderline keyword "prompt". New rules recommend including it.
  >
  >     Debug prompt fixes/improvements (#6537)

### 1.23.0 (2026-01-14)

**Commit range**: `azure-dev-cli_1.22.5..azure-dev-cli_1.23.0` (34 commits, 19 changelog entries)
**Diff**: `diff -u published/1.23.0.md corrected/1.23.0.md`
**Findings**: 0 errors, 2 warnings, 0 info

#### F5: Borderline excluded commit

- [WARN] Excluded commit #6368 matches borderline keyword "ux". New rules recommend including it.
  >
  >     UX changes for concurX extension to be TUI (#6368)

#### F6: Phantom entry (PR not in range)

- [WARN] PR #6444 in changelog but not found in commit range azure-dev-cli_1.22.5..azure-dev-cli_1.23.0 (phantom entry).
  >
  >     - [[#6444]](https://github.com/Azure/azure-dev/pull/6444) Fix AKS deployment schema to allow Helm deployments without...

### 1.22.5 (2025-12-18)

**Commit range**: `azure-dev-cli_1.22.4..azure-dev-cli_1.22.5` (4 commits, 2 changelog entries)

> No findings — this release is clean under the new rules.

### 1.22.4 (2025-12-17)

**Commit range**: `azure-dev-cli_1.22.3..azure-dev-cli_1.22.4` (7 commits, 5 changelog entries)

> No findings — this release is clean under the new rules.

### 1.22.3 (2025-12-15)

**Commit range**: `azure-dev-cli_1.22.2..azure-dev-cli_1.22.3` (4 commits, 2 changelog entries)

> No findings — this release is clean under the new rules.

### 1.22.2 (2025-12-12)

**Commit range**: (no previous tag in scope) (0 commits, 4 changelog entries)

> No findings — this release is clean under the new rules.

