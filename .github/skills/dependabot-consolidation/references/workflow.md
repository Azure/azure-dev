# Dependabot Consolidation Workflow

<!-- cspell:ignore GHSA GHSAs Lockfiles npmjs -->

Use these stable identifiers:

- PR body marker: `<!-- dependabot-consolidation-skill:v1 -->`
- PR title: `chore(deps): consolidate Dependabot updates`
- New branch prefix: `maintenance/dependabot-consolidation-`
- Existing label: `dependencies`

## 1. Verify Context and Access

From the repository root:

1. Verify `git remote get-url origin` resolves to `Azure/azure-dev`.
2. Read `cli/azd/AGENTS.md` in full and any instructions governing changed subprojects.
3. Require `git status --porcelain` to be empty. Do not automatically stash changes.
4. Run `gh auth status`.
5. Verify access before mutation:
   - Read Dependabot alerts.
   - Read PRs and commits.
   - Push repository branches.
   - Comment on and close PRs.

Use the REST API for alerts because the security page is not a stable machine-readable interface:

```bash
gh api --method GET --paginate \
  'repos/Azure/azure-dev/dependabot/alerts?state=open&per_page=100'
```

If alert access is denied, stop. Do not continue using only the visible Dependabot PRs because
that would produce an incomplete security sweep.

## 2. Build the Inventory

Fetch `origin/main`, then collect every open Dependabot PR with paginated REST results:

```bash
gh api --method GET --paginate \
  'repos/Azure/azure-dev/pulls?state=open&per_page=100' \
  --jq '.[] | select(.user.login == "dependabot[bot]")'
```

Fetch files, commits, mergeability, and other required details for each returned PR separately.
Do not use a fixed result limit for the repository-wide inventory.

For each alert, retain:

- Alert number and URL
- GHSA ID, summary, and severity
- `dependency.manifest_path`
- Package ecosystem and name
- Dependency relationship and scope
- Vulnerable range
- First patched version, if one exists

Group duplicate alerts by:

```text
manifest path + ecosystem + package name + first patched version
```

Several GHSAs can therefore be mitigated by one dependency update.

For each Dependabot PR, fetch its exact ordered commit SHAs:

```bash
gh api --method GET --paginate \
  'repos/Azure/azure-dev/pulls/<number>/commits?per_page=100' --jq '.[].sha'
```

Record every file touched by every source PR. Include routine version updates as well as
security updates.

## 3. Find or Plan the Consolidation PR

List open PRs and inspect their bodies locally for the exact marker:

```text
<!-- dependabot-consolidation-skill:v1 -->
```

- Zero matches: plan a new branch named
  `maintenance/dependabot-consolidation-YYYYMMDD` from the latest `origin/main`.
- One match: reuse its head branch and PR.
- Multiple matches: stop and ask which PR to use.

When reusing a PR:

- Verify the head repository is `Azure/azure-dev`.
- Fetch and check out the existing head branch.
- Bring in the latest `origin/main` without rewriting published history. Prefer a normal merge
  when required; do not force-push.
- Preserve previously recorded source PR and alert rows in the PR body.

If there are no open alerts and no open Dependabot PRs:

- Do not create a PR.
- If an existing consolidation PR remains open, leave it unchanged and link it in the result.

## 4. Present the Mandatory Mutation Preview

Before creating a branch, changing files, pushing, editing a PR, commenting, or closing anything,
present one `ask_user` confirmation containing:

- Whether this creates a PR or updates PR `#N`
- Number of open Dependabot PRs
- Number of open alerts and number of grouped fixes
- Source PR table with number, title, and files
- Alert groups with manifest, package, patched version, severity, and alert numbers
- Planned validation commands by project
- Exact number of source PRs that will be closed after a successful push

Choices:

- **Proceed with consolidation** (Recommended)
- **Cancel**

Cancellation must leave GitHub and the working tree unchanged.

## 5. Prepare the Branch

After confirmation:

1. Recheck that the working tree is clean.
2. Fetch `origin/main` and every source PR head.
3. Create the new branch from `origin/main`, or check out the existing consolidation branch.
4. Verify the branch contains no unrelated local commits or file changes.

Do not delete stale local or remote branches automatically. If the intended new branch already
exists without the marked PR, stop and ask the user how to proceed.

## 6. Incorporate Open Dependabot PRs

Process source PRs deterministically by PR number.

For each PR:

1. Verify its base branch is `main`.
2. Fetch its head and exact commit list.
3. Determine whether each commit or equivalent file change is already present.
4. Cherry-pick missing commits in their original order.
5. If a cherry-pick conflicts:
   - Inspect all overlapping dependency changes.
   - Abort only the in-progress cherry-pick, not the whole branch.
   - Reproduce the combined update with the project's existing package manager.
   - Use the newest compatible version that satisfies the source PR and all alert patched-version
     requirements.
   - Commit the resolved combined change with source PR numbers in the message.
6. Record the source PR as incorporated only after its intended versions and files are present.

