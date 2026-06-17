# E2E Static Tests for `azd ai agent`

Deterministic (no-LLM) end-to-end tests for the `azure.ai.agents` CLI extension.
Uses Python + tmux to drive the interactive CLI with hardcoded responses.

## Architecture

```
Python test scripts → tmux send-keys/capture-pane → azd ai agent CLI
```

No Copilot/LLM needed — all interactions are deterministic and reproducible.

## Tiers

| Tier | What | Auth Required | Time |
|------|------|---------------|------|
| 0 | Offline CLI validation (version, help, flags, doctor) | No | ~15s |
| 1 | Interactive init variants (Python/C#, code/container, existing/create) | Yes (read-only) | ~3.5min |
| 2 | Full golden path: init → provision → deploy → invoke → teardown | Yes (read-write) | ~12min |

## Running Locally

### Prerequisites
- `azd` built and on PATH with `azure.ai.agents` extension installed
- `tmux` installed
- Python 3.10+
- Azure CLI logged in (`az login`)
- GitHub token available (via `gh auth login` or `$GITHUB_TOKEN`)

### Environment Variables
| Variable | Required | Description |
|----------|----------|-------------|
| `E2E_SUBSCRIPTION` | Tier 1/2 | Azure subscription ID |
| `E2E_PROJECT` | Tier 1/2 | Foundry project name |
| `E2E_TENANT` | Tier 1/2 | Azure tenant ID |
| `GITHUB_TOKEN` | Tier 1/2 | GitHub PAT (for template downloads) |
| `E2E_TMUX` | No | Path to tmux binary |
| `E2E_HOME` | No | Override HOME directory |

### Run
```bash
# Tier 0 only (no auth needed)
python3 test_tier0.py

# Tier 1 (needs auth)
export E2E_SUBSCRIPTION="your-sub-id"
export E2E_PROJECT="your-foundry-project"
python3 test_tier1.py

# Tier 2 — both golden paths in parallel
python3 test_tier2.py --mode both

# Tier 2 — single mode
python3 test_tier2.py --mode code
python3 test_tier2.py --mode container

# All tiers
bash run_all.sh

# Skip Tier 2
bash run_all.sh --skip-tier2
```

## CI Pipeline

See `.github/workflows/e2e-ext-azure-ai-agents-static.yml`.

Triggered via `workflow_dispatch` with tier selection.
Builds azd from source, installs extension from local registry, runs tests.

## Test Details

### Tier 0 (16 tests)
- `azd ai agent version` / `--help`
- `azd ai agent sample list` (text + JSON output, language filter)
- `azd ai agent doctor` (empty dir, local-only, partial failure)
- Input validation (mutually exclusive flags, `--no-prompt` without config)
- `invoke --protocol banana_protocol` (invalid protocol rejection)
- `eval list` / `optimize apply` (missing context/flags)
- `delete --help` / `endpoint show --help` / `code download --help`
- Interactive picker Ctrl-C behavior

### Tier 1 (8 tests)
- Python Basic template + code deploy (existing project)
- Python Basic template + container deploy (existing project)
- C# Hello World template + code deploy
- "Create new" Foundry project path (Ctrl-C before creation)
- Init from manifest URL (`-m`)
- Init with `--agent-name` flag
- Invalid `--deploy-mode` flag (error case)
- `--no-prompt` with manifest URL

### Tier 2 (2 tests, parallel)
- **Code deploy golden path**: init → provision → deploy → invoke → teardown
- **Container deploy golden path**: init → provision → deploy → invoke → teardown

Both use the same Python Basic template with different deploy modes.
Agent names differ to avoid collisions when running in parallel.
