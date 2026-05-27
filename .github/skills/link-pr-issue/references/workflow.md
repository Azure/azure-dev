## Workflow

### Step 1 — Identify the PR

Determine the target PR number **and repository**. Use one of these sources
(in priority order):

1. **Explicit user input** — the user provides a PR URL (e.g.,
   `https://github.com/OWNER/REPO/pull/123`). Extract the owner, repo, and
   number from the URL.
2. **Explicit PR number** — the user provides just a number. Resolve the
   repository from the current checkout: `gh repo view --json owner,name`.
3. **Current branch** — run `gh pr view --json number,url` to find the PR
   for the checked-out branch. Extract owner/repo from the URL.

If no PR can be identified, ask the user for the PR number or URL.

**Carry the resolved `OWNER`, `REPO`, and `PR_NUMBER` through all subsequent
steps.** Always pass `--repo OWNER/REPO` to `gh` commands so they work
regardless of the current working directory.

### Step 2 — Fetch PR Details and Check Skip Conditions

Use `gh pr view <PR_NUMBER> --repo OWNER/REPO --json number,title,body,url,isDraft,author,labels`
to retrieve the PR metadata.

**Before creating an issue, check whether the PR is exempt from governance.**
The CI governance gate (`.github/scripts/pr-governance-issue-check.js`) skips
PRs that match any of these conditions:

1. **Draft PRs** — `isDraft` is `true`.
2. **Automated authors** — author login is `dependabot[bot]`, `dependabot`,
   `app/dependabot`, or `azure-sdk`.
3. **Skip label** — the PR carries the `skip-governance` label.

If any skip condition applies, inform the user the PR is exempt from governance
and stop — no issue is needed.

**Then check whether an issue is already linked** using the same source the CI
governance gate uses — the GraphQL `closingIssuesReferences` API:

```bash
gh api graphql -f query='query {
  repository(owner: "OWNER", name: "REPO") {
    pullRequest(number: PR_NUMBER) {
      closingIssuesReferences(first: 10) {
        nodes { number title url }
      }
    }
  }
}'
```

This covers issues linked via:
- Closing keywords in the PR body (`Fixes #`, `Closes #`, `Resolves #`)
- The GitHub sidebar "Linked issues" UI
- Commit messages with closing keywords

If `closingIssuesReferences.nodes` is non-empty, an issue is already linked.
Inform the user which issue(s) are linked and stop — no action needed.

### Step 3 — Draft the Issue

Compose an issue with:

- **Title**: Derived from the PR title. Keep it concise and action-oriented.
  If the PR title is already descriptive, reuse it. Otherwise, summarize.
- **Body**: Include:
  - A one-paragraph summary of the change (derived from the PR body).
  - A `## Linked PR` section referencing the PR number.
  - Any external issue references found in the PR body (e.g., links to
    upstream issues in other repos).

### Step 4 — Confirm Issue Draft with User

Present the drafted issue title and body to the user via `ask_user`.
Ask whether to proceed, modify, or cancel.

### Step 5 — Create the Issue

Write the issue body to a temporary file and use `--body-file` to avoid
shell injection from PR-derived content:

```bash
TMPFILE=$(mktemp)
printf '%s' "$ISSUE_BODY" > "$TMPFILE"
gh issue create --repo OWNER/REPO --title "$TITLE" --body-file "$TMPFILE"
rm "$TMPFILE"
```

Capture the new issue number from the output.

### Step 6 — Confirm and Link the PR

Now that the issue number is known, show the user the exact PR body update:

> The following line will be appended to the PR body:
> `Fixes #<issue_number>`

Ask the user to confirm via `ask_user` before editing the PR.

Once confirmed, write the updated PR body to a temporary file and use
`--body-file` to avoid shell metacharacter issues with user-controlled markdown:

```bash
TMPFILE=$(mktemp)
printf '%s\n\nFixes #%d\n' "$CURRENT_BODY" "$ISSUE_NUMBER" > "$TMPFILE"
gh pr edit "$PR_NUMBER" --repo OWNER/REPO --body-file "$TMPFILE"
rm "$TMPFILE"
```

### Step 7 — Report Success

Inform the user with:
- The new issue URL.
- Confirmation that the PR body was updated.
- A reminder that the CI gate should now pass on the next check.

## Error Handling

- **PR not found**: Ask the user to verify the PR number and repository.
- **Permission denied**: Inform the user they may not have write access to
  the repository. Suggest they check their `gh auth status`.
- **Issue already linked**: Do not create a duplicate. Inform the user which
  issue is already linked and stop.
- **PR body edit fails**: Show the error and suggest the user manually add
  `Fixes #<number>` to the PR description.
