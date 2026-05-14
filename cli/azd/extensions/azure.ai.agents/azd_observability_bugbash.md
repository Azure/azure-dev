# azd ai agent eval + optimize — BugBash

> **TiP regions** Currently, some dependent APIs are only available in this region for now

## 1. Install the extension

Prerequisites: [azd CLI](https://aka.ms/azd), [Go](https://go.dev/dl/), `az login`

```bash
azd ext install microsoft.azd.extensions
git clone https://github.com/Zyysurely/azure-dev.git
cd azure-dev/cli/azd/extensions/azure.ai.agents
git checkout zyying/opt_eval
azd x build
```

After building, register the extension and overlay the custom binary:

```powershell
# Windows (PowerShell)
azd ext install azure.ai.agents
copy bin\azure-ai-agents-windows-amd64.exe $env:USERPROFILE\.azd\extensions\azure.ai.agents\ -Force
```

```bash
# macOS / Linux
azd ext install azure.ai.agents
cp bin/azure-ai-agents-$(uname -s | tr A-Z a-z)-* ~/.azd/extensions/azure.ai.agents/
```

Verify:`azd ai agent eval --help` and `azd ai agent optimize --help`

## 2. Ensure the access to the bugbash project

https://ms.portal.azure.com/#@microsoft.onmicrosoft.com/resource/subscriptions/2d385bf4-0756-4a76-aa95-28bf9ed3b625/resourceGroups/rg-azdbugbash/users
Please activate `Foundry User` and `Owner` access


## 3. Create an optimization-ready agent

Navigate to a fresh directory outside the extension repo, init the agent and point to our bugbash project, if you already have an azd project with TiP foundry account, you can continue to use it.

```bash
mkdir bugbash-azd-<alias> && cd bugbash-azd-<alias>
azd init -t https://github.com/zyysurely/sample_agent .
azd ai agent init --project-id /subscriptions/2d385bf4-0756-4a76-aa95-28bf9ed3b625/resourceGroups/rg-azdbugbash/providers/Microsoft.CognitiveServices/accounts/azd-bugbash-0514/projects/bugbash-westus2
# Customize your agent name and model deployment
```

The template includes `agent_optimization/` — a small package that reads config
injected by the optimization service at runtime. Your agent calls `load_config()` at startup:

```python
from agent_optimization import load_config

config = load_config(
    default_instructions="You are a helpful assistant.",
    default_model="gpt-4.1-mini",
)
```

## 4. Test locally [You can skip since the current sample agent code has been verified]

```bash
azd ai agent run
# In another terminal:
azd ai agent invoke --local "Hello!"
```

## 5. Deploy hosted agent

Point to an existing Foundry project and deploy (no `azd provision` needed):

```bash
# Windows (PowerShell)
azd deploy
```


Verify: `azd ai agent invoke "Hello!"`

> **If you have Owner permissions** and want fresh resources: run `azd provision` before `azd deploy`.


## 6. E2E Hero Scenario (inside an azd project with a hosted agent)

All commands below auto-detect the agent context from the current azd environment.
Run them from your deployed azd project directory.

### 6a. Initialize an eval suite

> **Note:** The dataset generation API is not yet available. Use the sample `data.jsonl` included in the template.

```bash
azd ai agent eval init --dataset ./data.jsonl
```

The command resolves your agent from `azure.yaml` and prompts interactively:

```
Resolving eval context...
  Reading project configuration...
  Detecting agent service...
  Resolving Foundry project endpoint...

Detected eval target:
  (✓) Service:        sample-agent (azure.yaml)
  (✓) Agent:          sample-agent (AGENT_SAMPLE_AGENT_NAME)
  (✓) Version:        1 (AGENT_SAMPLE_AGENT_VERSION)
  (✓) Kind:           hosted (agent.yaml)
  (✓) Endpoint:       https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2
  (✓) Project:        D:\optimization\bugbash-agent-zyying
  Eval config:        D:\optimization\bugbash-agent-zyying\eval.yaml

? Eval suite name: smoke-core-zyying
? How would you like to provide the generation instruction?: Type inline
? Describe what this agent does and what scenarios to test: test agent
? Select the model for evaluation and generation: Select another deployment
? Select a model deployment: gpt-4o (gpt-4o)
? Max samples: 100
\ Evaluator generation...  (✓) Done  Evaluator generation  (1m16s)

   Artifacts:  D:\optimization\bugbash-agent-zyying\.azure\.foundry
               evaluators/smoke-core-zyying-35368f67.json
Eval suite created
   Config:     D:\optimization\bugbash-agent-zyying\eval.yaml
   Dataset:    D:\optimization\bugbash-agent-zyying\data.jsonl
   Evaluator:  smoke-core-zyying-35368f67

   Review the generated assets, then run:
     azd ai agent eval run
```

### 6b. Run an eval

```bash
azd ai agent eval run
```

Reads `eval.yaml`, creates the eval on the Foundry backend, and submits a run against your deployed agent.

### 6c. Browse eval results

```bash
# List all evals (table with status, run count, created date)
azd ai agent eval list

# Show details for the most recent eval (auto-resolved from azd env)
azd ai agent eval show

# Export results to JSON for offline analysis
azd ai agent eval show -O results.json
```

### 6d. Optimize the agent

After the eval suite is ready, run optimize. It auto-detects the `eval.yaml` you just created.

```bash
azd ai agent optimize
# → Prompts: "Found eval.yaml in project. Use it for optimization?"
#   Select Yes to use your eval config, or No to use the built-in dataset.
```

Expected output (takes ~5–20 minutes):

```
Optimizing agent "sample-agent"...
  Config: D:\optimization\bugbash-agent-zyying\eval.yaml
  Job ID: opt_f74131d58c774ebba1765fae1005a9f8
  ⠦ completed · strategy: gepa · iteration 1 · score: 0.95 · 3m0s

Results:
  Candidate              Score    Pass   Tokens
  ──────────────────── ─────── ─────── ────────
  baseline                0.73    100%      430
  baseline_instr_v2       0.77    100%     1180
  baseline_instr_v3       0.85    100%     1204
  baseline_instr_v1 ★     0.92    100%     1063

  Candidate IDs:
      baseline_instr_v2    cand_445fe8e68e224d6d94cbb37b022945eb
      baseline_instr_v3    cand_51b87d7ce10b43ba801776483a9b5506
    ★ baseline_instr_v1    cand_6b5c23ed295f4f4e9be87b7fdb3809b0

  Deploy the best candidate:
    azd ai agent optimize deploy --candidate cand_6b5c23ed295f4f4e9be87b7fdb3809b0
```

The ★ marks the best candidate. Copy the deploy command from the output to promote it.

#### Customizing optimization options in `eval.yaml`

You can fine-tune optimization behavior by adding or modifying the `options:` section in your `eval.yaml`. Below are all available fields, their types, and defaults:

```yaml
options:
  eval_model: "gpt-4o"                          # (string) Model used for evaluation. Default: "gpt-4o"
  mode: "optimize"                               # (string) Run mode. Default: "optimize"
  strategies:                                    # ([]string) Optimization strategies to try.
    - instruction                                #   Default: ["instruction", "skill", "agents-optimization-job"]
    - skill
    - agents-optimization-job
  budget: 5                                      # (int) Max optimization budget (number of candidates). Default: 5
  max_iterations: 2                              # (int) Max iterations per strategy. Default: 2 (when strategies are default)
  min_improvement: 0.0                           # (float) Minimum score improvement to accept a candidate. Default: 0 (not set)
  improvement_threshold: 0.0                     # (float) Threshold for incremental improvement. Default: 0 (not set)
  pass_threshold: 0.0                            # (float) Minimum passing score. Default: 0 (not set)
  keep_versions: false                           # (bool) Keep all intermediate agent versions. Default: false
  tasks_per_iteration: 0                         # (int) Number of tasks per iteration. Default: 0 (server decides)
  reflection_model: ""                           # (string) Model for reflection steps. Default: "" (uses eval_model)
```

For example, to increase the budget and use a different eval model:

```yaml
options:
  eval_model: "gpt-4.1"
  budget: 10
  max_iterations: 3
```

Fields you omit will use the defaults above. The `strategies` field defaults to all three strategies if not specified.

### 6e. Monitor optimization jobs

```bash
# Watch a running job in real-time
azd ai agent optimize status <operation-id> --watch

# List all optimization runs
azd ai agent optimize list

# Cancel a running job
azd ai agent optimize cancel <operation-id>
```

### 6f. Deploy the winning candidate

> **⚠️ Known Issue:** Due to a FAOS CANDIDATE API issue, `optimize deploy` and `optimize apply` cannot fetch candidate config at this time. This step is blocked until the API issue is resolved.
But you can check agent optimization job in foundry UI with `?flight=enable_faos_read_ui`

The optimize output includes a ready-to-use deploy command:

```bash
azd ai agent optimize deploy --candidate <candidate-id>
```

This creates a new agent version with `OPTIMIZATION_CONFIG` set to the candidate's
config (instructions, model, temperature). The agent SDK's `load_config()` reads this
at startup and applies the optimized settings.

### 6g. Verify the optimized agent

> **⚠️ Blocked:** This step depends on 6f, which is currently blocked by the FAOS CANDIDATE API issue.

```bash
azd ai agent invoke "Hello!"
# Expected: agent responds using the optimized configuration
```

---

## Comprehensive Test Scenarios

### A. `azd ai agent eval init`

#### Inside azd project (cd into your deployed azd project)

```bash
# A1. Default interactive init — auto-detects agent from azd env
azd ai agent eval init --dataset ./data.jsonl
# Expected: prompts for name, instruction, model, max-samples
#           writes eval.yaml + artifacts under .azure/.foundry/

# A2. Custom eval suite name
azd ai agent eval init --dataset ./data.jsonl --name my-custom-suite
# Expected: config name = "my-custom-suite-<hex>" (random suffix appended)

# A3. Inline gen-instruction (skip prompt)
azd ai agent eval init --dataset ./data.jsonl -g "Test the agent's ability to handle refund requests"
# Expected: uses inline instruction, skips instruction prompt

# A4. Gen-instruction from file
echo "Test customer support scenarios" > /tmp/instruction.txt
azd ai agent eval init --dataset ./data.jsonl -G /tmp/instruction.txt
# Expected: reads instruction from file

# A5. Custom eval model
azd ai agent eval init --dataset ./data.jsonl --eval-model gpt-4o
# Expected: uses gpt-4o instead of deployed model default

# A6. Custom evaluators
azd ai agent eval init --dataset ./data.jsonl --evaluator builtin.task_adherence --evaluator custom_eval
# Expected: eval.yaml has both evaluators listed

# A7. Custom output path
azd ai agent eval init --dataset ./data.jsonl -O my-eval.yaml
# Expected: writes to my-eval.yaml instead of eval.yaml

# A8. --no-wait mode
azd ai agent eval init --dataset ./data.jsonl --no-wait
# Expected: submits jobs, prints pending op IDs, returns immediately
#           eval.yaml has InitStatus: pending

# A9. Regeneration — eval.yaml already exists
#     (run init once first, then run again)
azd ai agent eval init --dataset ./data.jsonl
# Expected: prompts "Existing dataset: ... Do you want to regenerate?"
#           and "Existing evaluator: ... Do you want to regenerate?"

# A10. Reset defaults — overwrite existing config
azd ai agent eval init --dataset ./data.jsonl --reset-defaults
# Expected: overwrites eval.yaml without prompting about existing config

# A11. Non-interactive mode (no prompts)
azd ai agent eval init --dataset ./data.jsonl --no-prompt
# Expected: uses defaults without prompting. Full regeneration if eval.yaml exists.
# Clean up: Remove-Item env:\AZD_FORCE_TTY

# A12. Multiple agent services in azure.yaml
# (if your project has 2+ azure.ai.agent services)
azd ai agent eval init --dataset ./data.jsonl
# Expected: prompts to select which agent service
```

#### Outside azd project (cd to an empty directory)

```bash
mkdir /tmp/eval-test && cd /tmp/eval-test

# A13. No agent flag, no project — should fail
azd ai agent eval init --dataset ./data.jsonl
# Expected: ERROR — "failed to get project config (is there an azure.yaml?)"
#           or guidance to use --agent / run from azd project

# A14. Explicit agent + endpoint — works standalone
azd ai agent eval init --dataset ./data.jsonl \
  --agent sample-agent \
  -p https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2
# Expected: works without azure.yaml; writes eval.yaml in current dir

# A15. Missing endpoint — should fail with guidance
azd ai agent eval init --dataset ./data.jsonl --agent sample-agent
# Expected: ERROR — "Foundry project context could not be resolved"
#           suggests --project-endpoint or azd ai agent init

# A16. Endpoint via env var
$env:AZURE_AI_PROJECT_ENDPOINT = "https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2"
azd ai agent eval init --dataset ./data.jsonl --agent sample-agent
# Expected: picks up endpoint from env var, works
# Clean up: Remove-Item env:\AZURE_AI_PROJECT_ENDPOINT
```

---

### B. `azd ai agent eval run`

#### Inside azd project

```bash
# B1. Default run (eval.yaml exists from init)
azd ai agent eval run
# Expected: reads eval.yaml from project dir, creates eval, submits run

# B2. Custom config path
azd ai agent eval run --config my-eval.yaml
# Expected: uses my-eval.yaml instead of eval.yaml

# B3. Resume pending init
# (if you used --no-wait during init, eval.yaml has pending status)
azd ai agent eval run
# Expected: detects InitStatus: pending, resumes polling, then runs eval
```

#### Outside azd project

```bash
cd /tmp/eval-test   # directory with eval.yaml from A14

# B4. eval.yaml in cwd, no azd project
azd ai agent eval run
# Expected: falls back to prompt-based endpoint resolution, runs eval

# B5. No eval.yaml at all
mkdir /tmp/empty-test && cd /tmp/empty-test
azd ai agent eval run
# Expected: ERROR — cannot read eval.yaml
```

---

### C. `azd ai agent eval list`

```bash
# C1. Default list (inside or outside project, needs endpoint)
azd ai agent eval list
# Expected: table with columns: Eval ID, Name, Status, Runs, Created by, Created on
#           max 10 results, active eval marked with *

# C2. Custom limit
azd ai agent eval list --limit 3
# Expected: at most 3 rows

# C3. No evals exist
# (on a fresh project with no evals)
azd ai agent eval list
# Expected: "no evaluations found" or empty table
```

---

### D. `azd ai agent eval show`

```bash
# D1. Show by eval ID
azd ai agent eval show <eval-id>
# Expected: eval definition + recent run history

# D2. Auto-resolve eval ID (from azd env)
azd ai agent eval show
# Expected: uses last eval ID from environment

# D3. No eval ID available
# (fresh environment, no prior eval)
azd ai agent eval show
# Expected: ERROR — eval ID required

# D4. Show specific run details
azd ai agent eval show <eval-id> --eval-run-id <run-id>
# Expected: per-criteria breakdown, passed/failed/errored counts

# D5. Export eval + runs to JSON
azd ai agent eval show <eval-id> -O results.json
# Expected: writes {"eval": ..., "runs": [...]} to results.json

# D6. Export single run to JSON
azd ai agent eval show <eval-id> --eval-run-id <run-id> -O run.json
# Expected: writes single run result to run.json

# D7. Custom run limit
azd ai agent eval show <eval-id> --limit 5
# Expected: at most 5 runs in history
```

---

### E. `azd ai agent optimize` (main command)

#### Inside azd project

```bash
# E1. Default optimize — auto-detect agent
azd ai agent optimize
# Expected: if no eval.yaml → uses built-in dataset (3 tasks, 12 criteria)
#           if eval.yaml exists → prompts "Found eval.yaml in project. Use it?"

# E2. Accept eval.yaml prompt
# (run eval init first, then run optimize, confirm yes)
azd ai agent optimize
# Expected: loads config from eval.yaml. Output: "Config: <path>/eval.yaml"

# E3. Decline eval.yaml prompt
# (eval.yaml exists, decline the prompt)
azd ai agent optimize
# Expected: falls back to built-in defaults. Output: "Dataset: built-in (3 tasks, 12 criteria)"

# E4. eval.yaml + --no-prompt
$env:AZD_FORCE_TTY = "false"
azd ai agent optimize
# Expected: skips eval.yaml prompt, uses built-in defaults
# Clean up: Remove-Item env:\AZD_FORCE_TTY

# E5. Explicit --config overrides eval.yaml detection
azd ai agent optimize --config spec.yaml
# Expected: uses spec.yaml, ignores eval.yaml entirely

# E6. Positional agent arg
azd ai agent optimize my-agent
# Expected: uses "my-agent" as agent name

# E7. --agent flag
azd ai agent optimize --agent my-agent
# Expected: uses flag value

# E8. Custom eval model
azd ai agent optimize --eval-model gpt-4o
# Expected: overrides options.eval_model in config

# E9. Custom strategy (single)
azd ai agent optimize -s skill
# Expected: uses only skill strategy

# E10. Custom strategy (multiple)
azd ai agent optimize -s instruction -s skill
# Expected: uses both strategies

# E11. --no-wait
azd ai agent optimize --no-wait
# Expected: submits job, prints ID, returns immediately

# E12. Watch polling progress
azd ai agent optimize
# Expected: spinner shows status, strategy, iteration, score, elapsed time
#           final results table with ★ best candidate and deploy command
```

#### Outside azd project

```bash
mkdir /tmp/opt-test && cd /tmp/opt-test

# E13. No agent flag, no project — should fail
azd ai agent optimize
# Expected: ERROR — "agent name is required: use --agent <name>, or run from an azd project after 'azd deploy'"

# E14. Explicit agent + endpoint
azd ai agent optimize --agent sample-agent \
  -p https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2
# Expected: works without project. Uses built-in defaults.

# E15. Explicit agent via env var
$env:AZURE_AI_PROJECT_ENDPOINT = "https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2"
azd ai agent optimize --agent sample-agent
# Expected: resolves endpoint from env var
# Clean up: Remove-Item env:\AZURE_AI_PROJECT_ENDPOINT

# E16. With config file, no project
azd ai agent optimize --config spec.yaml
# Expected: loads config from file, no project resolution needed
```

#### Config validation (can run anywhere with a config file)

```bash
# E17. Missing agent name in config
# (create spec.yaml with empty agent.name)
azd ai agent optimize --config spec.yaml
# Expected: ERROR — "agent.name is required"

# E18. Missing eval model
# (config without options.eval_model)
azd ai agent optimize --config spec.yaml
# Expected: ERROR — "options.eval_model is required"

# E19. No dataset at all
# (config without dataset_file, dataset_reference, or inline)
azd ai agent optimize --config spec.yaml
# Expected: ERROR — "one of dataset_file or dataset_reference is required"

# E20. Conflicting dataset
# (config with both dataset_file and dataset_reference)
azd ai agent optimize --config spec.yaml
# Expected: ERROR — "dataset_file and dataset_reference are mutually exclusive"

# E21. Invalid config file path
azd ai agent optimize --config nonexistent.yaml
# Expected: ERROR — file not found + guidance to check path
```

---

### F. `azd ai agent optimize status`

```bash
# F1. Status by operation ID
azd ai agent optimize status <operation-id>
# Expected: job summary — ID, Status, Agent, Strategy, Score, Created

# F2. Auto-resolve from env (after running optimize in project)
azd ai agent optimize status
# Expected: uses OPTIMIZE_LAST_OPERATION_ID from azd env

# F3. No ID available
# (fresh env, never ran optimize)
azd ai agent optimize status
# Expected: ERROR — operation ID required

# F4. --watch mode
azd ai agent optimize status <operation-id> --watch
# Expected: polls until job completes, shows spinner + progress

# F5. Custom poll interval
azd ai agent optimize status <operation-id> --watch --poll-interval 10
# Expected: polls every 10 seconds instead of 5

# F6. Completed job shows candidates
azd ai agent optimize status <completed-operation-id>
# Expected: results table with candidates, scores, deploy command
```

---

### G. `azd ai agent optimize list`

```bash
# G1. Default list
azd ai agent optimize list
# Expected: table — ID, Status, Agent, Best Score, Created. Max 20 rows.

# G2. Filter by status
azd ai agent optimize list --status completed
# Expected: only completed jobs shown

# G3. Invalid status filter
azd ai agent optimize list --status invalid
# Expected: ERROR — invalid status value

# G4. Custom limit
azd ai agent optimize list --limit 3
# Expected: at most 3 entries

# G5. No jobs exist
# (fresh project endpoint)
azd ai agent optimize list
# Expected: "no optimization jobs found" message
```

---

### H. `azd ai agent optimize cancel`

```bash
# H1. Cancel a running job
# (start optimize --no-wait first, then cancel)
azd ai agent optimize --no-wait
azd ai agent optimize cancel <operation-id>
# Expected: job cancelled, shows guidance

# H2. Cancel already-completed job
azd ai agent optimize cancel <completed-id>
# Expected: ERROR or message — job already in terminal state

# H3. Missing ID argument
azd ai agent optimize cancel
# Expected: ERROR — requires exactly 1 argument
```

---

### I. `azd ai agent optimize apply` (inside azd project only)

> **⚠️ Known Issue:** Due to a FAOS CANDIDATE API issue, `optimize apply` and `optimize deploy` cannot apply the optimized result at this time. These commands will fail when trying to fetch candidate config.

```bash
# I1. Apply candidate config to agent.yaml
azd ai agent optimize apply --candidate <candidate-id>
# Expected: fetches candidate config, writes OPTIMIZATION_CONFIG and
#           OPTIMIZATION_CANDIDATE_ID into agent.yaml env vars.
#           Downloads skill files. Prints "azd deploy --service <svc>".
# Verify: cat agent.yaml — should see new env vars appended

# I2. Auto-detect agent service
azd ai agent optimize apply --candidate <candidate-id>
# Expected: resolves agent service from azure.yaml automatically

# I3. Explicit agent service name
azd ai agent optimize apply --candidate <candidate-id> --agent sample-agent
# Expected: uses specified service

# I4. Missing --candidate flag
azd ai agent optimize apply
# Expected: ERROR — --candidate is required

# I5. Outside azd project — should fail
cd /tmp/empty-test
azd ai agent optimize apply --candidate <candidate-id>
# Expected: ERROR — requires azd project, suggests "optimize deploy" instead
```

---

### J. `azd ai agent optimize deploy` (API-based, works anywhere)

```bash
# J1. Deploy candidate via API
azd ai agent optimize deploy --candidate <candidate-id> --agent sample-agent
# Expected: creates new agent version with OPTIMIZATION_CONFIG, shows new version number

# J2. Auto-detect agent inside project
cd <azd-project>
azd ai agent optimize deploy --candidate <candidate-id>
# Expected: resolves agent name from project + environment

# J3. Outside project with explicit agent + endpoint
cd /tmp/empty-test
azd ai agent optimize deploy --candidate <candidate-id> --agent sample-agent \
  -p https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2
# Expected: works without project context

# J4. Missing --candidate
azd ai agent optimize deploy
# Expected: ERROR — --candidate required

# J5. Verify deployed version
azd ai agent invoke "Hello!"
# Expected: agent responds using optimized config
```

---

### K. End-to-end flows

```bash
# K1. Full eval → optimize → apply → deploy roundtrip
azd ai agent eval init --dataset ./data.jsonl
azd ai agent eval run
azd ai agent eval list
azd ai agent eval show
azd ai agent optimize                    # accept eval.yaml prompt
azd ai agent optimize apply --candidate <best-candidate-id>
azd deploy --service sample-agent
azd ai agent invoke "Hello!"

# K2. Optimize-only flow (no eval init)
azd ai agent optimize
azd ai agent optimize status             # auto-resolves last job
azd ai agent optimize deploy --candidate <id>
azd ai agent invoke "Hello!"

# K3. Standalone flow (outside project)
mkdir /tmp/standalone && cd /tmp/standalone
azd ai agent optimize --agent sample-agent --eval-model gpt-4o --project-id 
azd ai agent optimize list
azd ai agent optimize status <id>
```

---

### L. Error & edge cases

```bash
# L1. Not logged in
azd auth logout
azd ai agent optimize --agent sample-agent
# Expected: authentication error

# L2. Invalid endpoint
azd ai agent optimize --agent sample-agent -p https://invalid.endpoint.com
# Expected: error with reachability guidance

# L3. --help for all commands
azd ai agent eval --help
azd ai agent eval init --help
azd ai agent eval run --help
azd ai agent eval list --help
azd ai agent eval show --help
azd ai agent optimize --help
azd ai agent optimize status --help
azd ai agent optimize list --help
azd ai agent optimize cancel --help
azd ai agent optimize apply --help
azd ai agent optimize deploy --help
# Expected: accurate, complete help text for each

# L4. Eval model not deployed
azd ai agent optimize --eval-model nonexistent-model
# Expected: job runs but all scores may be zero (known issue — no error message)

# L5. Artifacts directory structure
# (after eval init completes inside project)
ls .azure/.foundry/
# Expected: datasets/, evaluators/, results/ subdirectories with generated files
```

---

## Cleanup: Revert to the official extension binary

After the bugbash, reinstall the released extension to remove the custom binary:

```powershell
# Windows (PowerShell)
azd ext uninstall azure.ai.agents
azd ext install azure.ai.agents
```

```bash
# macOS / Linux
azd ext uninstall azure.ai.agents
azd ext install azure.ai.agents
```

This re-downloads the official published binary and removes the custom build overlay.