---
name: tool-version-upgrade
description: >-
  **WORKFLOW SKILL** — Upgrades bundled CLI tool versions (GitHub CLI or Bicep CLI) and pinned tool versions to the latest stable upstream release in azd.
  Supported targets: GitHub CLI (Go source), Bicep CLI (Go source + lint workflow), and GitHub Actions referenced in `.github/workflows/*.yml` (audit + bulk bump).
  Fetches the latest release(s) upstream, compares with the current pinned version(s), 
  confirms with the user, creates a tracking issue, updates the references in source code, GitHub Action workflows, and CI workflows, 
  and opens a PR.

  INVOKES: GitHub MCP tools, gh CLI, git CLI, go build, ask_user.

  USE FOR: upgrade github cli, update gh cli, update github cli version,
  upgrade gh to latest, bump gh cli, gh cli update, update gh tool version,
  upgrade bicep cli, update bicep cli, update bicep version,
  upgrade bicep to latest, bump bicep, bicep cli update, update bicep tool version,
  upgrade tool version, update tool version,
  upgrade github actions, update github actions, bump github actions,
  audit github actions versions, update workflow actions, check actions versions,
  upgrade actions/checkout, upgrade actions/setup-node, upgrade actions/setup-python,
  upgrade actions/setup-go, upgrade actions/github-script, upgrade golangci-lint-action,
  refresh workflow action versions, gh actions version bump.

  DO NOT USE FOR: code review (use code-review), changelog generation (use changelog-generation),
  deploying releases, publishing extensions to registry.
---

# tool-version-upgrade

**WORKFLOW SKILL** — Upgrades a bundled CLI tool version in azd to the latest upstream release.

INVOKES: GitHub MCP tools, `gh` CLI, `git` CLI, `go build`, `ask_user`.

## Supported Tools

| Parameter | GitHub CLI | Bicep CLI | GitHub Actions (workflows) |
|-----------|-----------|-----------|----------------------------|
| Tool name | GitHub CLI | Bicep CLI | GitHub Actions |
| Tool slug | `gh-cli` | `bicep-cli` | `gh-actions` |
| Upstream repo | `cli/cli` | `Azure/bicep` | (per-action; e.g. `actions/checkout`, `actions/setup-node`, `golangci/golangci-lint-action`) |
| Go version file | `cli/azd/pkg/tools/github/github.go` | `cli/azd/pkg/tools/bicep/bicep.go` | n/a |
| Version variable | `var Version semver.Version = semver.MustParse("{version}")` | same | n/a — versions are pinned per `uses:` line in YAML |
| Files to update | 1 file (see below) | 2 files (see below) | every `.github/workflows/*.yml` containing an outdated `uses:` reference (see below) |

### GitHub CLI — Files to Update

Only **one file**:

1. **`cli/azd/pkg/tools/github/github.go`**
   - Update the `Version` variable: `var Version semver.Version = semver.MustParse("{new_version}")`
   - Update the example comment on the download URL line if present (e.g., change the version in
     `// example: https://github.com/cli/cli/releases/download/v{version}/gh_{version}_linux_arm64.tar.gz`)

### Bicep CLI — Files to Update

**Two files** (both must be updated together):

1. **`cli/azd/pkg/tools/bicep/bicep.go`**
   - Update the `Version` variable: `var Version semver.Version = semver.MustParse("{new_version}")`

2. **`.github/workflows/lint-bicep.yml`**
   - Update the download URL in the "Upgrade bicep" step:
     ```yaml
     sudo curl -o $(which bicep) -L https://github.com/Azure/bicep/releases/download/v{new_version}/bicep-linux-x64
     ```

> **⚠️ Important** (Bicep only): Both files must be updated together to keep Go code and CI workflow
> in sync. Forgetting the workflow file is a common mistake — always verify both are changed.

### GitHub Actions (workflows) — Files to Update

**N files** — every `.github/workflows/*.yml` containing an outdated `uses:` reference. Scope is
limited to that directory; do not edit local actions, reusable workflows in this repo, or
devcontainer feature definitions.

For each `uses: owner/repo@ref` line, classify and act:

