# PR Documentation Monitor

A GitHub Action that analyzes pull request changes and identifies which documentation needs to be created, updated, or deleted -- both within the repository and in external documentation repos.

## How It Works

1. **Triggers** on PR events (opened, updated, merged, closed) or manual dispatch
2. **Extracts** the PR diff and classifies changes (API, behavior, config, feature, etc.)
3. **Inventories** documentation in both `Azure/azure-dev` and `MicrosoftDocs/azure-dev-docs`
4. **Analyzes** the changes using GitHub Models AI (GPT-4o) to determine doc impact
5. **Creates companion PRs** in the appropriate repos with branch naming `docs/pr-{N}`
6. **Posts a tracking comment** on the source PR linking to all companion doc PRs

## Configuration

### Required Secrets

| Secret | Description |
|--------|-------------|
| `DOCS_REPO_PAT` | GitHub PAT with `repo` scope for `MicrosoftDocs/azure-dev-docs`. Required for creating companion PRs in the external docs repo. Without it, the action can still scan the public docs repo for inventory and report impacts, but cannot create PRs there. |

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
