## Workflow

### Step 1 — Identify the PR

Determine the target PR number. Use one of these sources (in priority order):

1. **Explicit user input** — the user provides a PR number or URL.
2. **Current branch** — run `gh pr view --json number` to find the PR for the
   checked-out branch.

If no PR can be identified, ask the user for the PR number.

### Step 2 — Fetch PR Details and Check for Existing Linked Issues

Use `gh pr view <number> --json number,title,body,url` to retrieve the PR's
title, body, and URL.

Before creating an issue, check whether one is already linked using the same
source the CI governance gate uses — the GraphQL `closingIssuesReferences` API:

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

### Step 4 — Confirm with User

Present the drafted issue title and body to the user via `ask_user`.
Ask whether to proceed, modify, or cancel.

### Step 5 — Create the Issue

Run `gh issue create --repo <owner/repo> --title "<title>" --body "<body>"`
to create the issue. Capture the new issue number from the output.

### Step 6 — Link the PR

Append `\n\nFixes #<issue_number>` to the existing PR body using
`gh pr edit <number> --body "<updated_body>"`.

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
