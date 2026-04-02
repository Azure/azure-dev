# Common Tool Upgrade Workflow

Shared workflow steps for upgrading bundled CLI tool versions in azd.

## Step-by-Step Workflow

### Fetch Latest Release

Use the `gh` CLI to get the latest release from the upstream repository:

```bash
gh release view --repo {upstream_repo} --json tagName,name,publishedAt,body
```

Parse the tag to extract the semver version (strip leading `v` if present).

If `gh release view` fails, fall back to:
```bash
gh api repos/{upstream_repo}/releases/latest --jq '.tag_name'
```

### Read Current Version

Read the current version from the Go source file that defines it:

```bash
grep -n 'var Version semver.Version' {go_version_file}
```

Extract the version string from the `semver.MustParse("X.Y.Z")` call.

### Compare Versions

1. Compare the current version with the latest upstream release.
2. If they match, inform the user that the tool is already up to date and **stop**.
3. If the latest is newer, present the upgrade summary to the user via `ask_user`.

### Final Confirmation Gate

> **⚠️ MANDATORY — even in autopilot / auto-approve / yolo mode.**
> This confirmation MUST use `ask_user` and MUST NOT be skipped regardless of agent autonomy settings.
> This is a safety gate to prevent false-positive upgrades (e.g., wrong version parsed, pre-release
> tag picked up).

Before creating the issue, branch, or PR, present a full summary to the user via `ask_user`
showing: current version, target version, release URL, exact files to modify, and actions to
be taken.

If the user cancels, stop immediately — do not create the issue, branch, or PR.

### Create Tracking Issue

Create an issue in `Azure/azure-dev` using the `gh` CLI:

```bash
gh issue create \
  --repo Azure/azure-dev \
  --title "Update {tool_name} version to {latest_version}" \
  --body "{issue_body}"
```

After creation, capture the issue number from the output.

### Create Clean Branch from origin/main

> **⚠️ CRITICAL**: The branch MUST be created from a clean, up-to-date `origin/main`.
> Never branch from the user's current working branch — it may contain unrelated changes
> that would leak into the PR.

1. **Abort if the working tree is dirty** (uncommitted changes unrelated to this upgrade):
   ```bash
   git status --porcelain
   ```
   If there is any output, **stop and warn the user**:
   > Your working tree has uncommitted changes. Please commit or stash them before running
   > this skill, so the upgrade PR contains only the version bump.

   Do NOT proceed with dirty state — do not use `git stash` automatically.

2. **Fetch latest main and create branch from it**:
   ```bash
   git fetch origin main
   git checkout -b update/{tool_slug}-{latest_version} origin/main
   ```
   This creates the branch directly from `origin/main` without switching to local `main` first,
   ensuring no local-only commits are included.

3. **Verify the branch is clean and based on origin/main**:
   ```bash
   git --no-pager log --oneline origin/main..HEAD
   ```
   This MUST produce no output (zero commits ahead of origin/main). If it shows any commits, abort.

4. Apply all file edits (tool-specific — see the SKILL.md for which files).

5. Verify the build compiles:
   ```bash
   cd cli/azd && go build ./...
   ```

6. Stage **only the files that were supposed to be modified** (do not use `git add -A`):
   ```bash
   git add {explicit_file_paths}
   ```
   Then verify nothing unexpected is staged:
   ```bash
   git --no-pager diff --cached --stat
   ```
   The output must show ONLY the expected files. If unexpected files appear, abort.

7. Commit, push, and create the PR.

### Post-Creation

After the PR is created, present a summary to the user:

> ✅ Done!
> - Issue: #{issue_number} — {issue_url}
> - PR: #{pr_number} — {pr_url}
> - Version: {current_version} → {latest_version}