Never resolve a conflict by taking one side wholesale without checking the other dependency
updates. Lockfiles commonly overlap even when the package updates are independent.

## 7. Mitigate Alerts Not Covered by Source PRs

For every grouped alert not already covered by an incorporated PR:

1. Inspect the manifest and its owning project.
2. Confirm a patched version exists.
3. Use the project's existing ecosystem tool to update the direct or transitive dependency.
4. Regenerate the matching lockfile or checksum file.
5. Before regenerating an npm lockfile, inspect its existing registry and integrity conventions.
   If the owning pipeline uses `create-authenticated-npmrc.yml` or CFSClean network isolation,
   regenerate with that authenticated registry configuration. Preserve feed-produced metadata
   such as omitted `resolved` fields and `sha1-` integrity; never introduce public
   `registry.npmjs.org` URLs into a lockfile that intentionally excludes them.
6. Confirm the target package version is available from the authenticated registry used by CI.
   A version available from the public npm registry but absent from the CI feed is blocked: do not
   include it, do not close its source PR, and record the feed availability issue in the
   consolidation PR.
7. Avoid unrelated broad upgrades. Do not run repository-wide `go mod tidy`, `npm update`, or
   similar commands across projects that are outside the alert's manifest.
8. Verify the resolved version is outside the vulnerable range and is at least the first patched
   version.

Typical ecosystem operations, adjusted to the repository's existing scripts and lockfile:

| Ecosystem | Preferred approach |
|---|---|
| Go modules | Run `go get <module>@<version>` and `go mod tidy` in the affected module only |
| npm | Use the package manager and lockfile already present; update only the affected package |
| GitHub Actions | Preserve SHA pinning when present and update the paired version comment |
| NuGet | Use the existing project or central package-management mechanism |
| Maven | Update the owning `pom.xml` property/dependency and regenerate existing lock data |
| pip | Update the declared constraint and regenerate the existing lock/requirements output |

If no patched version exists, the manifest cannot be located, or a compatible update cannot be
produced, mark the alert group blocked. Do not claim it is mitigated.

## 8. Verify Coverage and Validate Projects

Create a disposition table for every discovered item:

- `incorporated`
- `already incorporated`
- `blocked` with reason
- `not applicable` only with explicit evidence

Before pushing:

1. Verify every incorporated source PR's intended version and file changes.
2. Verify every covered alert group's resolved version is no longer vulnerable.
3. Review `git diff --stat origin/main...HEAD` and the full diff for unrelated changes.
4. Run the smallest existing validation that covers every changed project.
5. When `cli/azd` tests can spawn the CLI, run `go build` first as required by `AGENTS.md`.
6. Do not install a new validator solely for this workflow.

If validation fails, diagnose and fix dependency-caused failures. If a failure cannot be fixed,
do not push or close source PRs. Report the command and blocker.

## 9. Commit and Push

Stage only reviewed dependency files. Never use `git add -A`.

Use a concise commit message such as:

```text
chore(deps): consolidate Dependabot updates
```

Include the repository-required commit trailers. Push normally; never force-push.

## 10. Create or Update the PR

Create a normal, non-draft PR when no marked PR exists. Otherwise update the existing PR body and
title. Add the existing `dependencies` label; do not create labels.

The PR body must contain:

```markdown
<!-- dependabot-consolidation-skill:v1 -->

## Summary

Consolidates open Dependabot updates and mitigates current Dependabot security alerts.

## Source Dependabot PRs

| PR | Update | Status |
|---|---|---|
| #123 | package update | Included |

## Security alerts

| Alerts | Manifest | Package | Patched version | Severity | Status |
|---|---|---|---|---|---|
| #456, #457 | path/to/lockfile | package | 1.2.3 | high | Covered |

## Validation

- `command`

## Blocked items

- None.
```

Link PRs and alerts with full repository URLs. Preserve prior rows when updating the PR, deduplicate
by PR number and alert number, and update their status rather than deleting historical coverage.
Do not state that GitHub alerts are closed before the consolidation PR merges; use `Covered`.

## 11. Supersede Incorporated Dependabot PRs

Only after the consolidation branch is pushed and the PR body accurately records coverage:

1. Re-fetch the list of open Dependabot PRs to avoid acting on stale state.
2. For each still-open PR recorded as incorporated, comment:

   ```text
   This update is included in #<consolidation-pr-number>, which consolidates the repository's
   current Dependabot updates and security fixes. Closing this PR in favor of the consolidated PR.
   ```

3. Close that PR with `gh pr close`.
4. Do not close blocked, skipped, superseded-by-a-newer-PR, or unverified PRs.
5. If commenting or closing fails, continue recording failures but do not claim full completion.

## 12. Final Report

Report:

- Created or updated consolidation PR link
- Count of alert groups covered and blocked
- Dependabot PRs incorporated and closed
- Dependabot PRs left open, with reasons
- Validation failures or GitHub write failures

Do not merge the consolidation PR. Dependabot alerts should close automatically after the fix
merges into the default branch.
