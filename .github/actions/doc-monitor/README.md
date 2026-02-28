# PR Documentation Monitor

A GitHub Action that analyzes pull request changes and identifies which documentation needs to be created, updated, or deleted -- both within the repository and in external documentation repos.

## How It Works

1. **Triggers** on `pull_request_target` events (`opened`, `synchronize`, `reopened`, `closed` -- merges surface as `closed` when the PR is merged) or manual `workflow_dispatch`
2. **Extracts** the PR diff and classifies changes (API, behavior, config, feature, etc.)
3. **Inventories** documentation in both `Azure/azure-dev` and `MicrosoftDocs/azure-dev-docs-pr`
4. **Analyzes** the changes using GitHub Models AI (GPT-4o) to determine doc impact
5. **Creates companion PRs** in the appropriate repos with branch naming `docs/pr-{N}`
6. **Posts a tracking comment** on the source PR linking to all companion doc PRs

## Configuration

### Prerequisites: OIDC + Key Vault Signing for Cross-Repo Access

The doc-monitor creates companion PRs in `MicrosoftDocs/azure-dev-docs-pr`. To authenticate cross-repo operations, it uses **OIDC + Azure Key Vault signing** to mint GitHub App installation tokens -- the private key never leaves Key Vault.

The workflow:
1. Authenticates to Azure via OIDC (`azure/login@v2` with federated credentials)
2. Signs a JWT using `az keyvault key sign` (non-exportable RSA key in Key Vault)
3. Exchanges the JWT for a short-lived GitHub App installation token

This uses the `eng/common/actions/login-to-github` composite action (adapted from [azure-sdk-tools](https://github.com/Azure/azure-sdk-tools/tree/main/eng/common/scripts/login-to-github.ps1)).

**Required infrastructure (managed by EngSys):**

| Component | Value | Purpose |
|-----------|-------|---------|
| GitHub Environment | `AzureSDKEngKeyVault` | OIDC federated credential binding |
| Azure Key Vault | `azuresdkengkeyvault` | Hosts the non-exportable RSA signing key |
| Key Vault Key | `azure-sdk-automation` | RSA key used to sign GitHub App JWTs |
| GitHub App ID | `1086291` | Azure SDK Automation GitHub App |

**Required GitHub App permissions (on MicrosoftDocs org):**

| Permission | Level | Purpose |
|------------|-------|---------|
| `contents` | `write` | Create branches and push commits in the docs repo |
| `pull_requests` | `write` | Create and update companion PRs in the docs repo |

**Required workflow permissions:**

| Permission | Purpose |
|------------|---------|
| `id-token: write` | Request OIDC token for Azure login |
| `contents: write` | Create branches and commits in azure-dev |
| `pull-requests: write` | Create PRs and comments in azure-dev |
| `models: read` | Access GitHub Models AI |

> **Without the token**, the action can still scan the public docs repo for inventory and report impacts, but cannot create PRs there.

### Trigger: `pull_request_target`

The workflow uses `pull_request_target` instead of `pull_request` for security. This ensures the workflow code always runs from the **base branch** (main), not from the fork's PR branch. This prevents fork PRs from modifying the workflow to exfiltrate secrets. The action reads PR data via the GitHub API only -- it never checks out or executes code from the PR branch.

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
