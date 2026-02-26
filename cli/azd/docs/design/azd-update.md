# Design: `azd update`, Auto-Update & Channel Management

**Epic**: [#6721](https://github.com/Azure/azure-dev/issues/6721)
**Status**: Draft

---

## Overview

Today, when a new version of `azd` is available, users see a warning message with copy/paste instructions to update manually. This design introduces:

1. **`azd update`** — a command that performs the update for the user
2. **Auto-update** — opt-in background updates applied at next startup
3. **Channel management** — ability to switch between `stable` and `daily` builds

The feature will ship as a hidden command initially for internal testing before being advertised publicly.

---

## Goals

- Make it easy for users to update `azd` intentionally
- Support opt-in auto-update for both stable and daily channels
- Preserve user control (opt-out, channel selection, check interval)
- Avoid disruption to CI/CD pipelines
- Respect platform install methods (Homebrew, winget, choco, scripts)

---

## Existing Infrastructure

### Install Method Tracking

azd already tracks how it was installed via `.installed-by.txt` placed alongside the binary:

| Installer | Value | Default Location |
|-----------|-------|------------------|
| Bash script | `install-azd.sh` | `/opt/microsoft/azd/` |
| PowerShell script | `install-azd.ps1` | `C:\Program Files\Azure Dev CLI\` (customizable via `-InstallFolder`) |
| Homebrew | `brew` | Homebrew prefix (e.g., `/usr/local/Cellar/azd/`) |
| Chocolatey | `choco` | `C:\Program Files\Azure Dev CLI\` (via MSI) |
| Winget | `winget` | `C:\Program Files\Azure Dev CLI\` (via MSI) |
| Debian pkg | `deb` | `/opt/microsoft/azd/` |
| RPM pkg | `rpm` | `/opt/microsoft/azd/` |
| MSI | `msi` | `C:\Program Files\Azure Dev CLI\` |

`.installed-by.txt` is always placed in the same directory as the azd binary. At runtime, azd locates itself via `os.Executable()` and reads `.installed-by.txt` from that directory.

**Code**: `cli/azd/pkg/installer/installed_by.go`

### Version Check

- **Endpoint**: `https://aka.ms/azure-dev/versions/cli/latest` — returns latest stable semver (plaintext)
- **Logic**: `main.go` → `fetchLatestVersion()` — async check at startup, cached in `~/.azd/update-check.json`
- **Skip**: `AZD_SKIP_UPDATE_CHECK=true` disables the check
- Already shows platform-specific upgrade instructions based on install method

**Current cache format** (`~/.azd/update-check.json`):
```json
{"version":"1.23.6","expiresOn":"2026-02-26T01:24:50Z"}
```

**New cache format** (extended for channel + daily support):
```json
{
  "channel": "daily",
  "version": "1.24.0-beta.1",
  "buildNumber": 98770,
  "expiresOn": "2026-02-26T08:00:00Z"
}
```

- `channel`: `"stable"` or `"daily"`. Missing field defaults to `"stable"` (backward compatible with existing cache files).
- `buildNumber`: Only used for daily builds (semver alone can't distinguish dailies). Missing field triggers a fresh check for daily users.
- `expiresOn`: Channel-dependent TTL — defaults to 24h for stable, 4h for daily. Configurable via `azd config set updates.checkIntervalHours <hours>`.

### Build Artifacts

- **Stable**: Published to GitHub Releases + Azure Blob Storage (`release/stable/`, `release/{VERSION}/`) + package managers (brew, winget, choco, apt, dnf)
- **Daily**: Published to Azure Blob Storage only (`release/daily/`), overwritten each build. Archived at `daily/archive/{BuildId}-{CommitSHA}/`
- **Base URL**: `https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/`

### Versioning Scheme

| State | Version Format | Example |
|-------|---------------|---------|
| Stable release | `X.Y.Z` | `1.23.6` |
| Development (daily) | `X.Y.Z-beta.1` | `1.24.0-beta.1` |

After each stable release, `cli/version.txt` is immediately bumped to the next beta. Daily builds all carry this beta version until the next release.

### Reusable Existing Patterns

The extension manager (`pkg/extensions/manager.go`) already implements a nearly identical download-verify-install flow. Reuse these existing utilities rather than building new ones:

| Capability | Existing Code | Notes |
|-----------|---------------|-------|
| **HTTP download + progress** | `pkg/input/progress_log.go`, `pkg/async/progress.go` | Terminal-based progress display |
| **Checksum verification** | `pkg/extensions/manager.go` → `validateChecksum()` | Supports SHA256/SHA512 |
| **Staging + temp file mgmt** | `pkg/extensions/manager.go` → `downloadFromRemote()` | Downloads to `os.TempDir()`, cleanup via `defer os.Remove()` |
| **Shelling out to tools** | `pkg/exec/command_runner.go` → `CommandRunner` interface | Wraps `os/exec` with context support, I/O routing |
| **Config nested keys** | `pkg/config/config.go` → `Get(path)`, `GetString(path)` | Dotted path traversal (e.g., `updates.channel`) |
| **Hidden commands** | `cmd/build.go`, `cmd/auth_token.go` | `Hidden: true` on `cobra.Command` |
| **Semver comparison** | `blang/semver/v4` (main.go), `Masterminds/semver/v3` (extensions) | Already used for version check |
| **User confirmation** | `pkg/ux/confirm.go` → `NewConfirm()` | Standard `[y/N]` prompt pattern |
| **Binary self-location** | `pkg/installer/installed_by.go` → `os.Executable()` | Resolves symlinks, finds install dir |
| **Background goroutine** | `main.go` → `fetchLatestVersion()` | Non-blocking startup check pattern |
| **CI detection** | `internal/tracing/resource/ci.go` → `IsRunningOnCI()` | Detects GitHub Actions, Azure Pipelines, Jenkins, etc. |

---

## Design

### 1. Configuration

Two new config keys via `azd config`:

```bash
azd config set updates.autoUpdate on     # or "off" (default: off)
```

Channel is set via `azd update --channel <stable|daily>` (which persists the choice to `updates.channel` config). Default channel is `stable`.

These follow the existing convention of `"on"/"off"` for boolean-like config values (consistent with alpha features).

### 2. Daily Build Version Tracking

**Problem**: Daily builds all share the same semver (e.g., `1.24.0-beta.1`), so version comparison alone can't determine if a newer daily exists.

**Solution**: Upload a `version.txt` to `release/daily/` containing the Azure DevOps `$(Build.BuildId)` (monotonically increasing integer):

```
1.24.0-beta.1
98770
```

**Pipeline change**: Add a step in the daily publish pipeline to write and upload this file alongside the binaries. Same overwrite behavior — zero storage impact.

**Client comparison**: Cache the build number locally (extend existing `~/.azd/update-check.json`). Compare local build number against remote — higher number means update available.

### 3. `azd update` Command

A new command (initially hidden) that updates the azd binary.

**Usage**:
```bash
azd update                                        # Update to latest version on current channel
azd update --channel daily                        # Switch channel to daily and update now
azd update --channel stable                       # Switch channel to stable and update now
azd update --auto-update on                       # Enable auto-update
azd update --auto-update off                      # Disable auto-update
azd update --check-interval-hours 4               # Override check interval
```

Flags can be combined: `azd update --channel daily --auto-update on --check-interval-hours 2`

**Defaults**:

| Flag | Config Key | Default | Values |
|------|-----------|---------|--------|
| `--channel` | `updates.channel` | `stable` | `stable`, `daily` |
| `--auto-update` | `updates.autoUpdate` | `off` | `on`, `off` |
| `--check-interval-hours` | `updates.checkIntervalHours` | `24` (stable), `4` (daily) | Any positive integer |

All flags persist their values to config, which can also be set directly via `azd config set`.

**Update strategy based on install method**:

| Install Method | Strategy |
|----------------|----------|
| `brew` | Shell out: `brew upgrade azd` |
| `winget` | Shell out: `winget upgrade Microsoft.Azd` |
| `choco` | Shell out: `choco upgrade azd` |
| `install-azd.sh`, `install-azd.ps1`, `msi`, `deb`, `rpm` | Direct binary download + replace |

> **Note**: Linux `deb`/`rpm` packages are standalone files from GitHub Releases — there is no managed apt/dnf repository. These users are treated the same as script-installed users for update purposes.

#### Direct Binary Update Flow (Script/MSI Users)

Follows the same download-verify-install pattern as the extension manager (`pkg/extensions/manager.go`):

```
1. Check current channel config (stable or daily)
2. Fetch remote version info (always fresh — ignores cache for manual update)
   - Stable: GET https://aka.ms/azure-dev/versions/cli/latest
   - Daily: GET release/daily/version.txt (semver + build number)
3. Compare with local version (using existing blang/semver library)
   - Stable: semver comparison
   - Daily: build number comparison
4. If no update available → "You're up to date"
5. Download new binary to ~/.azd/staging/azd-new (with progress bar via pkg/input/progress_log.go)
6. Verify integrity (reuse pkg/extensions/manager.go validateChecksum())
7. Replace binary at install location
   - If install location is user-writable → direct move
   - If install location needs elevation → `sudo mv` (user sees OS password prompt)
8. Done — new version takes effect on next invocation
```

#### Elevation Handling

Most install methods write to system directories requiring elevation:

| Location | Needs Elevation |
|----------|----------------|
| `/opt/microsoft/azd/` (Linux script) | Yes — `sudo mv` |
| `C:\Program Files\` (Windows MSI) | Yes — UAC |
| Homebrew prefix | No — user-writable |
| User home dirs | No |

For `sudo`, azd passes through stdin/stdout so the user sees the standard OS password prompt. Use existing `CommandRunner` (`pkg/exec/command_runner.go`) for exec:

```go
cmd := exec.Command("sudo", "mv", stagedBinary, currentBinaryPath)
cmd.Stdin = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
```

One password prompt total (download to staging is in user space, only the final move needs elevation).

### 4. Auto-Update

When `updates.autoUpdate` is set to `on`:

**Cache TTL** (channel-dependent):
- Stable: 24h (releases are infrequent)
- Daily: 4h (builds land frequently)

The check is a cheap HTTP GET; downloads only happen when a newer version exists.

**Flow (download + swap on next startup)**:

```
Startup (every azd invocation):
1. Check AZD_SKIP_UPDATE_CHECK / CI env vars → skip if set
2. Check non-interactive terminal → skip if detected
3. Background goroutine: check version (respecting channel-dependent cache TTL)
4. If newer version available → download to ~/.azd/staging/
5. On NEXT startup: detect staged binary → swap → continue execution
6. Display banner: "azd has been updated to version X.Y.Z"
```

This approach is used universally across platforms for consistency (avoids the Windows binary-locking issue).

**CI/Non-Interactive Detection**: Auto-update is disabled when running in CI/CD or non-interactive environments. Use the existing `resource.IsRunningOnCI()` detection (supports GitHub Actions, Azure Pipelines, Jenkins, GitLab CI, CircleCI, and others) and `--no-prompt` flag. This is consistent with how azd already disables interactive prompts in these environments.

Skip auto-update when:
- `resource.IsRunningOnCI()` returns true
- `--no-prompt` flag is set
- `AZD_SKIP_UPDATE_CHECK=true`

### 5. Channel Switching

#### Same Install Method (Script/MSI)

Switching channels is just changing the download source:

```bash
azd update --channel daily
# Persists channel config and updates from release/daily/ instead of release/stable/
```

**Daily → Stable downgrade warning** (using existing `pkg/ux/confirm.go` → `NewConfirm()`):
```
⚠ You're currently on daily build 1.24.0-beta.1 (build 98770).
  Switching to stable will downgrade you to 1.23.6.
  Continue? [y/N]
```

#### Cross Install Method

Switching between a package manager and direct installs is **not supported** via `azd update`. Users must manually uninstall and reinstall:

| Scenario | Guidance |
|----------|----------|
| Package manager → daily | Show: "Daily builds aren't available via {brew/winget/choco}. Uninstall with `{uninstall command}`, then install daily with `curl -fsSL https://aka.ms/install-azd.sh \| bash -s -- --version daily`" |
| Script/daily → package manager | Show: "To switch to {brew/winget/choco}, first uninstall the current version, then install via your package manager." |

This avoids the silent symlink overwrite problem that exists today with conflicting install methods.

**Package manager users on stable**: `azd update` delegates to the package manager. No channel switching complexity — daily isn't available through package managers.

### 6. `azd version` Output

Extend `azd version` to show channel and update info:

```
azd version 1.23.6 (stable)
```

```
azd version 1.24.0-beta.1 (daily, build 98770)
```

### 7. Telemetry

Leverage existing azd telemetry infrastructure (OpenTelemetry) — new commands and flags are automatically tracked. Additionally track:
- Update success/failure outcomes
- Update method used (package manager vs direct binary download)
- Channel distribution (stable vs daily)
- Auto-update opt-in rate

**Result/Error Codes**:

| Code | Meaning |
|------|---------|
| `update.success` | Update completed successfully |
| `update.alreadyUpToDate` | No update available, already on latest |
| `update.downloadFailed` | Failed to download binary from remote |
| `update.checksumMismatch` | Downloaded binary failed integrity verification |
| `update.elevationRequired` | Update requires elevation and user declined |
| `update.elevationFailed` | Elevation prompt (sudo/UAC) failed |
| `update.replaceFailed` | Failed to replace binary at install location |
| `update.packageManagerFailed` | Package manager command (brew/winget/choco) failed |
| `update.versionCheckFailed` | Failed to fetch remote version info |
| `update.unsupportedInstallMethod` | Unknown or unsupported install method |
| `update.channelSwitchDowngrade` | User declined downgrade when switching channels |
| `update.skippedCI` | Skipped due to CI/non-interactive environment |

---

