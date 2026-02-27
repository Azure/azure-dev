# PR Documentation Monitor

A GitHub Action that analyzes pull request changes and identifies which documentation needs to be created, updated, or deleted -- both within the repository and in external documentation repos.

## How It Works

1. **Triggers** on `pull_request_target` events (`opened`, `synchronize`, `reopened`, `closed` -- merges surface as `closed` when the PR is merged) or manual `workflow_dispatch`
2. **Extracts** the PR diff and classifies changes (API, behavior, config, feature, etc.)
3. **Inventories** documentation in both `Azure/azure-dev` and `MicrosoftDocs/azure-dev-docs`
4. **Analyzes** the changes using GitHub Models AI (GPT-4o) to determine doc impact
5. **Creates companion PRs** in the appropriate repos with branch naming `docs/pr-{N}`
6. **Posts a tracking comment** on the source PR linking to all companion doc PRs

## Configuration

### Prerequisites: GitHub App for Cross-Repo Access

The doc-monitor creates companion PRs in `MicrosoftDocs/azure-dev-docs`. To authenticate cross-repo operations, a **GitHub App** is used instead of a long-lived PAT.

The workflow uses [`actions/create-github-app-token@v1`](https://github.com/actions/create-github-app-token) to mint a short-lived token (valid ~1 hour) scoped to the `azure-dev-docs` repository.

**Required secrets:**

| Secret | Description |
|--------|-------------|
| `DOC_MONITOR_APP_ID` | Application ID of the GitHub App |
| `DOC_MONITOR_APP_PRIVATE_KEY` | Private key (PEM) for the GitHub App |

**Required GitHub App permissions:**

| Permission | Level | Purpose |
|------------|-------|---------|
| `contents` | `write` | Create branches and push commits in the docs repo |
| `pull_requests` | `write` | Create and update companion PRs in the docs repo |

The App must be installed on the `MicrosoftDocs` organization with access to the `azure-dev-docs` repository.

> **Without the token**, the action can still scan the public docs repo for inventory and report impacts, but cannot create PRs there.

### Trigger: `pull_request_target`

The workflow uses `pull_request_target` instead of `pull_request` for security. This ensures the workflow code always runs from the **base branch** (main), not from the fork's PR branch. This prevents fork PRs from modifying the workflow to exfiltrate secrets. The action reads PR data via the GitHub API only — it never checks out or executes code from the PR branch.

### Workflow Permissions

The workflow requires these permissions (already configured in `doc-monitor.yml`):

- `contents: write` -- create branches and commits
- `pull-requests: write` -- create PRs and comments
- `models: read` -- access GitHub Models AI

## Usage

### Automatic (PR Events)

The workflow runs automatically on PR events targeting `main`. No action needed.

### Manual Trigger

#### Single PR

Run the workflow manually from the Actions tab with:
- **mode**: `single`
- **pr_number**: the PR number to analyze

#### All Open PRs

- **mode**: `all_open`
- Analyzes every open PR targeting `main`

#### Specific List

- **mode**: `list`
- **pr_list**: comma-separated PR numbers (e.g., `123,456,789`)

## Branch Naming

Companion doc PR branches follow the pattern `docs/pr-{source-pr-number}`, ensuring:
- 1:1 mapping between source and doc PRs
- Idempotent re-runs (branch is updated, not recreated)
- Easy identification of related PRs

## Respecting Human Edits

The action never force-pushes to doc PR branches. If a human has made commits:
- New analysis results are committed on top of existing commits
- Conflicts are flagged in the tracking comment rather than overwritten

## Development

```bash
cd .github/actions/doc-monitor
npm install
npm run typecheck   # Type check only
npm run build       # Build dist/index.js with ncc
```

The compiled `dist/index.js` must be committed for the action to work in GitHub Actions.
