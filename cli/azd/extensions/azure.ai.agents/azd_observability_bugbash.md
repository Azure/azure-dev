# azd ai agent eval + optimize — BugBash

> **TiP regions** Currently, some dependent APIs are only available in this region for now

## 1. Install the extension

Prerequisites: [azd CLI](https://aka.ms/azd), [Go](https://go.dev/dl/), `az login`

```bash
# Installing private registry for bugbash
azd ext install microsoft.azd.extensions
azd ext source add --name zyysurely --type url --location https://raw.githubusercontent.com/Zyysurely/azure-dev/zyying/opt_eval/cli/azd/extensions/registry.json
azd ext install azure.ai.agents --source zyysurely --version 0.1.33-optbugbash-preview --force

# If then you want to switch back to the official version, use
azd ext install azure.ai.agents --force
```

Verify:`azd ai agent eval --help` and `azd ai agent optimize --help`

## 2. Ensure the access to the bugbash project

https://ms.portal.azure.com/#@microsoft.onmicrosoft.com/resource/subscriptions/2d385bf4-0756-4a76-aa95-28bf9ed3b625/resourceGroups/rg-azdbugbash/users
Please activate `Foundry User` and `Owner` access


## 3. Create an optimization-ready agent

Navigate to a fresh directory outside the extension repo, init the agent and point to our bugbash project, if you already have an azd project with TiP foundry account, you can continue to use it.

```bash
git clone https://github.com/ai-platform-microsoft/foundry-observability-playground.git
cd .\foundry-observability-playground\demos\build2026\agents\travel-approver\
azd ai agent init --project-id /subscriptions/2d385bf4-0756-4a76-aa95-28bf9ed3b625/resourceGroups/rg-azdbugbash/providers/Microsoft.CognitiveServices/accounts/azd-bugbash-0514/projects/bugbash-westus2
# !!! Customize your agent name and model deployment
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


## 6. E2E Hero Scenarios

There are two paths depending on whether you use the **bugbash project** or **your own project**.

---

### Path A: Using the bugbash project (eval + optimize)

> Use this path if you cloned the template in step 3 and deployed to the bugbash Foundry project.
> You have access to the eval APIs and can run the full eval → optimize flow.

All commands below auto-detect the agent context from the current azd environment.
Run them from your deployed azd project directory.

#### 6a-A. Initialize an eval suite

> Generating the eval suite which can optimize your agent adaptively, which could used for optimization as well

```bash
# including both data generation and evaluator generation
azd ai agent eval init 

# (Recommended) Using our provided golden dataset, but also adaptive evaluator
azd ai agent eval init --dataset eval/travel_approval_golden.jsonl
```

The command resolves your agent from `azure.yaml` and prompts interactively:

```
Resolving eval context...
  Reading project configuration...
  Detecting agent service...
  Resolving Foundry project endpoint...

Detected eval target:
  (✓) Service:        travel-approver-bb (azure.yaml)
  (✓) Agent:          travel-approver-azd-bb (AGENT_TRAVEL_APPROVER_BB_NAME)
  (✓) Version:        2 (AGENT_TRAVEL_APPROVER_BB_VERSION)
  (✓) Kind:           hosted (agent.yaml)
  (✓) Endpoint:       https://azd-bugbash-0514.services.ai.azure.com/api/projects/bugbash-westus2 (AZURE_AI_PROJECT_ENDPOINT)
  (✓) Project:        D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver (azure.yaml service "travel-approver-bb" project path)
  Eval config:      D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver\eval.yaml

   Agent Config:     D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver\.agent_configs\baseline\metadata.yaml
? Eval suite name: travel-approver-azd-bb
? Instruction file: .agent_configs\baseline\instructions.md
? Include agent traces for evaluator generation?: No
? Select the model for evaluation and generation: gpt-4o (deployed)
? Max samples (between 15 and 1000): 15
  (–) Running  Evaluator generation  (evaluatorgen-travel-approver-azd-bb-v1-3392d06e)
  (–) Running  Dataset generation  (datagen-c00db6c5b7ee4585aa9f25f7089a05a6)
  (✓) Done  Evaluator generation  (34 seconds)
  (✓) Done  Dataset generation  (2m 19s)

Eval suite created
   Config:     D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver\eval.yaml
   Dataset:    travel-approver-azd-bb (2.0)
               D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver\datasets\travel-approver-azd-bb
   Evaluator:  travel-approver-azd-bb (1)
               D:\optimization\bugbash\foundry-observability-playground\demos\build2026\agents\travel-approver\evaluators\travel-approver-azd-bb\rubric_dimensions.json

   Evaluator dimensions (6):
     Weight  Dimension
     ──────  ─────────
         10  policy_compliance
          6  budget_accuracy
          5  alternative_suggestions_specificity
          4  decision_explanation_clarity
          3  user_constraint_adherence
          5  general_quality

   Portal:
     Dataset:   https://ai.azure.com/nextgen/r/LThb9AdWSnaqlSi_ntO2JQ,rg-azdbugbash,,azd-bugbash-0514,bugbash-westus2/build/data/datasets/travel-approver-azd-bb/2.0
     Evaluator: https://ai.azure.com/nextgen/r/LThb9AdWSnaqlSi_ntO2JQ,rg-azdbugbash,,azd-bugbash-0514,bugbash-westus2/build/evaluations/catalog/travel-approver-azd-bb/1

   Next steps:
     azd ai agent eval run
       Run the eval suite against your agent.
     azd ai agent eval update
       Edit the generated dataset or evaluator locally, then upload changes.
```

#### 6b-A. Run an eval (Optional, if you want to try evaluation run)

```bash
azd ai agent eval run
```

Reads `eval.yaml`, creates the eval on the Foundry backend, and submits a run against your deployed agent.

#### 6c-A. Browse eval results (Optional)

```bash
# List all evals (table with status, run count, created date)
azd ai agent eval list

# Show details for the most recent eval (auto-resolved from azd env)
azd ai agent eval show

# Export results to JSON for offline analysis
azd ai agent eval show -O results.json
```

#### 6d-A. Optimize the agent

After the eval suite is ready, run optimize. It auto-detects the `eval.yaml` you just created.

```bash
azd ai agent optimize
```

Expected output (takes ~5–20 minutes):

```
# azd ai agent optimize
? Select an agent service: travel-zyying-new
? Found eval.yaml in project. Use it for optimization?: Yes
? Instruction file: .agent_configs\baseline\instructions.md
? Skills directory (enter to skip): skills
? Would you like to specify target models for optimization?: Yes
? Select target models for optimization (current: gpt-4o): gpt-4o (current), gpt-4.1
Optimizing agent "travel-zyying-new"...
  Config: D:\optimization\public\viveks-scratch\optimization-demo-v2\src\travel-approver-demo\eval.yaml
  Baseline saved to .agent_configs\baseline\metadata.yaml
  Job ID: opt_b1cca48e468b4a508d21bfa19cdd16de
  Status: pending
  Portal: https://eastus2euap.ai.azure.com/nextgen/r/LThb9AdWSnaqlSi_ntO2JQ,rg-azdbugbash,,azd-bugbash-0514,bugbash-westus2/build/agents/travel-zyying-new/optimization/opt_b1cca48e468b4a508d21bfa19cdd16de?flight=enable_faos_read_ui

  ⠼ completed · strategy: gepa · iteration 1 · score: 0.77 · 7m50s

Results:
  Candidate              Score    Pass   Tokens
  ──────────────────── ─────── ─────── ────────
  baseline ★              0.77    100%        0
  candidate_1             0.74    100%        0

  Candidate IDs:
    ★ baseline             cand_c6532ad867594dd4b6878a45604a4994
      candidate_1          cand_d9bedab23c5641d4a2d83c98aa635c2f

  Apply the best candidate locally, then deploy:
    azd ai agent optimize apply --candidate cand_c6532ad867594dd4b6878a45604a4994
    azd deploy
```

The ★ marks the best candidate. Copy the deploy command from the output to promote it.

#### Customizing optimization options in `eval.yaml`

You can fine-tune optimization behavior by adding or modifying the `options:` section in your `eval.yaml`. Below are all available fields, their types, and defaults:

```yaml
options:
  eval_model: "gpt-4o"                          # (string) Model used for evaluation. Default: "gpt-4o"
  target_attributes:                            # If not specify, we should auto detect it
        - instruction
        - skill
        - model
  target_config:
        model:
            - gpt-4.1
            - gpt-4.1-mini
            - gpt-4o
  budget: 0  # Deprecating                       # (int) Max optimization budget (number of candidates). Default: 5
  max_iterations: 4                              # (int) Max iterations per strategy. Default: 4 (when strategies are default)
  min_improvement: 0.0                           # (float) Minimum score improvement to accept a candidate.
  keep_versions: false                           # (bool) Keep all intermediate agent versions. Default: false
  reflection_model: ""                           # (string) Model for reflection steps. Default: "" (uses eval_model)
```

#### 6e-A. Monitor optimization jobs

```bash
# Watch a running job in real-time
azd ai agent optimize status <operation-id> --watch

# List all optimization runs
azd ai agent optimize list

# Cancel a running job
azd ai agent optimize cancel <operation-id>
```

#### 6f-A. Deploy the winning candidate

> **⚠️ Known Issue:** Due to a FAOS CANDIDATE API issue, `optimize deploy` and `optimize apply` cannot fetch candidate config at this time. This step is blocked until the API issue is resolved.
But you can check agent optimization job in foundry UI with `?flight=enable_faos_read_ui`

The optimize output includes a ready-to-use deploy command:

```bash
azd ai agent optimize deploy --candidate <candidate-id>
```

This creates a new agent version with `OPTIMIZATION_CONFIG` set to the candidate's
config (instructions, model, temperature). The agent SDK's `load_config()` reads this
at startup and applies the optimized settings.

#### 6g-A. Verify the optimized agent

> **⚠️ Blocked:** This step depends on 6f, which is currently blocked by the FAOS CANDIDATE API issue.

```bash
azd ai agent invoke "Hello!"
# Expected: agent responds using the optimized configuration
```

---

### Path B: Using your own project (optimize only, built-in dataset)

> Use this path if you have your own azd project with a deployed hosted agent on a westus2/ncus Foundry account.
> The eval APIs (`eval init`, `eval run`) require specific backend support that may not be available on your project.
> Instead, go directly to `optimize` which uses a **built-in dataset** (3 tasks, 12 criteria) — no eval setup needed.

#### 6a-B. Prerequisites

- You have an azd project with a hosted agent already deployed (`azd deploy` completed).
- Your agent uses the `agent_optimization` SDK package with `load_config()`.
- You are logged in (`az login`) and have access to the Foundry project.

#### 6b-B. Optimize the agent (built-in dataset)

From your azd project directory:

```bash
azd ai agent optimize
# → If eval.yaml exists, select "No" to use the built-in dataset
# → If no eval.yaml, it automatically uses the built-in dataset
```

Or explicitly skip the eval.yaml prompt:

```bash
azd ai agent optimize --no-prompt
# Always uses built-in defaults (3 tasks, 12 criteria)
```

Expected output (takes ~5–20 minutes):

```
Optimizing agent "your-agent"...
  Dataset: built-in (3 tasks, 12 criteria)
  Job ID: opt_abc123...
  ⠦ completed · strategy: gepa · iteration 1 · score: 0.85 · 5m0s

Results:
  Candidate              Score    Pass   Tokens
  ──────────────────── ─────── ─────── ────────
  baseline                0.60    100%      300
  baseline_instr_v1 ★     0.85    100%      980

  Deploy the best candidate:
    azd ai agent optimize deploy --candidate cand_...
```

#### 6c-B. Monitor optimization jobs

```bash
# Watch the running job
azd ai agent optimize status --watch

# List all jobs
azd ai agent optimize list

# Cancel if needed
azd ai agent optimize cancel <operation-id>
```

#### 6d-B. Deploy the winning candidate

> **⚠️ Known Issue:** Due to a FAOS CANDIDATE API issue, `optimize deploy` and `optimize apply` cannot fetch candidate config at this time.
> You can check agent optimization job results in Foundry UI with `?flight=enable_faos_read_ui`.

```bash
azd ai agent optimize deploy --candidate <candidate-id>
```

#### 6e-B. Verify the optimized agent

> **⚠️ Blocked:** This step depends on 6d-B, which is currently blocked by the FAOS CANDIDATE API issue.

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