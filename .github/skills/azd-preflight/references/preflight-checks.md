# Preflight Checks Reference

The `mage preflight` command runs these 8 checks in order. Each check, its
purpose, and the automated fix strategy are listed below.

## 1. Formatting (`gofmt`)

**Command**: `gofmt -s -l .` (from `cli/azd/`)
**Passes when**: No files are listed (all files are formatted).
**Auto-fix**: `gofmt -s -w .`

## 2. Code Modernization (`go fix`)

**Command**: `go fix -diff ./...` (from `cli/azd/`)
**Passes when**: No diff output (all modernizations already applied).
**Auto-fix**: `go fix ./...`

## 3. Copyright Headers

**Command**: `eng/scripts/copyright-check.sh .` (from `cli/azd/`, run via bash/sh)
**Passes when**: All `.go` files have the Microsoft copyright header.
**Auto-fix**: `eng/scripts/copyright-check.sh . --fix`

## 4. Lint (`golangci-lint`)

**Command**: `golangci-lint run ./...` (from `cli/azd/`)
**Passes when**: Zero lint findings.
**Auto-fix**: Analyze each finding and apply the appropriate code change. Common
issues include:
- `lll` (line too long) — break long lines
- `gosec` — address security findings
- `errorlint` — use `errors.Is`/`errors.As`/`errors.AsType` correctly
- `unused` — remove dead code
- `staticcheck` — fix static analysis warnings

## 5. Spell Check (`cspell`)

**Command**: `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress` (from `cli/azd/`)
**Passes when**: No unknown words found.
**Auto-fix**: For legitimate technical terms, add them to `cli/azd/.vscode/cspell.yaml`
using file-scoped `overrides` entries (not the global `words` list). For actual
typos, fix the spelling in source code.

## 6. Build (`go build`)

**Command**: `go build ./...` (from `cli/azd/`)
**Passes when**: Compilation succeeds with zero errors.
**Auto-fix**: Analyze compiler errors and fix the source code. Common causes:
- Missing imports
- Type mismatches
- Undefined symbols
- Syntax errors

## 7. Unit Tests (`go test -short`)

**Command**: `go test ./... -short -cover -count=1` (from `cli/azd/`)
**Passes when**: All tests pass.
**Auto-fix**: Analyze test failures and fix the root cause in source code or
tests. Do NOT skip or delete failing tests — fix them.

## 8. Playback Tests (Functional)

**Command**: Discovers test recordings in `test/functional/testdata/recordings/`
and runs matching functional tests with `AZURE_RECORD_MODE=playback`.
**Passes when**: All playback tests pass.
**Auto-fix**: Analyze failures. If the test logic is wrong, fix it. If a
recording is stale, add the test name to `excludedPlaybackTests` in
`cli/azd/magefile.go` with a reason string — but only as a last resort after
confirming the recording genuinely needs re-recording.

## Prerequisites

Before running preflight, these tools must be installed:

- **Go** (version matching `cli/azd/go.mod`)
- **golangci-lint**: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.11.4`
- **cspell**: `npm install -g cspell@8.13.1`
- **bash or sh**: On Windows, Git for Windows provides this
- **mage**: `go install github.com/magefile/mage@latest`
