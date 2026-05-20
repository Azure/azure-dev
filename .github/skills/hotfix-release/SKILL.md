---
name: hotfix-release
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; bump minor on new references or fix strategies.
description: >-
  **WORKFLOW SKILL** — Creates a hotfix release branch from an existing release tag,
  cherry-picks specified PRs, bumps version, updates changelog, and pushes the branch.
  Interactive — handles cherry-pick conflicts with user guidance.

  INVOKES: git CLI, gh CLI, GitHub MCP tools, ask_user.

  USE FOR: create hotfix, hotfix release, cherry-pick release, patch release,
  hotfix for azd, emergency release, create hotfix branch, hotfix from tag.

  DO NOT USE FOR: regular releases (use changelog-generation + ADO pipeline),
  changelog only (use changelog-generation), code review (use code-review),
  sprint checks (use sprint-check).
---

# hotfix-release

**WORKFLOW SKILL** — Creates hotfix release branches with cherry-picked fixes.

INVOKES: `git` CLI, `gh` CLI, GitHub MCP tools, `ask_user`.

## Prerequisites

| Tool | Purpose |
|------|---------|
| `git` | Git CLI — push access to Azure/azure-dev |
| `gh` | GitHub CLI — authenticated with repo access |
| `go` | Go toolchain — to verify build after cherry-pick (optional) |

## Preflight

Verify access:

```bash
# Check git remote
git remote -v | grep "Azure/azure-dev"

# Check gh auth
gh auth status

# Check write access
gh api repos/Azure/azure-dev --jq .permissions.push
```

If push is `false`, stop:
> You need write access to Azure/azure-dev to create hotfix branches.

## Workflow

### Step 1 — Parse Request

The user provides:
- **Release version** to hotfix (e.g., "1.24.3" or "v1.24.3")
- **PR numbers** to cherry-pick (e.g., "#8001, #8002" or "8001 8002")

Example prompts:
- "create hotfix for v1.24.3 with PRs #8001, #8002"
- "hotfix 1.24.3 cherry-pick 8001 8002"
- "patch release for 1.24.3"

If either is missing, ask via `ask_user`.

**Version**: ask for the base release version to hotfix:
> Which release version should I create the hotfix from?

**PRs**: ask which PRs to cherry-pick:
> Which merged PRs should be included in the hotfix? (comma-separated numbers)

### Step 2 — Validate Inputs

**Validate the release tag exists:**

```bash
git fetch --tags
git tag -l "azure-dev-cli_${BASE_VERSION}"
```

The tag format is `azure-dev-cli_X.Y.Z` (e.g., `azure-dev-cli_1.24.3`).
If not found, list recent release tags and ask the user to pick:

```bash
git tag -l "azure-dev-cli_*" --sort=-version:refname | head -10
```

**Validate each PR is merged:**

```bash
gh pr view PR_NUMBER --repo Azure/azure-dev --json state,mergeCommit,mergedAt,baseRefName
```

For each PR:
- Must be `state: "MERGED"`
- Must have `baseRefName: "main"` — reject PRs merged into feature branches (they may contain unrelated changes)
- Record `mergeCommit.oid` — this is what we cherry-pick
- If PR is not merged, warn and skip it
- If PR was merged into a non-main branch, warn:
  > PR #NNNN was merged into `BRANCH`, not `main`. Cherry-picking its merge commit may include unrelated changes. Skip this PR?

**Determine if merge commit:**

After fetching, check the parent count of the merge commit SHA:

```bash
git fetch origin MERGE_SHA
git show --no-patch --pretty=%P MERGE_SHA
```

- If 1 parent → squash merge (cherry-pick directly)
- If 2+ parents → merge commit (use `git cherry-pick -m 1 MERGE_SHA`)

**Compute hotfix version:**
- Parse base version X.Y.Z → hotfix version X.Y.(Z+1)
- e.g., 1.24.3 → 1.24.4
- Confirm with user:
  > Hotfix version will be **1.24.4**. Is that correct?

### Step 3 — Create Hotfix Branch

```bash
# Ensure we're up to date
git fetch origin

# Create branch from the release tag
git checkout -b hotfix/azd-HOTFIX_VERSION azure-dev-cli_BASE_VERSION
```

Example: `git checkout -b hotfix/azd-1.24.4 azure-dev-cli_1.24.3`

### Step 4 — Cherry-Pick PRs

For each PR, in the order specified by the user:

```bash
# For squash merges (single commit)
git cherry-pick MERGE_COMMIT_SHA

# For merge commits (multiple commits)
git cherry-pick -m 1 MERGE_COMMIT_SHA
```

