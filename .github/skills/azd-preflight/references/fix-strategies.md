# Fix Strategies

Detailed fix procedures for each preflight check failure. Process checks in
their original order (1-9) because earlier fixes can resolve later failures.

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

## Go Spell Check (`cspell`) — Analyze and Fix

Re-run the Go source spell check to get the specific unknown words:

```bash
cd cli/azd && cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress 2>&1
```

For each unknown word:
1. **If it's a typo**: Fix the spelling in the source file.
2. **If it's a legitimate technical term**: Add it to `cli/azd/.vscode/cspell.yaml` using a
   file-scoped `overrides` entry. Use the pattern from existing entries in that file.
3. **If uncertain**: Ask the user via `ask_user` whether to fix the spelling or add to the dictionary.

## Misc/Docs Spell Check (`cspell-misc`) — Analyze and Fix

Re-run the repository-wide spell check from the repository root:

```bash
cspell lint "**/*" --relative --config ./.vscode/cspell.misc.yaml --no-progress 2>&1
```

For each unknown word:
1. **If it's a typo**: Fix the spelling in the source file.
2. **If it's a legitimate file-specific term**: Add it to a file-scoped `overrides` entry in
   `.vscode/cspell.misc.yaml`. Use `.vscode/cspell.global.yaml` only for terms used broadly across
   the repository.
3. **If uncertain**: Ask the user via `ask_user` whether to fix the spelling or update a dictionary.

## Changed CHANGELOG Spell Check — Analyze and Fix

The normal misc/docs check excludes directories with their own cspell configuration, including
`cli/`. For each changed changelog, walk from its directory toward the repository root and select
the nearest `cspell.yaml` or `.vscode/cspell.yaml`. Fall back to
`cli/azd/.vscode/cspell.yaml` when no nearer config exists. Run the targeted check from the
repository root:

```bash
cspell lint "<changelog-path>" --relative --config "<cspell-config>" --no-progress 2>&1
```

For each unknown word:
1. **If it's a typo or ordinary prose**: Fix the changelog entry.
2. **If it is a GitHub username in a contributor attribution**:
   - Verify the exact alias is the author of the changelog entry's linked PR. If the PR author is
    unavailable, verify the account resolves with `gh api "users/<alias>" --silent`.
   - Add the exact alias to `.vscode/cspell-github-user-aliases.txt`, preserving the file's
    case-insensitive alphabetical order and avoiding duplicates.
   - Do not add ordinary names, misspellings, or unverified handles to the alias dictionary.
3. **If it's another legitimate changelog-specific term**: Add it to a `CHANGELOG.md`
   file-scoped `overrides` entry in the resolved owning cspell config.
4. **If uncertain**: Ask the user via `ask_user` whether to fix the spelling or update a dictionary.

## Generated Pipeline Action Pins — Auto-fix

When `cli/azd/resources/pipeline/.github/workflows/azure-dev-actions.yml` changes:

1. Read the `uses:` value and trailing release-version comment for the `checkout`, `setup-azd`,
   `setup-terraform`, and `setup-dotnet` step IDs.
2. Validate that each ID maps to its expected action repository and uses a full 40-character SHA.
3. Update the matching action repository's literal `uses:` line in
   `cli/azd/resources/pipeline/.github/azure-dev.ymlt`. Do not match by line number or copy a SHA
   between different action repositories.
4. Regenerate the pipeline snapshots from `cli/azd/`:

   ```bash
   UPDATE_SNAPSHOTS=true go test ./pkg/pipeline -run '^Test_promptForCiFiles$'
   ```

5. Remove `UPDATE_SNAPSHOTS` from the environment and rerun the same test without update mode.

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
