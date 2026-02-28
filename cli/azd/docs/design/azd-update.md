# Design: `azd update`, Auto-Update & Channel Management

**Epic**: [#6721](https://github.com/Azure/azure-dev/issues/6721)
**Status**: Draft

---

## Overview

Today, when a new version of `azd` is available, users see a warning message with copy/paste instructions to update manually. This design introduces:

1. **`azd update`** — a command that performs the update for the user
2. **Auto-update** — opt-in background updates applied at next startup
3. **Channel management** — ability to switch between `stable` and `daily` builds

The feature ships as a hidden command behind an alpha feature toggle (`alpha.update`) for safe rollout. When the toggle is off, there are zero changes to existing behavior — `azd version`, update notifications, everything stays exactly as it is today.

---

## Goals

- Make it easy for users to update `azd` intentionally
- Support opt-in auto-update for both stable and daily channels
- Preserve user control (opt-out, channel selection, check interval)
- Avoid disruption to CI/CD pipelines
- Respect platform install methods (Homebrew, winget, choco, scripts)
- Ship safely behind a feature flag with zero impact when off

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
  "version": "1.24.0-beta.1-daily.5935787",
  "buildNumber": 5935787,
  "expiresOn": "2026-02-27T08:00:00Z"
}
```

- `channel`: `"stable"` or `"daily"`. Missing field defaults to `"stable"` (backward compatible with existing cache files).
- `buildNumber`: Extracted from the daily version string's `daily.N` suffix. Used to compare daily builds since they share a base semver.
- `expiresOn`: Channel-dependent TTL — defaults to 24h for stable, 4h for daily. Configurable via `azd config set updates.checkIntervalHours <hours>`.

### Build Artifacts

- **Stable**: Published to GitHub Releases + Azure Blob Storage (`release/stable/`, `release/{VERSION}/`) + package managers (brew, winget, choco, apt, dnf)
- **Daily**: Published to Azure Blob Storage only (`release/daily/`), overwritten each build. Archived at `daily/archive/{BuildId}-{CommitSHA}/`
- **Base URL**: `https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/`

### Versioning Scheme

| State | Version Format | Example |
|-------|---------------|---------|
| Stable release | `X.Y.Z` | `1.23.6` |
| Daily build | `X.Y.Z-beta.1-daily.{BuildId}` | `1.24.0-beta.1-daily.5935787` |

After each stable release, `cli/version.txt` is immediately bumped to the next beta (e.g. `1.24.0-beta.1`). The CI pipeline appends `-daily.{BuildId}` for daily builds, where `BuildId` is the Azure DevOps `$(Build.BuildId)` — a monotonically increasing integer that lets us tell daily builds apart even though they share the same base semver.

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

**Problem**: Daily builds share a base semver (e.g., `1.24.0-beta.1`), so version comparison alone can't tell if a newer daily exists.

**Solution**: The CI pipeline publishes a `version.txt` to `release/daily/` containing the full daily version string:

```
1.24.0-beta.1-daily.5935787
```

This is the same version string baked into the binary at build time. The build number (`5935787`) is the Azure DevOps `$(Build.BuildId)` — monotonically increasing, so a higher number always means a newer build.

**Pipeline change**: Add a step in the daily publish pipeline (`Publish_Continuous_Deployment`) to write `$(CLI_VERSION)` to `version.txt` and upload alongside the binaries.

**Client comparison**: Parse the build number from the `daily.N` suffix. Compare local build number (from the running binary's version string) against remote — higher number means update available.

**Cache format** (`~/.azd/update-check.json`):
```json
{
  "channel": "daily",
  "version": "1.24.0-beta.1-daily.5935787",
  "buildNumber": 5935787,
  "expiresOn": "2026-02-27T08:00:00Z"
}
```

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

```
1. Check current channel config (stable or daily)
2. Fetch remote version info (always fresh — ignores cache for manual update)
   - Stable: GET https://aka.ms/azure-dev/versions/cli/latest
   - Daily: GET release/daily/version.txt (full version string, e.g. 1.24.0-beta.1-daily.5935787)
3. Compare with local version
   - Stable: semver comparison (blang/semver)
   - Daily: build number comparison (extracted from the daily.N suffix)
4. If no update available → "You're up to date"
5. Download update (with progress bar)
   - macOS/Linux: download archive to temp dir, extract binary
   - Windows: download MSI to temp dir
6. Verify code signature (macOS: codesign, Windows: Get-AuthenticodeSignature)
7. Install update
   - macOS/Linux: replace binary at install location (sudo if needed)
   - Windows: run MSI silently via `msiexec /i <path> /qn`
