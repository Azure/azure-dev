# Hook Executor Config Integration — Issues

## Issue 1: Add `packageManager` config to JS/TS hook executors

**Background & Motivation**
Users running JS/TS hooks need to use the same package manager as their project (pnpm, yarn). Today the hook executor auto-detects but provides no override, unlike the framework service which supports `ServiceConfig.Config["packageManager"]`.

**User Story**
As a developer using pnpm/yarn, I want to specify `packageManager` in my hook config so that my JS/TS hooks use the correct package manager without relying on lock file detection.

**Solution Approach**
- Read `Config["packageManager"]` in JS/TS executor `Prepare()` phase
- Reuse existing `packageManagerFromConfig()` validation pattern from `framework_service_node.go`
- Pass override to `node.NewCliWithPackageManager()` instead of auto-detecting
- Applies to both `js_executor.go` and `ts_executor.go` (shared via `node_helpers.go`)
- Accepted values: `npm`, `pnpm`, `yarn` (same as ServiceConfig)

**Acceptance Criteria**
- [ ] `config.packageManager: pnpm` causes JS/TS hooks to use pnpm for install
- [ ] `config.packageManager: yarn` causes JS/TS hooks to use yarn for install
- [ ] Omitting `packageManager` preserves current auto-detection behavior (lock file → default npm)
- [ ] Invalid values (e.g., `bun`) produce a clear error message
- [ ] Unit tests covering override, fallback, and invalid value cases

**Default when omitted:** Auto-detect from lock files (existing behavior)

**Out of Scope**
- Custom install flags (e.g., `--no-audit`) — future P2 enhancement
- TypeScript runtime selection (`tsRuntime`) — separate P1 issue
- Bun or Deno support — not currently supported in azd

**Testing Expectations**
- Unit tests: table-driven tests for config parsing, validation, and CLI selection
- Test that override beats auto-detection when both are present

**Type:** backend
**Size:** M (4-7 files) — `js_executor.go`, `ts_executor.go`, `node_helpers.go`, tests
**Labels:** `area/hooks`, `enhancement`
**Depends on:** PR #7690 (core Config plumbing)
**Related:** #7653, #7435

---

## Issue 2: Add `virtualEnvName` config to Python hook executor

**Background & Motivation**
Python hook scripts default to `{baseName}_env` for virtual environments, but many Python projects use `.venv` by convention. The issue #7653 explicitly calls out `virtualEnvName` as a key config property for Python hooks.

**User Story**
As a Python developer, I want to specify `virtualEnvName` in my hook config so that my Python hooks use my preferred virtual environment directory.

**Solution Approach**
- Read `Config["virtualEnvName"]` in `python_executor.go` `Prepare()` phase
- If set, use as the venv name instead of `VenvNameForDir()` default
- Validate it's a string, non-empty, and doesn't contain path separators (security: prevent path traversal)
- Pass to existing `EnsureVirtualEnv()` and `InstallDependencies()` methods

**Acceptance Criteria**
- [ ] `config.virtualEnvName: .venv` creates/uses `.venv` directory for the hook
- [ ] `config.virtualEnvName: my_env` creates/uses `my_env` directory
- [ ] Omitting `virtualEnvName` preserves current behavior (search `.venv`/`venv`, fall back to `{baseName}_env`)
- [ ] Values with path separators (`/`, `\`) are rejected with a clear error
- [ ] Empty string values are rejected with a clear error
- [ ] Unit tests covering override, fallback, and validation

**Default when omitted:** Existing behavior — search for `.venv`/`venv` in project dir, then fall back to `{baseName}_env`

**Out of Scope**
- Python package manager selection (`uv`, `poetry`, `pdm`) — future P2 enhancement
- Custom requirements file name — future enhancement
- Python binary path override — future enhancement

**Testing Expectations**
- Unit tests: table-driven tests for config parsing, validation, and venv name resolution
- Test path traversal rejection (e.g., `../evil`)

**Type:** backend
**Size:** S (1-3 files) — `python_executor.go`, test file
**Labels:** `area/hooks`, `enhancement`
**Depends on:** PR #7690 (core Config plumbing)
**Related:** #7653, #7435

---

## Issue 3: Add `configuration` and `framework` config to .NET hook executor

**Background & Motivation**
The .NET hook executor builds with an empty configuration (SDK default = Debug), while the framework service hardcodes `Release`. Users need control over the build configuration for hook scripts, especially for production seed/migration hooks. Multi-targeting users also need to specify a target framework moniker.

**User Story**
As a .NET developer, I want to specify `configuration` and `framework` in my hook config so that my .NET hooks build with the correct settings.

**Solution Approach**
- Read `Config["configuration"]` in `dotnet_executor.go` `Prepare()` phase
- Pass to `dotnetCli.Build()` instead of empty string
- Validate `configuration` is a string (no enum restriction — MSBuild allows custom configurations like `Staging`)
- Read `Config["framework"]` for target framework moniker
- Pass as `-f` / `--framework` flag to `dotnet run` in `Execute()` phase

**Acceptance Criteria**
- [ ] `config.configuration: Release` builds hook script in Release mode
- [ ] `config.configuration: Debug` explicitly builds in Debug mode
- [ ] `config.framework: net10.0` targets specific framework for both build and run
- [ ] Omitting `configuration` preserves current behavior (empty string → SDK default)
- [ ] Omitting `framework` preserves current behavior (auto-detected from project)
- [ ] Both can be specified together
- [ ] Unit tests covering configuration override, framework override, combined, and fallback

**Defaults when omitted:**
- `configuration`: empty string (SDK default, typically Debug)
- `framework`: not set (auto-detected from project file)

**Out of Scope**
- Custom dotnet run arguments — future enhancement
- Suppressing DOTNET_NOLOGO — low priority
- Runtime identifier (`-r`) support — future enhancement

**Testing Expectations**
- Unit tests: table-driven tests for config parsing and dotnet CLI argument construction
- Test that configuration is passed to both Build() and Run() commands
- Test that framework is passed to Run() command

**Type:** backend
**Size:** S (1-3 files) — `dotnet_executor.go`, test file
**Labels:** `area/hooks`, `enhancement`
**Depends on:** PR #7690 (core Config plumbing)
**Related:** #7653, #7435
