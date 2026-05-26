---
short: Generate, run, and iterate on evals end-to-end.
order: 35
---
# Evaluate: generate, run, and iterate on evals

Audience: an AI coding assistant testing a deployed agent end-to-end: generate an eval suite, run it, read the results, edit and rerun. This topic walks the full eval lifecycle as a single thread so the workflow reads top-to-bottom.

Every command in this topic targets a DEPLOYED agent on Foundry. Run the `deploy` topic first if `azd ai agent show` returns `status: "not_deployed"`.

Most write commands here are BILLED. They emit the standard confirmation envelope on `--no-prompt` without `--force`. See the `operate` topic for the envelope contract.

---

## The lifecycle

```
       (deployed agent)
              |
              v
1. azd ai agent eval init     <- generate eval.yaml + dataset + evaluators
              |
              v
2. azd ai agent eval run      <- billed: run the eval suite
              |
              v
3. azd ai agent eval show     <- read the run results
              |
   (edit eval.yaml? new data?)
              |
              v
4. azd ai agent eval update   <- billed: upload local edits as new versions
              |
              v
        re-run (step 2)
```

Background, read-only at any point:

```bash
azd ai agent eval list --output json
azd ai agent eval show <eval-id> --output json
```

---

## Step 1 -- Generate the eval suite

```bash
azd ai agent eval init --dry-run
azd ai agent eval init --force
```

What this does:

* Submits a dataset-generation job (billed).
* Submits an evaluator-generation job (billed).
* Waits for both to finish (poll), then downloads the artifacts.
* Writes `eval.yaml` at the agent project root.
* Writes the generated dataset + evaluator files alongside it.

Useful flags:

* `--agent <name>` -- target agent. Auto-detected from `azure.yaml`.
* `--dataset <path-or-name>` -- skip dataset generation; use an existing local file or a registered dataset.
* `--evaluator <name>` (repeatable) -- skip evaluator generation; use built-in or pre-registered evaluators.
* `--max-samples <n>` -- generated dataset size (15-1000).
* `--gen-instruction "<text>"` / `--gen-instruction-file <path>` -- override the agent instruction used during generation.
* `--trace-days <n>` -- include the last N days of real agent traces in evaluator generation (0 = no traces).
* `--eval-model <name>` -- model used for evaluator generation. Pick from `azd ai agent connection list`.
* `--no-wait` -- submit jobs and exit without polling; the OP IDs are written into `eval.yaml` for later resolution.
* `--reset-defaults` -- overwrite an existing `eval.yaml`.
* `--out-file <path>` -- write `eval.yaml` somewhere other than the agent project root.

Confirmation envelope: this is BILLED, so `--no-prompt` without `--force` returns exit 2 with the standard envelope. Summarize `changes[]` for the human and re-run `confirmCommand` after consent.

If `--no-wait` was used, the resulting `eval.yaml` contains `pendingOperations:` blocks. Re-run `eval init` once they complete to materialize the artifacts.

---

## Step 2 -- Run the eval

```bash
azd ai agent eval run --dry-run
azd ai agent eval run --force
```

What this does:

* Reads `eval.yaml` from the agent project root.
* Submits an eval run (billed).
* Polls until the run completes (default) and prints the result summary.

Useful flags:

* `--config <path>` -- explicit `eval.yaml` location.
* `--name <name>` -- override the eval run name (defaults to the config's eval name).
* `--no-wait` -- start the run and return immediately. Use `eval show --eval-run-id <id>` later to fetch results.

Each `eval run` is BILLED -- the envelope describes the agent and dataset that will be exercised.

---

## Step 3 -- Inspect results

```bash
# Latest eval run for the current agent
azd ai agent eval show --output json

# Specific eval
azd ai agent eval show <eval-id> --output json

# Specific run within an eval
azd ai agent eval show <eval-id> --eval-run-id <run-id> --output json

# Export full results to a file
azd ai agent eval show <eval-id> --eval-run-id <run-id> -O results.json

# Limit the number of runs returned per eval
azd ai agent eval show <eval-id> --limit 5 --output json
```

`eval list` returns the lightweight catalog:

```bash
azd ai agent eval list --output json
```

```json
{
  "items": [
    {
      "id": "eval-id",
      "name": "smoke-core",
      "active": true,
      "runCount": 4,
      "lastRunStatus": "completed",
      "createdBy": "alice@example.com",
      "createdAt": 1737045821
    }
  ]
}
```

`eval show` with `--eval-run-id` returns the full OpenAIEvalRun object under `.run` plus the eval id under `.eval`. Use `-O <file>` when the payload is large (full per-sample traces).

---

## Step 4 -- Edit and update

When you change a dataset file (JSONL) or an evaluator rubric file locally and want the next run to use the new versions:

```bash
azd ai agent eval update --dry-run
azd ai agent eval update --force
```

Default behavior: detect every asset that has local changes (dataset JSONL files referenced via `local_uri:` and evaluator rubric files referenced via `local_uri:`), upload new versions, and rewrite the version numbers in `eval.yaml`.

Useful flags:

* `--dataset-only` -- skip evaluators.
* `--evaluator-only` -- skip the dataset.
* `--config <path>` -- explicit `eval.yaml` location.

This is BILLED (uploads create new versions). The envelope lists exactly which assets will get new versions.

After `eval update`, re-run `eval run` to exercise the new versions.

---

## Local edits round-trip

`eval.yaml` is the source of truth for which dataset and evaluators a run uses. Two common patterns:

1. **Edit dataset directly** -- `local_uri:` in the dataset block points at a JSONL file in the repo. Add/remove samples, save, then `eval update --dataset-only --force`.
2. **Add a custom evaluator** -- append an evaluator block to `eval.yaml` with `local_uri:` pointing at a rubric file; run `eval update --evaluator-only --force` to upload it.

Validate before committing:

```bash
azd ai agent doctor --output json
```

Look for the `eval-config-valid` check; failures name the field path.

---

## Cross-link: optimize

The `optimize` subgroup ALSO submits billed jobs (see `operate`) and shares the same evaluator + dataset definitions. After a clean eval baseline:

```bash
azd ai agent optimize --target instruction --force
```

submits an optimization run that uses the active eval to score candidate prompt instructions. The optimization deeper-dive lives in `operate` (write side) and `investigate` (read side).

---

## Common eval error codes

| `code`                | What it means                                    | Fix                                                          |
| --------------------- | ------------------------------------------------ | ------------------------------------------------------------ |
| `eval_config_invalid` | `eval.yaml` failed schema validation             | `azd ai agent doctor --output json`; fix the named field     |
| `eval_not_found`      | The named eval id is gone or never existed       | Re-list with `eval list`                                     |
| `eval_run_not_found`  | The eval run id is gone or never existed         | Re-list runs with `eval show <eval-id>`                      |
| `dataset_pending`     | A pending dataset job is still running           | Wait, re-run `eval init` without `--no-wait` to materialize  |
| `evaluator_pending`   | A pending evaluator job is still running         | Wait, re-run `eval init` without `--no-wait` to materialize  |

---

## What this topic does NOT cover

* Optimization commands -- see `operate` (write side) and `investigate` (read side).
* Deploying the agent under test -- see `deploy`.
* Editing the agent's `agent.yaml` -- see `extend`.
