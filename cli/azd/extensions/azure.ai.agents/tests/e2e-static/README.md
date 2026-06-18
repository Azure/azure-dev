# E2E Static Tests v2 — Record/Replay Approach

## Concept

Based on Ben Hanrahan's `cli-interactive-tester` tool:
- Ben's tool records agent-driven sessions and can replay them without Copilot
- We use the same pattern: **YAML scripts** define a sequence of wait→action steps
- A **runner** drives tmux identically to Ben's `agent.py` primitives
- No LLM — pure deterministic replay of a recorded interaction

## How It Works

```
YAML script (recorded steps)
  → runner.py reads steps sequentially
  → For each step: wait for expected text in pane → execute action → next step
  → After all steps: run assertions
```

## Script Format

```yaml
name: "init-python-basic"
command: "azd ai agent init"
cwd: "~/e2e-test/{name}"
timeout: 300

steps:
  - wait_for: "Select a language"
    action: select
    text: "Python"

  - wait_for: "Select a starter template"
    action: select
    text: "Basic"

  - wait_for: "Enter a name"
    action: input
    text: ""              # empty = accept default (Enter)

assertions:
  - file_exists: "azure.yaml"
  - file_contains: { path: "azure.yaml", pattern: "services:" }
```

## Actions (same semantics as Ben's agent.py)

| Action    | Params     | Behavior                                    |
|-----------|-----------|---------------------------------------------|
| `select`  | `text`    | Type filter → wait → Enter (select_by_text) |
| `input`   | `text`    | Type text → Enter                           |
| `enter`   | —         | Press Enter (accept default/highlighted)    |
| `confirm` | `value`   | Send y/n → Enter                            |
| `key`     | `key`,`count` | Send raw tmux keys                      |
| `wait`    | `seconds` | Just wait                                   |

## Running

```bash
# Single test
python run.py scripts/init_python_basic.yaml

# All tests
python run.py --all

# Verbose (show each step)
python run.py scripts/init_python_basic.yaml -v
```

## References

- **Ben's tool**: `D:\w1\cli-interactive-tester\` — full MCP server + scenario runner
  - `auto_test_tool/agent.py` — tmux primitives, select_by_text, action dispatch
  - `auto_test_tool/runner.py` — scenario execution, tmux session management
  - `scenarios/azd_ai_agent/` — example scenarios in YAML
- **Test cases source**: Based on internal bug-bash wiki and manual test steps
- **azd extension source**: `D:\w1\azure-dev\cli\azd\extensions\azure.ai.agents\internal\cmd\`

## Known Issue: WSL Token Timeout

In WSL, `azd auth token` takes >10s (token refresh network call), exceeding the
Go Azure SDK's 10s process timeout for `AzureDeveloperCLICredential`. This causes
init to fail after Foundry project selection with `signal: killed`.

This is a WSL-specific problem. On native Linux or in CI with service principal
auth, this should not occur. The test framework itself is correct.