| Bucket | Pattern | Action |
|--------|---------|--------|
| Major-tag pin | `uses: owner/repo@vN` (N is an integer) | Bump to latest stable major (`@vM`). |
| Exact-version pin | `uses: owner/repo@vX.Y.Z` or `@X.Y.Z` | Bump to latest stable major tag (`@vM`). |
| SHA pin | `uses: owner/repo@<40-hex-sha> # vN` | Resolve the SHA the latest major tag points to, replace **both** the SHA and the trailing `# vN` comment so they stay paired. **Never** demote a SHA pin to a bare tag. |
| Branch ref | `uses: owner/repo@main` (or any non-version ref) | **Skip**, list as warning. Branch refs are usually intentional. |
| Local action | `uses: ./...` | **Skip** silently. |
| Local reusable workflow | `uses: ./.github/workflows/...` | **Skip** silently. |

> **⚠️ Important** (GitHub Actions only): one run produces **one tracking issue and one PR**
> covering every outdated reference, grouped by action. Do not open one PR per action.

> **⚠️ Supply-chain hardening**: SHA-pinned references exist on purpose — typically for workflows
> that gate releases or extension approvals (e.g., `approval-ext-azure-ai-agents.yml`). 

## Workflow

### Step 1 — Identify Tool

If the user's request doesn't specify which tool, ask via `ask_user`:

> Which tool would you like to upgrade?

Choices:
- **GitHub CLI**
- **Bicep CLI**
- **GitHub Actions (workflow files)**

Use the corresponding column from the **Supported Tools** table for all subsequent steps.

### Step 2 — Fetch Latest Release

```bash
gh release view --repo {upstream_repo} --json tagName,name,publishedAt,body
```

The tag format is `v{version}` (e.g., `v2.86.0` or `v0.41.2`). Strip the `v` prefix to get the semver.

After stripping the `v` prefix, **validate the version is strict semver** (no pre-release suffixes):

```bash
echo "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "Unexpected version format: $version"; exit 1; }
```

If the latest release is a pre-release (e.g., `v0.42.0-rc1`), **stop and warn the user** — do not
use pre-release versions. Suggest the user wait for the stable release or specify a version manually.

If `gh release view` fails, fall back to:
```bash
gh api repos/{upstream_repo}/releases/latest --jq '.tag_name'
```

### Step 3 — Read Current Version

```bash
grep -n 'var Version semver.Version' {go_version_file}
```

Extract the version string from the `semver.MustParse("X.Y.Z")` call.

**Bicep only** — also verify the CI workflow version matches:

```bash
grep -n 'bicep/releases/download' .github/workflows/lint-bicep.yml
```

If the two versions don't match, warn the user before proceeding.

### Step 4 — Compare & Confirm Upgrade

1. If the current version **equals** the latest release, inform the user and **stop** — no issue,
   branch, or PR needed:
   > ✅ {tool_name} is already at the latest version (**{version}**). No update needed.

2. If the current version is **newer** than the latest release, **stop and warn** — this likely
   means the latest GitHub release is not yet stable, or the pinned version was set manually:
   > ⚠️ The current {tool_name} version (**{current_version}**) is newer than the latest release
   > (**{latest_version}**). This may indicate a pre-release pin or a release rollback. No action taken.

3. If the latest is newer, present the upgrade summary via `ask_user`:

   > The current {tool_name} version is **{current_version}**.
   > The latest release is **{latest_version}** (released {release_date}).
   >
   > Proceed with upgrade?

   Choices:
   - **Yes, upgrade to {latest_version}** *(Recommended)*
   - **No, cancel**

### Step 5 — Final Confirmation Gate

> **⚠️ MANDATORY — even in autopilot / auto-approve / yolo mode.**
> This confirmation MUST use `ask_user` and MUST NOT be skipped regardless of agent autonomy settings.
> This is a safety gate to prevent false-positive upgrades (e.g., wrong version parsed, pre-release
> tag picked up).

Present a full summary via `ask_user`:

> **Ready to apply {tool_name} upgrade**
>
> - Current version: **{current_version}**
> - Target version: **{latest_version}**
> - Upstream release: {release_url}
> - Files to modify:
>   {files_list_with_paths}
>
> This will:
> 1. Create a tracking issue in Azure/azure-dev
> 2. Create a new branch from `origin/main`
> 3. Apply the version changes
> 4. Open a PR
>
> Confirm to proceed?

Choices:
- **Yes, apply upgrade** *(Recommended)*
- **No, cancel**

If the user cancels, stop immediately — do not create the issue, branch, or PR.

