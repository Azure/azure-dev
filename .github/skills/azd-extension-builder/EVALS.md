# azd-extension-builder — Evaluation Suite

Waza eval suite for the `azd-extension-builder` skill. It measures whether the skill leads the
agent to give correct, current guidance for building azd extensions. Uses the
[microsoft/waza](https://github.com/microsoft/waza) v1.0 skill-eval schema.

## Layout

The eval files are **colocated with `SKILL.md`** (same directory). This is required: `waza run`
treats the directory that contains `eval.yaml` as the skill directory, so the eval only loads the
skill under test when `SKILL.md` sits next to `eval.yaml`. A nested `evals/` subfolder would run
the agent **without** the skill and silently measure nothing.

```
azd-extension-builder/
├── SKILL.md
├── eval.yaml        # spec: config (copilot-sdk), metrics, tasks glob
├── tasks/           # one scenario per file
│   ├── scaffold-go-extension.yaml
│   ├── install-developer-extension.yaml   # guardrail: no dev source required
│   ├── first-party-extension.yaml
│   ├── capability-mcp-server.yaml
│   ├── build-test-local.yaml
│   ├── release-external.yaml               # pack -> release -> publish order
│   ├── release-first-party.yaml            # 2-PR flow, not direct azd x release
│   └── negative-unrelated.yaml             # skill must not over-trigger
└── references/
```

## Schema (waza v1.0)

- `eval.yaml`: `name`, `skill`, `config` (`trials_per_task`, `timeout_seconds`, `executor:
  copilot-sdk`, `model`), `metrics[]`, and a `tasks:` glob list.
- Task files: `id`, `name`, `inputs.prompt`, optional `expected` (`output_contains`,
  `output_not_contains`, `should_trigger`), and per-task `graders[]`.

Grader types used:

- `text` — `contains` / `not_contains` / `regex_match` (`regex_match` with an alternation gives
  "match any of" semantics).
- `action_sequence` — `matching_mode: in_order_match` + `expected_actions` for multi-step flows.

## Install waza

The public npm `waza` package is currently an empty placeholder — use the official installer:

```bash
curl -fsSL https://raw.githubusercontent.com/microsoft/waza/main/install.sh | bash
# adds waza to ~/bin (or /usr/local/bin); ensure it is on PATH
```

## Run

```bash
# Compliance + schema check for the skill (offline, no model calls)
waza check .github/skills/azd-extension-builder

# Run the evals (uses the embedded Copilot SDK; needs Copilot access)
waza run .github/skills/azd-extension-builder/eval.yaml -v

# Or, from the skill directory:
cd .github/skills/azd-extension-builder && waza run eval.yaml -v
```

`waza check` performs schema validation as part of its report. Full `waza run` executes the agent
via the copilot-sdk executor and requires Copilot access.

## Maintenance

Update or add tasks when the skill's guidance changes (new capability, new `azd x` flag, changed
release flow). Keep graders asserting behavior that is verifiable against live sources
(`azd x --help`, `extension.schema.json`) per the skill's freshness rules.
