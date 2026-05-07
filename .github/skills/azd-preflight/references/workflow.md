# Workflow

### Step 1 — Verify Prerequisites

1. Locate the repo root by checking for `cli/azd/magefile.go` relative to cwd.
   If cwd is already `cli/azd/` (contains `magefile.go`), use cwd directly.
   If cwd is the repo root, use `cli/azd/` as the working directory.
2. Verify tools: `mage`, `go`, `golangci-lint`, `cspell`.
3. If any tool is missing, offer to install via `ask_user` (see preflight-checks.md § Prerequisites).
   If the user declines, stop.

### Step 2 — Run Preflight

Run from the `cli/azd/` directory (adjust based on cwd detected in Step 1):

```bash
# If at repo root:
cd cli/azd && mage preflight
# If already in cli/azd/:
mage preflight
```

**Important**: This command can take 10+ minutes (unit tests alone can take up to 10 minutes).
Use an appropriate timeout (at least 15 minutes).

Capture the full output including the summary table at the end.

### Step 3 — Parse Results

The preflight output ends with a summary block listing each check as `✓` (pass) or `✗` (fail):

```
══════════════════════════
  Preflight Summary
══════════════════════════
  ✓ gofmt        ✗ lint
  ✓ cspell        ...
══════════════════════════
```

Parse this summary to identify which checks passed (`✓`) and which failed (`✗`).

**If all checks pass**: Report success and stop. No fixes needed.

**If any checks fail**: Proceed to Step 4 for each failing check, in order.

### Step 4 — Fix Failures (Iterative)

For each failing check, apply the fix strategy from the references. Process checks in
their original order (1-8) because earlier fixes can resolve later failures (e.g., `gofmt`
fixes may resolve `lint` issues, `build` fixes resolve `test` failures).

{{ references/fix-strategies.md }}

### Step 5 — Re-run Preflight

After applying all fixes, re-run the full preflight:

```bash
cd cli/azd && mage preflight
```

**If all checks pass**: Report success and stop.

**If failures remain**: Return to Step 4 for the remaining failures. This is an iterative
loop — continue until either:
- All 8 checks pass, OR
- 3 full cycles have been attempted without progress on a specific check

### Step 6 — Escalate if Stuck

If after 3 cycles a check still fails with the same error, report the persistent failure
with full context and ask via `ask_user` whether to: show the failing code for guided fix,
skip the check, or abort preflight.