### Step 6 — Create Clean Branch, Apply Changes & Build

Per [references/tool-upgrade-workflow.md](references/tool-upgrade-workflow.md) § Create Clean Branch from origin/main.

1. Verify the working tree is clean (`git status --porcelain`). If dirty, **stop** and warn:
   > Your working tree has uncommitted changes. Please commit or stash them before running
   > this skill, so the upgrade PR contains only the version bump.

   Do NOT proceed with dirty state — do not use `git stash` automatically.

2. Delete any stale branch from a previous cancelled run, then create the branch from `origin/main`:
   ```bash
   git fetch origin main
   git branch -D update/{tool_slug}-{latest_version} 2>/dev/null || true
   git checkout -b update/{tool_slug}-{latest_version} origin/main
   ```

3. Verify zero commits ahead of origin/main:
   ```bash
   git --no-pager log --oneline origin/main..HEAD
   ```
   Must produce no output. If it shows any commits, abort.

4. Apply the file edits:

   **GitHub CLI**: Edit `cli/azd/pkg/tools/github/github.go`:
   - Replace version in `semver.MustParse("{old}")` with the new version.
   - Update any example URL comments that reference the old version.

   **Bicep CLI**: Edit both files:
   - `cli/azd/pkg/tools/bicep/bicep.go` — replace version in `semver.MustParse("{old}")`.
   - `.github/workflows/lint-bicep.yml` — replace old version in the curl download URL.

5. Build and verify:
   ```bash
   cd cli/azd && go build ./...
   ```
   If the build fails, **delete the branch, report the error, and stop**.
   Do NOT create an issue or PR for a broken build.

6. **Bicep only** — verify both files have the new version:
   ```bash
   grep 'MustParse' cli/azd/pkg/tools/bicep/bicep.go | head -1
   grep 'bicep/releases/download' .github/workflows/lint-bicep.yml
   ```

7. Stage **only** the expected files (do NOT use `git add -A`):

   **GitHub CLI**:
   ```bash
   git add cli/azd/pkg/tools/github/github.go
   ```

   **Bicep CLI**:
   ```bash
   git add cli/azd/pkg/tools/bicep/bicep.go .github/workflows/lint-bicep.yml
   ```

8. Verify nothing unexpected is staged:
   ```bash
   git --no-pager diff --cached --stat
   ```
   Output must show ONLY the expected files. If unexpected files appear, abort.

### Step 7 — Create Tracking Issue

> **Note**: The issue is created **after** the build succeeds to avoid orphan issues when the
> build or staging validation fails.

```bash
gh issue create \
  --repo Azure/azure-dev \
  --title "Update {tool_name} version to {latest_version}" \
  --body "{issue_body}"
```

**GitHub CLI issue body:**

```markdown
Update GitHub CLI version from {current_version} to {latest_version}.

Release: https://github.com/cli/cli/releases/tag/v{latest_version}

### Files to update

- [ ] `cli/azd/pkg/tools/github/github.go` — update `Version` variable to `{latest_version}`
```

**Bicep CLI issue body:**

```markdown
Update Bicep CLI version from {current_version} to {latest_version}.

Release: https://github.com/Azure/bicep/releases/tag/v{latest_version}

### Files to update

- [ ] `cli/azd/pkg/tools/bicep/bicep.go` — update `Version` variable to `{latest_version}`
- [ ] `.github/workflows/lint-bicep.yml` — update download URL to `v{latest_version}`
```

Capture the issue number from the output.

### Step 8 — Commit & PR

```bash
git commit -m "Update {tool_name} to v{latest_version}" \
  -m "Fixes #{issue_number}"
git push -u origin update/{tool_slug}-{latest_version}
gh pr create \
  --repo Azure/azure-dev \
  --title "Update {tool_name} to v{latest_version}" \
  --body "{pr_body}" \
  --base main
```

**GitHub CLI PR body:**

```markdown
Updates the bundled GitHub CLI version from {current_version} to {latest_version}.

Fixes #{issue_number}

Release: https://github.com/cli/cli/releases/tag/v{latest_version}

## Changes

- Bumped `Version` constant in `cli/azd/pkg/tools/github/github.go`
```

**Bicep CLI PR body:**