**If cherry-pick succeeds**: log success and continue.

**If cherry-pick has conflicts**:
1. Show the conflicting files:
   ```bash
   git diff --name-only --diff-filter=U
   ```
2. Show the conflict content for each file
3. Ask the user via `ask_user`:
   > Cherry-pick of PR #NNNN has conflicts in these files:
   > - path/to/file1.go
   > - path/to/file2.go
   >
   > How should I proceed?

   Choices:
   - **Show conflicts** — I'll display the diff and help resolve
   - **Skip this PR** — abort this cherry-pick, continue with others
   - **Abort hotfix** — cancel the entire hotfix

4. If resolving: help the user edit the conflicting files, then:
   ```bash
   git add .
   git cherry-pick --continue
   ```

**After each cherry-pick**, verify the build still compiles:
```bash
cd cli/azd && go build ./... 2>&1 | head -20
```

If build fails, warn the user and offer to continue or stop.

### Step 5 — Bump Version

Update version files per [references/version-files.md](references/version-files.md):

{{ references/version-files.md }}

### Step 6 — Update Changelog

Add a hotfix section to `cli/azd/CHANGELOG.md`:

```markdown
## X.Y.Z (YYYY-MM-DD) — Hotfix

### Bugs Fixed

- Description from PR #NNNN title [[#NNNN]](https://github.com/Azure/azure-dev/pull/NNNN)
- Description from PR #MMMM title [[#MMMM]](https://github.com/Azure/azure-dev/pull/MMMM)
```

- Use PR titles as entry descriptions
- Place the new section **above** the previous release section
- Use today's date
- Mark as "Hotfix" in the header

Present the changelog entry to the user for review before writing:
> Here's the changelog entry I'll add. Look good?

### Step 7 — Commit & Push

```bash
git add -A
git commit -m "Release hotfix azd X.Y.Z

Cherry-picked fixes:
- PR #NNNN: <title>
- PR #MMMM: <title>

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"

git push origin hotfix/azd-HOTFIX_VERSION
```

### Step 8 — Summary

Print a summary with next steps:

```
✅ Hotfix branch created and pushed!

  Branch:  hotfix/azd-1.24.4
  Compare: https://github.com/Azure/azure-dev/compare/azure-dev-cli_1.24.3...hotfix/azd-1.24.4
  Version: 1.24.4

  Cherry-picked PRs:
    ✅ #8001 — "Fix auth token refresh"
    ✅ #8002 — "Handle nil pointer in deploy"

  Next steps:
    1. Review the branch: git log --oneline azure-dev-cli_1.24.3..hotfix/azd-1.24.4
    2. Trigger the release pipeline (see "Triggering the Release" below)
    3. After release, add the hotfix changelog entry to main's CHANGELOG.md
```

**Do NOT** create a PR to main. The hotfix branch is released directly via the ADO pipeline.

**Do NOT** create tags manually. The release pipeline creates both `azure-dev-cli_X.Y.Z` and `cli/azd/vX.Y.Z` tags automatically.

---

## Triggering the Release

Open the [azure-dev - cli](https://dev.azure.com/azure-sdk/internal/_build?definitionId=4643) pipeline in ADO:

1. Click **Run pipeline**
2. Select the `hotfix/*` branch (e.g., `hotfix/azd-1.24.4`)
3. ✅ Check **"Check to run a release build"**
4. Leave Azure Record Mode as **live** (default)
5. Click **Run**

The pipeline builds, signs, and publishes the release — including GitHub release, tags, storage upload, Chocolatey, WinGet, and Homebrew.

> **Tip**: Set the pipeline variable `Skip.IncrementVersion = true` before running. The default increment job creates a version-bump PR targeting the source branch, which is unnecessary for a hotfix branch.

---

## Post-Release

1. Add the hotfix changelog section (e.g., `## 1.24.4`) to `main`'s CHANGELOG.md so the release history is complete
2. Do **not** merge version file changes back — main's version is already ahead
3. The hotfix branch can be kept for reference or deleted per team preference

---

## Error Handling

- Tag not found → list recent tags, let user pick
- PR not merged → warn, skip, continue with others
- Cherry-pick conflict → interactive resolution (Step 4)
- Build failure after cherry-pick → warn, offer to continue or abort
- Push rejected → first try `git pull --rebase origin BRANCH` to incorporate any remote commits, then retry `git push`. Only suggest `git push --force-with-lease` as a last resort if rebase also fails, and warn the user that force-pushing may overwrite others' work on the branch
- `gh` not authenticated → stop, tell user to run `gh auth login`
- No write access → stop, explain requirement
