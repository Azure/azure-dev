---
name: tool-version-upgrade
description: >-
  **WORKFLOW SKILL** — Upgrades bundled CLI tool versions (GitHub CLI or Bicep CLI) in azd.
  Fetches the latest release from the upstream repo, compares with the current pinned version,
  confirms with the user, creates a tracking issue, updates version references in source code
  and CI workflows, and opens a PR.

  INVOKES: GitHub MCP tools, gh CLI, git CLI, go build, ask_user.

  USE FOR: upgrade github cli, update gh cli, update github cli version,
  upgrade gh to latest, bump gh cli, gh cli update, update gh tool version,
  upgrade bicep cli, update bicep cli, update bicep version,
  upgrade bicep to latest, bump bicep, bicep cli update, update bicep tool version,
  upgrade tool version, update tool version.

  DO NOT USE FOR: code review (use code-review), changelog generation (use changelog-generation),
  deploying releases, publishing extensions to registry.
---

# tool-version-upgrade

**WORKFLOW SKILL** — Upgrades a bundled CLI tool version in azd to the latest upstream release.

INVOKES: GitHub MCP tools, `gh` CLI, `git` CLI, `go build`, `ask_user`.

## Supported Tools

| Parameter | GitHub CLI | Bicep CLI |
|-----------|-----------|-----------|
| Tool name | GitHub CLI | Bicep CLI |
| Tool slug | `gh-cli` | `bicep-cli` |
| Upstream repo | `cli/cli` | `Azure/bicep` |
| Go version file | `cli/azd/pkg/tools/github/github.go` | `cli/azd/pkg/tools/bicep/bicep.go` |
| Version variable | `var Version semver.Version = semver.MustParse("{version}")` | same |
| Files to update | 1 file (see below) | 2 files (see below) |

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

## Workflow

### Step 1 — Identify Tool

If the user's request doesn't specify which tool, ask via `ask_user`:

> Which tool would you like to upgrade?

Choices:
- **GitHub CLI**
- **Bicep CLI**

Use the corresponding row from the **Supported Tools** table for all subsequent steps.

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

## Error Handling

- `gh release view` fails → fall back to `gh api repos/{upstream_repo}/releases/latest`
- Already up to date → inform user, stop
- Working tree dirty → stop, tell user to commit/stash first
- Build fails after version change → revert, report error, stop
- Version mismatch between `bicep.go` and `lint-bicep.yml` before upgrade → warn user, ask which to use as baseline
- Unexpected files staged → abort, report what was found
- Issue or PR creation fails → report error, provide manual commands