```markdown
Updates the bundled Bicep CLI version from {current_version} to {latest_version}.

Fixes #{issue_number}

Release: https://github.com/Azure/bicep/releases/tag/v{latest_version}

## Changes

- Bumped `Version` constant in `cli/azd/pkg/tools/bicep/bicep.go`
- Updated download URL in `.github/workflows/lint-bicep.yml`
```

### Post-Creation

Present a summary to the user:

> ✅ Done!
> - Issue: #{issue_number} — {issue_url}
> - PR: #{pr_number} — {pr_url}
> - Version: {current_version} → {latest_version}

---

## GitHub Actions (workflows) — Step Overrides

When the selected tool is **GitHub Actions**, Steps 2, 3, 4, 6, 7, and 8 differ from the
single-tool flow above. Steps 1 (Identify Tool) and 5 (Final Confirmation Gate) work the
same way. The branch slug is `gh-actions-versions` (no version suffix, since this run
covers many actions at once).

### Step 2 (override) — Inventory References

Scan every workflow file:

```bash
git grep -nE '^\s*uses:\s+[^ ]+' -- '.github/workflows/*.yml'
```

Parse each line into one of the buckets in **GitHub Actions (workflows) — Files to Update**.
Deduplicate by `(owner/repo, current_ref)` so each unique reference is fetched only once.

### Step 3 (override) — Fetch Latest Stable Major per Action

For each unique `owner/repo`:

```bash
gh release view --repo {owner}/{repo} --json tagName,name,publishedAt,isPrerelease
```

Treat the leading integer of `tagName` as the latest **major** (e.g., `v6.0.1` → `v6`).
If the latest release is a pre-release (`isPrerelease: true`), fall back to:

```bash
gh release list --repo {owner}/{repo} --limit 20 --json tagName,isPrerelease \
  | jq -r '[.[] | select(.isPrerelease==false)][0].tagName'
```

If the tag does not match `v[0-9]+(\.[0-9]+\.[0-9]+)?`, **skip** this action and add it
to the warnings list — do not guess.

For SHA-pinned references, additionally resolve the commit SHA the major tag currently
points to:

```bash
gh api repos/{owner}/{repo}/git/refs/tags/v{major} --jq '.object.sha'
```

If `.object.type == "tag"` (annotated tag), follow once:

```bash
gh api repos/{owner}/{repo}/git/tags/{sha} --jq '.object.sha'
```

### Step 4 (override) — Compute Diff & Compare

For each parsed reference, compute the desired new value per the buckets table. If
`new == current`, drop from the change list.

If the change list is **empty** after this step, inform the user and **stop** — no
issue, branch, or PR needed:

> ✅ All GitHub Actions references are already at the latest stable major. No updates needed.

If any reference has a cross-major jump (e.g., `v4 → v6` skipping `v5`), record a
**breaking-change advisory** for the Step 5 confirmation prompt so the user can
spot-check release notes before approving.

### Step 5 (Final Confirmation Gate — adapted)

Use the standard gate, but the summary must show:

- Outdated references grouped by action with `current → new` and the affected file count.
- Skipped entries (branch refs, unparseable tags, pre-release-only repos).
- Cross-major-jump advisories.
- The full list of files that will be modified.

Example summary body:

> **GitHub Actions upgrade — ready to apply**
>
> | Action | Current | New | Files |
> |---|---|---|---|
> | actions/checkout | v4 | v6 | 14 |
> | actions/setup-node | v4 | v6 | 7 |
> | actions/github-script | v7 (SHA-pinned) | v9 (SHA-pinned) | 1 |
>
> Skipped (branch refs): `actions/stale@main` in `stale-issues.yml`
>
> ⚠️ Cross-major jumps: `actions/checkout v4 → v6` (skips v5).

### Step 6 (override) — Branch & Apply Edits

Use the standard clean-branch flow but with a fixed slug:

```bash
git fetch origin main
git branch -D update/gh-actions-versions 2>/dev/null || true
git checkout -b update/gh-actions-versions origin/main
git --no-pager log --oneline origin/main..HEAD   # must be empty
```

Apply edits with these constraints:

- Replace **only** on the parsed `uses:` lines. Never use a project-wide find-and-replace.
- For SHA-pinned references, replace **both** the SHA and the trailing `# vN` comment in
  a single edit so they stay paired.
- Preserve indentation and surrounding YAML exactly.

After all edits, sanity-check the diff for accidental SHA-pin demotion:

