# Fix Strategies

Detailed fix procedures for each preflight check failure. Process checks in
their original order (1-8) because earlier fixes can resolve later failures.

## Formatting (`gofmt`) — Auto-fix

```bash
cd cli/azd && gofmt -s -w .
```

No user confirmation needed — this is a deterministic, safe operation.

## Code Modernization (`go fix`) — Auto-fix

```bash
cd cli/azd && go fix ./...
```

No user confirmation needed — this applies standard Go modernizations.

## Copyright Headers — Auto-fix

Run the copyright check script with `--fix`:

```bash
cd cli/azd && bash ../../eng/scripts/copyright-check.sh . --fix
```

On Windows, use the detected bash/sh path (Git for Windows).

## Lint (`golangci-lint`) — Analyze and Fix

Re-run lint to get the specific findings:

```bash
cd cli/azd && golangci-lint run ./... 2>&1
```

For each finding:
1. Read the file and line indicated in the lint output.
2. Apply the appropriate fix based on the linter rule (see preflight-checks.md § 4).
3. Follow the coding conventions in `cli/azd/AGENTS.md` — especially line length (125 chars),
   error handling patterns, and modern Go idioms.

If a lint finding is ambiguous or the fix would change behavior, ask the user via `ask_user`.

## Spell Check (`cspell`) — Analyze and Fix

Re-run cspell to get the specific unknown words:

```bash
cd cli/azd && cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress 2>&1
```

For each unknown word:
1. **If it's a typo**: Fix the spelling in the source file.
2. **If it's a legitimate technical term**: Add it to `cli/azd/.vscode/cspell.yaml` using a
   file-scoped `overrides` entry. Use the pattern from existing entries in that file.
3. **If uncertain**: Ask the user via `ask_user` whether to fix the spelling or add to the dictionary.

## Build (`go build`) — Analyze and Fix

Re-run the build:

```bash
cd cli/azd && go build ./... 2>&1
```

Analyze compiler errors and fix the source code. This requires understanding the codebase
context — read the surrounding code and `cli/azd/AGENTS.md` for patterns.

If a build error requires a design decision (e.g., interface changes, new dependencies),
ask the user via `ask_user`.

## Unit Tests (`go test`) — Analyze and Fix

Re-run the failing tests. If the full preflight captured specific test failures, target those:

```bash
cd cli/azd && go test ./path/to/package/... -run TestName -short -count=1 -v 2>&1
```

If no specific failures were captured, re-run the full suite:

```bash
cd cli/azd && go test ./... -short -cover -count=1 2>&1
```

Analyze failures:
1. **Test logic bug**: Fix the test.
2. **Source code bug exposed by test**: Fix the source code.
3. **Flaky test**: Investigate root cause; do NOT skip or ignore.

## Playback Tests — Analyze and Fix

Re-run playback tests:

```bash
cd cli/azd && AZURE_RECORD_MODE=playback go test -run '<pattern>' ./test/functional -timeout 30m -count=1 -v 2>&1
```

If a recording is genuinely stale (HTTP interactions changed), add the test name to the
`excludedPlaybackTests` map in `cli/azd/magefile.go` as a last resort. Ask the user
for confirmation via `ask_user` before excluding any test.
