# AZD CLI Evaluation & Testing Framework

Measures how well GitHub Copilot CLI interacts with the Azure Developer CLI (`azd`). Inspired by the [microsoft/github-copilot-for-azure](https://github.com/microsoft/github-copilot-for-azure) testing architecture.

## Overview

When a user asks Copilot CLI a question like "deploy my Python app to Azure", the LLM suggests `azd` shell commands (`azd init`, `azd provision`, `azd deploy`). This framework tests:

1. **Does the LLM suggest the right commands?** (text graders)
2. **Are the commands in the right order?** (action_sequence graders)
3. **Do the commands succeed when executed?** (code graders)
4. **Is the deployed infrastructure correct and the app running?** (code graders with ARM + HTTP validation)

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
- [Waza CLI](https://github.com/microsoft/waza) (`azd extension install waza` or `go install github.com/microsoft/waza@latest`)
- Python 3.x (for custom graders)

### Run Tests

```bash
# Install dependencies
npm install

# Build azd (required for CLI tests)
cd ../../../ && go build && cd test/eval

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

## Adding a New Scenario

1. Create a YAML file in the appropriate `tasks/` subdirectory
2. Define `id`, `description`, `inputs.prompt`, and `graders`
3. Choose graders:
   - `text` — regex pattern matching on LLM response
   - `action_sequence` — verify command ordering
   - `behavior` — efficiency constraints (max tool calls, tokens)
   - `code` — custom Python validation (for E2E tests)
4. Submit a PR — CI validates YAML syntax automatically

### Example

```yaml
id: my-new-scenario-001
description: User asks how to create a new azd project
inputs:
  prompt: "How do I start a new Azure project with azd?"
graders:
  - type: text
    weight: 0.5
    config:
      must_match:
        - "azd init"
      must_not_match:
        - "azd down"
  - type: behavior
    weight: 0.5
    config:
      max_tool_calls: 5
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
| `eval-human.yml` | Weekly | Human usage baseline tests |
| `eval-report.yml` | Weekly | Comparison report + auto-issue creation |

## Reports

Generated reports are saved to `reports/` (gitignored). In CI, they're uploaded as workflow artifacts with 30-day retention.