```bash
git --no-pager diff -- '.github/workflows/*.yml' | grep -E '^[-+].*uses:'
```

If a `-uses: owner/repo@<sha> # vN` line appears paired with a `+uses: owner/repo@vM`
**without** an SHA, **abort** and report.

### Step 7 (override) — Validation & Staging

There is no Go build for workflow-only edits. Validate YAML parses:

```bash
for f in .github/workflows/*.yml; do
  python -c "import sys, yaml; yaml.safe_load(open(sys.argv[1]))" "$f"
done
```

If any file fails to parse, **abort and revert** the edits to that file. If every
file fails, abort the whole run.

If `actionlint` happens to be installed locally, run it as an extra check:

```bash
actionlint .github/workflows/*.yml || true
```

Do NOT install `actionlint` automatically — the project's own lint workflows will
catch issues on the PR.

Stage explicitly — never `git add -A`:

```bash
git add .github/workflows/*.yml
git --no-pager diff --cached --stat
```

The diff stat must contain ONLY paths under `.github/workflows/`. If anything else
is staged, **abort**.

### Step 8 (override) — Issue, Commit & PR

**Issue title:** `Update GitHub Actions to latest stable versions`

**Issue body:**

```markdown
Audit and bump GitHub Actions referenced in `.github/workflows/*.yml` to the latest
stable major versions.

### Upgrades

{table_of_action_to_current_to_new_to_file_count}

### Skipped

{list_of_skipped_branch_refs_or_unparseable_tags}

### Files modified

{bullet_list_of_workflow_files}
```

**Commit & push:**

```bash
git commit -m "Update GitHub Actions to latest stable versions" \
  -m "Fixes #{issue_number}"
git push -u origin update/gh-actions-versions
gh pr create \
  --repo Azure/azure-dev \
  --title "Update GitHub Actions to latest stable versions" \
  --body "{pr_body}" \
  --base main
```

**PR body:**

```markdown
Bump all GitHub Actions referenced in workflow files to their latest stable major versions.

Fixes #{issue_number}

## Upgrades

{table_of_action_to_current_to_new_to_file_count}

## Notes

- Major-tag pins (`@vN`) preserved.
- SHA pins (`@<sha> # vN`) preserved as SHA pins, with both the SHA and the `# vN`
  comment updated together to stay in sync.
- Branch refs (e.g., `@main`) intentionally skipped.
- Cross-major jumps (where applicable) called out below; release notes should be
  reviewed before merge.

{breaking_change_callouts_if_any}
```

### Post-Creation (GitHub Actions)

> ✅ Done!
> - Issue: #{issue_number} — {issue_url}
> - PR: #{pr_number} — {pr_url}
> - Actions bumped: {count}
> - Files modified: {file_count}

### Out of Scope (GitHub Actions)

- Pinning previously unpinned actions to SHAs. (Use the existing convention: bare
  major tags by default; SHAs only where the file already SHA-pins.)
- Bumping the Go toolchain inside `setup-go` `with: go-version:` — that's a separate
  workflow (`validate-go-version`).
- Bumping Node / Python / Bicep tool *runtime* versions inside `setup-*` `with:` blocks.
- Updating actions used outside `.github/workflows/` (e.g., devcontainer features).

If the user asks for any of the above, tell them this skill does not cover it.

---

## Error Handling

- `gh release view` fails → fall back to `gh api repos/{upstream_repo}/releases/latest`
- Already up to date → inform user, stop
- Working tree dirty → stop, tell user to commit/stash first
- Build fails after version change → revert, report error, stop
- Version mismatch between `bicep.go` and `lint-bicep.yml` before upgrade → warn user, ask which to use as baseline
- Unexpected files staged → abort, report what was found
- Issue or PR creation fails → report error, provide manual commands
- **GitHub Actions only** — `gh release view` fails for an action → fall back to
  `gh release list` and pick the newest non-prerelease; if still none, skip the action
  and add it to warnings
- **GitHub Actions only** — YAML parse failure after edits → revert that file, drop
  it from the change list, continue with the rest. If every file fails, abort the run.
- **GitHub Actions only** — a SHA pin demoted to a bare tag pin detected in the diff
  → abort, report the file/line, do not commit
- **GitHub Actions only** — a major tag (e.g., `@v3`) no longer exists upstream because
  the action was renamed/archived → skip and add to warnings; never silently rewrite to
  a different `owner/repo`
