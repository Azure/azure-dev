# AZD CLI Evaluation & Testing Framework

Measures how well GitHub Copilot CLI interacts with the Azure Developer CLI (`azd`). Inspired by the [microsoft/github-copilot-for-azure](https://github.com/microsoft/github-copilot-for-azure) testing architecture.

## Overview

When a user asks Copilot CLI a question like "deploy my Python app to Azure", the LLM suggests `azd` shell commands (`azd init`, `azd provision`, `azd deploy`). This framework tests:

1. **Does the LLM suggest the right commands?** (text graders)
2. **Are the commands in the right order?** (action_sequence graders)
3. **Do the suggested commands actually succeed?** (Jest unit + human tests validate CLI behavior)
4. **Is the deployed infrastructure correct?** (Python code graders with ARM + HTTP validation, used in E2E lifecycle evals only)

## Architecture

| Component | Purpose | Technology |
|-----------|---------|------------|
| **Waza tasks** (`tasks/`) | LLM evaluation scenarios | [microsoft/waza](https://github.com/microsoft/waza) YAML |
| **Custom graders** (`graders/`) | Azure resource + app validation | Python |
| **Jest unit tests** (`tests/unit/`) | Command structure validation | TypeScript/Jest |
| **Jest human tests** (`tests/human/`) | Human CLI usage baselines | TypeScript/Jest |

## Quick Start

### Prerequisites

- Node.js 20+
- Go (to build azd)
- [Waza CLI](https://github.com/microsoft/waza) (`npm install -g waza`)
- Python 3.x (for custom graders)

### Run Tests

```bash
# Install dependencies
npm install

# Build azd (required for CLI tests)
cd ../../ && go build -o ./azd . && cd test/eval

# Run Jest unit tests (no LLM, no Azure)
npm run test:unit

# Validate Waza task YAML syntax
npm run waza:validate

# Run Waza evals with mock executor (offline, fast)
npm run waza:run:mock

# Run Waza evals with Copilot SDK (requires COPILOT_CLI_TOKEN)
export COPILOT_CLI_TOKEN=<your-token>
npm run waza:run

# Run human usage baseline tests
npm run test:human
```

## How-To Guides

### Adding a Waza LLM Eval Task

Waza tasks are YAML files that define a prompt → grader pipeline. Each task sends a user prompt to the LLM and grades the response.

**Step 1: Pick the right category folder**

| Category | Directory | When to use |
|----------|-----------|-------------|
| `tasks/deploy/` | User wants to deploy something to Azure |
| `tasks/troubleshoot/` | User pastes an error and asks for help |
| `tasks/environment/` | User manages azd environments |
| `tasks/lifecycle/` | Full init→provision→deploy→down workflows |
| `tasks/negative/` | Questions where azd is the **wrong** tool |

**Step 2: Create the YAML file**

```bash
# Example: test that the LLM handles a "wrong SKU" error
touch tasks/troubleshoot/wrong-sku-error.yaml
```

**Step 3: Define the task structure**

Every task file needs these fields:

```yaml
id: troubleshoot-wrong-sku-001    # Unique ID (category-name-NNN)
description: >                    # What this task tests
  User's azd provision failed because the requested VM SKU
  is not available in their region.
inputs:
  prompt: |                       # The exact prompt sent to the LLM
    I ran azd provision and got this error:
    ERROR: deployment failed: The requested VM size Standard_NC6 is not
    available in the current region (eastus2). Available sizes: Standard_D2s_v3,
    Standard_D4s_v3.
    How do I fix this?
  context: |                      # Optional background (not sent to LLM, used by graders)
    User is deploying a GPU workload but picked a region without GPU SKUs.
graders:                          # One or more graders (weights must sum to 1.0)
  - type: text
    weight: 0.4
    config:
      must_match:
        - "(region|location|SKU|size)"
      must_not_match:
        - "I don't know"
  - type: text
    weight: 0.3
    config:
      must_match_any:
        - "different (region|location)"
        - "Standard_D"
        - "az vm list-sizes"
  - type: behavior
    weight: 0.3
    config:
      max_tool_calls: 5
      max_tokens: 2000
```

**Step 4: Validate and test**

```bash
npm run waza:validate                  # Check YAML syntax
npm run waza:run:mock                  # Quick test with mock executor
COPILOT_CLI_TOKEN=<token> npm run waza:run   # Real LLM test
```

### Grader Reference

| Grader | Purpose | Key config fields |
|--------|---------|-------------------|
| `text` | Regex matching on LLM response | `must_match` (all must hit), `must_not_match` (none can hit), `must_match_any` (at least one) |
| `action_sequence` | Verify commands appear in order | `expected_order` (list of commands/patterns) |
| `behavior` | Efficiency constraints | `max_tool_calls`, `max_tokens` |
| `code` | Custom Python validation | `script` (path to grader in `graders/`), `params` (dict passed to script) |

**Grader weights** must sum to 1.0 across all graders in a task. Each grader returns a 0.0–1.0 score, and the weighted average is the task score.

**Regex tips for `text` graders:**
- Patterns are Python regexes, case-insensitive by default
- Use `(a|b|c)` for alternatives: `"azd (up|provision)"`
- Use `must_match` when ALL patterns must appear
- Use `must_match_any` when at least ONE pattern must appear
- Use `must_not_match` as guardrails (e.g., don't suggest destructive commands)

### Adding a Custom Python Grader

Custom graders validate real Azure resources during E2E evals. They live in `graders/` and are referenced from task YAML via the `code` grader type.

**Step 1: Create the grader**

```python
# graders/my_validator.py
import json
import os
import subprocess
import sys
import urllib.request

def get_azure_token():
    """Get Azure access token via CLI or environment."""
    try:
        result = subprocess.run(
            ["az", "account", "get-access-token", "--query", "accessToken", "-o", "tsv"],
            capture_output=True, text=True, check=True
        )
        return result.stdout.strip()
    except Exception:
        return os.environ.get("AZURE_ACCESS_TOKEN")

def grade(context: dict) -> dict:
    """
    Called by Waza with:
      - context: dict containing "params" from the task YAML, plus run metadata

    Must return: {"score": 0.0-1.0, "reason": "explanation"}
    """
    params = context.get("params", {})
    subscription_id = params.get("subscription_id", os.environ.get("AZURE_SUBSCRIPTION_ID"))
    resource_group = params.get("resource_group")
    token = get_azure_token()

    if not token:
        return {"score": 0.0, "reason": "No Azure credentials available"}

    # Make ARM API calls to validate resources...
    url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/resourceGroups/{resource_group}?api-version=2021-04-01"
    )
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        resp = urllib.request.urlopen(req)
        data = json.loads(resp.read())
        return {"score": 1.0, "reason": f"Resource group '{resource_group}' exists"}
    except Exception as e:
        return {"score": 0.0, "reason": str(e)}

if __name__ == "__main__":
    context = json.loads(sys.argv[1]) if len(sys.argv) > 1 else {}
    print(json.dumps(grade(context)))
```

**Step 2: Reference it from a task YAML**

```yaml
graders:
  - type: code
    weight: 0.3
    config:
      script: graders/my_validator.py
      params:
        resource_group: "rg-myapp-dev"
        expected_resources:
          - "Microsoft.Web/sites"
```

**Existing graders you can reuse:**
- `graders/infra_validator.py` — checks that a resource group and its resources exist after `azd provision`
- `graders/cleanup_validator.py` — checks that resources are deleted after `azd down`
- `graders/app_health.py` — HTTP health checks against deployed app URLs

### Adding a Jest Unit Test

Unit tests validate `azd` CLI behavior directly — no LLM, no Azure. They run fast and catch regressions in command structure, help text, and flags.

**Step 1: Create or edit a test file in `tests/unit/`**

```typescript
// tests/unit/my-new-test.test.ts
import { azd } from "../test-utils";

describe("my new azd tests", () => {
  test("version outputs a semver string", () => {
    const result = azd("version");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/\d+\.\d+\.\d+/);
  });
});
```

> **Important:** The shared `azd()` helper in `tests/test-utils.ts` sets `NO_COLOR: "1"` and `AZD_FORCE_TTY: "false"` automatically. Always import from there — don't create local helpers.

**Step 2: Run it**

```bash
# Build azd first (tests shell out to the binary)
cd ../../../ && go build && cd test/eval

# Run just your test
npx jest tests/unit/my-new-test.test.ts

# Run all unit tests
npm run test:unit
```

### Adding a Human Scenario Test

Human tests validate CLI usability patterns — things like "can a user discover the right command?" These run against the real `azd` binary but test the experience, not the LLM.

```typescript
// tests/human/my-scenario.test.ts
import { azd } from "../test-utils";

describe("deploy command discovery", () => {
  test("user can find deploy command from root help", () => {
    const root = azd("--help");
    expect(root.stdout).toContain("deploy");

    const deploy = azd("deploy --help");
    expect(deploy.exitCode).toBe(0);
    expect(deploy.stdout).toMatch(/\bUsage\b/);
  });
});
```

### Directory Structure Reference

```
cli/azd/test/eval/
├── eval.yaml              # Waza eval config (model, metrics, thresholds)
├── jest.config.ts          # Jest config for unit + human tests
├── package.json            # Node dependencies and npm scripts
├── tsconfig.json           # TypeScript config
├── graders/                # Custom Python graders for E2E validation
│   ├── app_health.py       #   HTTP health checks on deployed apps
│   ├── cleanup_validator.py #  Validates resources deleted after azd down
│   └── infra_validator.py  #   Validates resources exist after azd provision
├── tasks/                  # Waza LLM eval task definitions
│   ├── deploy/             #   Deployment scenarios
│   ├── environment/        #   Environment management scenarios
│   ├── lifecycle/          #   Full init→provision→deploy→down E2E
│   ├── negative/           #   Questions where azd is the wrong tool
│   └── troubleshoot/       #   Error diagnosis scenarios
├── tests/
│   ├── unit/               # Jest unit tests (no LLM, no Azure)
│   │   ├── command-registry.test.ts
│   │   ├── command-sequencing.test.ts
│   │   ├── flag-validation.test.ts
│   │   └── help-text-quality.test.ts
│   └── human/              # Human CLI usability tests
│       ├── cli-workflow.test.ts
│       ├── command-discovery.test.ts
│       └── error-recovery.test.ts
├── reports/                # Generated reports (gitignored)
```

## Scenario Categories

| Category | Directory | Graders | CI Frequency |
|----------|-----------|---------|-------------|
| Deploy workflows | `tasks/deploy/` | text, action_sequence, behavior | 3x daily |
| Error troubleshooting | `tasks/troubleshoot/` | text, behavior | 3x daily |
| Environment management | `tasks/environment/` | text, action_sequence | 3x daily |
| Negative tests | `tasks/negative/` | text, behavior | 3x daily |
| Full lifecycle E2E | `tasks/lifecycle/` | text, action_sequence, code | Weekly |

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `eval-unit.yml` | On PR | Jest unit tests + `waza validate` |
| `eval-waza.yml` | 3x daily (Tue-Sat) | Waza evals via Copilot SDK |
| `eval-e2e.yml` | Weekly | Waza E2E with Azure resource validation |
| `eval-report.yml` | Weekly | Aggregates results from Waza + E2E runs |

## Authentication & Secrets

### Test tiers and what they need

| Tier | Command | Azure Auth | Copilot Token | Browser Popups |
|------|---------|-----------|---------------|----------------|
| Unit tests | `npm run test:unit` | ❌ None | ❌ None | Never |
| Human tests | `npm run test:human` | ❌ None | ❌ None | Never |
| Mock LLM eval | `npm run waza:run:mock` | ❌ None | ❌ None | Never |
| LLM eval | `npm run waza:run` | ❌ None | ✅ Required | Never |
| E2E lifecycle | `eval-e2e.yml` | ✅ Required | ✅ Required | Never (OIDC) |

> **No test should ever open a browser.** Unit and human tests use `--no-prompt` and only test help text / error messages — they never call Azure APIs. E2E workflows use OIDC service principal auth (headless).

### Local development

```bash
# 1. Log in to Azure (one-time, uses your existing browser session)
az login
az account set --subscription <YOUR_SUBSCRIPTION_ID>

# 2. Verify your auth works (no browser should open)
azd auth login    # uses cached az credentials

# 3. Run tests — unit tests never touch Azure
npm run test:unit

# 4. For LLM evals (optional)
export COPILOT_CLI_TOKEN=<your-copilot-cli-token>
npm run waza:run
```

**Configuring a specific subscription for E2E graders:**

```bash
# Set the subscription that E2E graders will validate against
export AZURE_SUBSCRIPTION_ID=<your-subscription-id>

# Or configure via azd
azd config set defaults.subscription <your-subscription-id>
```

### CI/CD setup (GitHub Actions)

Configure these repository secrets (**Settings → Secrets and variables → Actions**):

| Secret | Used By | Purpose | How to Obtain |
|--------|---------|---------|---------------|
| `AZURE_CLIENT_ID` | `eval-e2e.yml` | OIDC Azure Login (no browser) | Create a service principal in Microsoft Entra ID with [federated credential](https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-create-trust) for GitHub Actions |
| `AZURE_TENANT_ID` | `eval-e2e.yml` | OIDC Azure Login | Microsoft Entra ID → Overview → Tenant ID |
| `AZURE_SUBSCRIPTION_ID` | `eval-e2e.yml`, graders | Target subscription for E2E deployments | Azure Portal → Subscriptions |
| `COPILOT_CLI_TOKEN` | `eval-waza.yml`, `eval-e2e.yml` | Authenticate Waza Copilot SDK executor | Copilot CLI API token |
| `GITHUB_TOKEN` | `eval-report.yml` | Create regression issues from reports | Auto-provided by GitHub Actions (no setup needed) |

**Setting up the service principal for CI:**

```bash
# 1. Create the service principal
az ad sp create-for-rbac --name "azd-eval-ci" --role Contributor \
  --scopes /subscriptions/<SUBSCRIPTION_ID>

# 2. Add OIDC federated credential for GitHub Actions
az ad app federated-credential create \
  --id <APP_OBJECT_ID> \
  --parameters '{
    "name": "azd-eval-github",
    "issuer": "https://token.actions.githubusercontent.com",
    "subject": "repo:Azure/azure-dev:ref:refs/heads/main",
    "audiences": ["api://AzureADTokenExchange"]
  }'

# 3. Add the 3 secrets to the repo (AZURE_CLIENT_ID, AZURE_TENANT_ID, AZURE_SUBSCRIPTION_ID)
```

> **Note:** OIDC federated credentials mean no client secret is stored anywhere. The service principal needs `Contributor` role on the target subscription. Graders obtain Azure access tokens at runtime via `az account get-access-token`.

## Reports

Generated reports are saved to `reports/` (gitignored). In CI, they're uploaded as workflow artifacts with 30-day retention.