8. Done — new version takes effect on next invocation
```

#### Code Signing Verification

Before installing, the downloaded binary's code signature is verified:
- **macOS**: `codesign -v --strict <binary>` — checks Apple notarization
- **Windows**: `Get-AuthenticodeSignature` via PowerShell — checks Authenticode signature
- **Linux**: Skipped (no standard code signing mechanism)

The check is fail-safe: if `codesign` or PowerShell isn't available (unlikely), the update proceeds. But if the tool runs and the signature is explicitly invalid, the update is blocked.

#### Elevation Handling

Most install methods write to system directories requiring elevation:

| Location | Needs Elevation | Update Method |
|----------|----------------|---------------|
| `/opt/microsoft/azd/` (Linux script) | Yes — `sudo cp` | Direct binary replacement |
| `C:\Program Files\` (Windows MSI) | Yes — handled by MSI installer | MSI via `msiexec /i` |
| `~/.azd/bin/` (Windows PowerShell script) | No — user-writable | MSI via `msiexec /i` |
| Homebrew prefix | No — user-writable | Delegates to `brew upgrade azd` |
| User home dirs | No | Direct binary replacement |

**Windows**: Updates always use the MSI installer (`msiexec /i <path> /qn`), which handles UAC elevation when installing to protected locations like `C:\Program Files\`. Downgrades between GA versions are not supported via MSI.

**macOS/Linux (brew)**: Homebrew tracks installed assets, so azd never overwrites brew-managed binaries directly. Same-channel updates delegate to `brew upgrade azd`. Channel switching (stable ↔ daily) currently requires uninstalling brew and reinstalling via script. A future brew pre-release formula could enable `brew` to handle daily builds natively.

**macOS/Linux (script)**: For `sudo`, azd passes through stdin/stdout so the user sees the standard OS password prompt. Uses `CommandRunner` (`pkg/exec/command_runner.go`) for exec.

### 4. Auto-Update

When `updates.autoUpdate` is set to `on`:

**Cache TTL** (channel-dependent):
- Stable: 24h (releases are infrequent)
- Daily: 4h (builds land frequently)

The check is a cheap HTTP GET; downloads only happen when a newer version exists.

**Flow (two-phase: stage in background, apply on next startup)**:

```
Phase 1 — Stage (background goroutine during any azd invocation):
1. Check AZD_SKIP_UPDATE_CHECK / CI env vars → skip if set
2. Check version (respecting channel-dependent cache TTL)
3. If newer version available → download to ~/.azd/staging/azd
4. Verify checksum + code signature on the staged binary

Phase 2 — Apply (on NEXT startup, before command execution):
1. Detect staged binary at ~/.azd/staging/azd
2. Verify staged binary integrity (macOS: codesign check — unsigned is OK, corrupted/truncated is rejected)
3. Try to copy over current binary (with fsync to flush data to disk)
   - If writable (user home, homebrew prefix) → swap, re-exec, show success banner
   - If permission denied (system dir like /opt/microsoft/azd/) → skip, show warning
   - If staged binary is invalid (e.g. truncated download) → clean up, skip silently
4. On success: write marker file, re-exec with same args, display banner
5. On permission denied: show "WARNING: azd version X.Y.Z has been downloaded. Run 'azd update' to apply it."
   (The "out of date" banner is suppressed when this elevation warning is shown, to avoid duplicate warnings.)
```

The re-exec approach (`syscall.Exec` on Unix, spawn-and-exit on Windows) means the user's command runs seamlessly on the new binary — they just see a one-line success banner before their normal output.

**Staged binary verification**: Before applying, `verifyStagedBinary()` checks the staged binary's integrity. On macOS, it runs `codesign -v --strict`. Unsigned binaries (dev builds) are allowed ("code object is not signed at all" is OK), but corrupted/truncated binaries with invalid signatures are rejected and cleaned up. This prevents crashes from partially-downloaded files left behind when a background download goroutine is interrupted.

**Elevation-aware behavior**: Auto-update doesn't prompt for passwords. If the install location requires elevation, it gracefully falls back to a warning and the staged binary stays around for `azd update` to apply (which has the sudo fallback with an interactive prompt).

**CI/Non-Interactive Detection**: Auto-update staging is skipped when running in CI/CD. Uses `resource.IsRunningOnCI()` (supports GitHub Actions, Azure Pipelines, Jenkins, GitLab CI, CircleCI, etc.) and `AZD_SKIP_UPDATE_CHECK`.

Skip auto-update when:
- `resource.IsRunningOnCI()` returns true
- `AZD_SKIP_UPDATE_CHECK=true`

### 5. Channel Switching

#### Same Install Method (Script/MSI)

Switching channels is just changing the download source:

```bash
azd update --channel daily
# Persists channel config and updates from release/daily/ instead of release/stable/
```

**Channel switch confirmation** (any direction — daily↔stable):
```
? Switch from daily channel (1.24.0-beta.1-daily.5935787) to stable channel (1.23.6)? [Y/n]
```

If the user declines, the command prints "Channel switch cancelled." (no SUCCESS banner) and exits without modifying config or downloading anything. The channel config is only persisted after confirmation.

#### Cross Install Method

Switching between a package manager and direct installs is **not supported** via `azd update`. Users must manually uninstall and reinstall:

| Scenario | Guidance |
|----------|----------|
| Package manager → daily | Show: "Daily builds aren't available via {brew/winget/choco}. Uninstall with `{uninstall command}`, then install daily with `curl -fsSL https://aka.ms/install-azd.sh \| bash -s -- --version daily`" |
| Script/daily → package manager | Show: "To switch to {brew/winget/choco}, first uninstall the current version, then install via your package manager." |

This avoids the silent symlink overwrite problem that exists today with conflicting install methods.

**Package manager users on stable**: `azd update` delegates to the package manager. No channel switching complexity — daily isn't available through package managers.

### 6. `azd version` Output

When the update feature is enabled, `azd version` shows the channel:

```
azd version 1.23.6 (commit abc1234) (stable)
```

```
azd version 1.24.0-beta.1-daily.5935787 (commit abc1234) (daily)
```

The channel suffix is derived from the running binary's version string (presence of `daily.` pattern), not the configured channel. This means the output always reflects what the binary actually is.

When the feature toggle is off, `azd version` output stays unchanged — no suffix, no channel info.

### 7. Telemetry

Uses the existing azd telemetry infrastructure (OpenTelemetry). New telemetry fields tracked on every update operation:

| Field | Description |
|-------|-------------|
| `update.from_version` | Version before update |
| `update.to_version` | Target version |
| `update.channel` | `stable` or `daily` |
| `update.method` | How the update was performed (e.g. `brew`, `direct`, `winget`) |
| `update.result` | Result code (see below) |

**Result/Error Codes**:

| Code | Meaning |
|------|---------|
| `update.success` | Update completed successfully |
| `update.alreadyUpToDate` | No update available, already on latest |
| `update.downloadFailed` | Failed to download binary from remote |
| `update.checksumMismatch` | Downloaded binary failed integrity verification |
| `update.signatureInvalid` | Code signature verification failed |
| `update.elevationRequired` | Update requires elevation and user declined |
| `update.elevationFailed` | Elevation prompt (sudo/UAC) failed |
| `update.replaceFailed` | Failed to replace binary at install location |
| `update.packageManagerFailed` | Package manager command (brew/winget/choco) failed |
| `update.versionCheckFailed` | Failed to fetch remote version info |
| `update.unsupportedInstallMethod` | Unknown or unsupported install method |
| `update.channelSwitchDowngrade` | User declined when switching channels |
| `update.skippedCI` | Skipped due to CI/non-interactive environment |

These codes are integrated into azd's `MapError` pipeline, so update failures show up properly in telemetry dashboards alongside other command errors.

### 8. Feature Toggle (Alpha Gate)

The entire update feature ships behind `alpha.update` (default: off). This means:

- **Toggle off** (default): Zero behavior changes. `azd version` output is the same. Update notification shows the existing platform-specific install instructions. `azd update` returns an error telling the user to enable the feature.
- **Toggle on** (`azd config set alpha.update on`): All update features are active — `azd update` works, auto-update stages/applies, `azd version` shows the channel suffix, notifications say "run `azd update`."

This lets us roll out to internal users first, gather feedback, and fix issues before broader availability. Once stable, the toggle can be removed and the feature enabled by default.

### 9. Update Banner Suppression

The startup "out of date" warning banner is suppressed during `azd update` (stale version is in-process and about to be replaced) and `azd config` (user is managing settings — showing a warning alongside config changes is noise). This is handled by `suppressUpdateBanner()` in `main.go`.

When the auto-update elevation warning is shown ("azd version X.Y.Z has been downloaded. Run 'azd update' to apply it."), the "out of date" warning is also suppressed to avoid showing two redundant warnings about the same condition.

---

