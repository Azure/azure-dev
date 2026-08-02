# `azd ai eval` — command reference and verification status

Generated from the built binary at commit `d776507a3`, plus a live run against a real
Foundry project. Not a design document: this records what exists **today** and how much
of it is actually proven.

## How to read the status column

| Status | Meaning |
|---|---|
| **LIVE** | A committed live test exercises this path against the real service. |
| **PARTIAL** | The underlying API call is exercised live, but not every flag or branch is. |
| **UNIT** | Unit tests only. No service call is made by this command, or none is covered. |
| **NONE** | No automated coverage. Manually tried at some point, or never run. |

Two caveats that apply to the whole table, and that the column cannot express:

1. **No live test drives a CLI command.** Every live test calls the client layer
   (`evalClient.CreateOpenAIEval`, `datasetClient.UploadNewVersion`, …) directly. Flag
   parsing, prompting, `--no-prompt`, `-o json` rendering and the table output are
   covered by unit tests only. So "LIVE" means *the API path this command uses* works,
   not that the command itself was run.
2. **`TestLiveRun` skips unless `AZURE_AI_EVAL_AGENT` is set.** It is not set in normal
   runs, so the **agent-target** run path is unverified. The dataset-only run path *is*
   verified, by `TestLiveCodeEvaluatorScoresARun`.

Last full live run: **173 passed, 1 skipped, 0 failed**.

## Global flags

Available on every command.

| Flag | Description |
|---|---|
| `-C, --cwd <dir>` | Set the working directory. |
| `--debug` | Debug and diagnostics logging. |
| `-e, --environment <name>` | azd environment to use. |
| `--no-prompt` | Never prompt. Fails if a required value cannot be resolved. |
| `-o, --output <fmt>` | Output format; `json` emits machine-readable output. |

Most service-touching commands also take `--project-endpoint <url>` to override the
endpoint resolved from the azd environment.

## Composite commands

| Command | Description | Key params | Status |
|---|---|---|---|
| `init` | Scaffold `evals/azure.yaml` + `evals/eval_generate.yaml`. **Makes no service calls.** | `--target`, `--dataset`, `--evaluator` (repeatable), `--judge-model`, `--out-dir` (default `evals`), `--force` | UNIT |
| `generate` | Run the generation jobs, download the rubric and dataset, write `source:` refs into the deploy spec. | `--config` (default `evals/eval_generate.yaml`), `--deploy-config` (default `evals/azure.yaml`), `--target`, `--generation-model`, `--max-samples` (15–1000), `--trace-days`, `--agent-instruction[-file]`, `--dataset`, `--evaluator`, `--no-wait` | NONE |
| `run` | Run an evaluation, creating the eval if it does not exist. | `--config`, `--eval`, `--eval-id`, `--name`, `--level`, `--max-samples`, `--from-traces`, `--trace-window`, `--max-traces`, `--response-id`, `--max-turns`, `--wait` (default true), `--no-wait` | PARTIAL |

`run` example:

```console
$ azd ai eval run
Started run evalrun_1f3f909b... on eval eval_9cd479cc...
run reached completed: passed=2 failed=0 errored=0
```

`run` is PARTIAL because the dataset-only path is live-proven while the agent target,
`--from-traces` and `--response-id` are not.

## `dataset`

| Command | Description | Key params | Status |
|---|---|---|---|
| `dataset create` | Register a dataset, publishing a new version. | `--name`, `--file` (a `.jsonl` or a directory containing one), `--version` | LIVE |
| `dataset list` | List datasets, or the versions of one. | `--name` | PARTIAL |
| `dataset show` | Show a dataset version. | `--name`, `--version` (omit for latest) | PARTIAL |
| `dataset delete` | Delete a dataset version. | `--name`, `--version` | PARTIAL |

```console
$ azd ai eval dataset list
NAME             VERSION  FORMAT  URI
support-golden   3        jsonl   azureml://.../support-golden/versions/3
```

`TestLiveDatasetLifecycle` covers create, version increment, listing and delete through
the client — hence PARTIAL for the read/delete commands rather than LIVE.

## `evaluator`

| Command | Description | Key params | Status |
|---|---|---|---|
| `evaluator create` (rubric) | Register a rubric evaluator. | `--name`, `--rubric <json>` | NONE |
| `evaluator create` (code) | Register a code evaluator from a **single Python script**. | `--name`, `--file <script.py>`, `--image-tag`, `--init-params`, `--data-schema`, `--metrics` | LIVE |
| `evaluator list` | List the project's evaluators, versions of one, or the built-ins. | `--name`, `--builtin` | PARTIAL |
| `evaluator show` | Show an evaluator definition. | `--name`, `--version` | NONE |
| `evaluator delete` | Delete an evaluator version. | `--name`, `--version` | PARTIAL |

```console
$ azd ai eval evaluator create --name answer_length --file ./answer_length.py
Published evaluator answer_length version 1

$ azd ai eval evaluator list --builtin
NAME                    VERSION  TYPE
builtin.groundedness    16       builtin
builtin.relevance       12       builtin
```

A code evaluator script must declare a **top-level `grade(sample, item)`** returning a
float. It runs as an OpenAI python grader, which receives the script source and nothing
else — there is no import path, so a helper module beside the script cannot be imported.
Dependencies come from `--image-tag`.

`--rubric`, `evaluator show` are NONE: no live test publishes a rubric or reads a
definition back through them.
`--image-tag` reaches the definition and round-trips, but has **never been exercised
against a real custom image**.

## `run` subcommands

| Command | Description | Key params | Status |
|---|---|---|---|
| `run start` | Start a run, creating the eval if needed. Same flags as `run`. | as `run` | PARTIAL |
| `run list` | List runs for an eval. | `[eval-id]`, `--eval`, `--eval-id`, `--limit` | NONE |
| `run show` | Show one run. | `[eval-id]`, `--run-id` (defaults to most recent) | PARTIAL |
| `run cancel` | Cancel an in-flight run. | `[eval-id]`, `--run-id` | PARTIAL |
| `run delete` | Delete a run. | `[eval-id]`, `--run-id` | NONE |

```console
$ azd ai eval run list
RUN ID              NAME                  STATUS     RESULTS
evalrun_1f3f909b... pr-gate-1785370812    completed  2 passed, 0 failed, 0 errored
```

Every command taking an eval id accepts it as the argument, as `--eval-id <id>`, or as
`--eval <name>` to name one from the config.

## `results`

| Command | Description | Key params | Status |
|---|---|---|---|
| `results show` | Per-sample results for a run (`output_items`). | `<eval-id>`, `--run-id`, `--failed-only`, `-O/--out-file` | NONE |
| `results export` | Export run results. | `<eval-id>`, `--run-id`, `--format json\|csv`, `-O/--out-file` | NONE |
| `results compare` | Compare runs against a baseline. | `[eval-id]`, `--baseline`, `--treatment` (repeatable), `--name` | NONE |

The root help still lists `dataset`, `evaluator`, `generate`, `init`, `results` and
`run`. `schedule` is gone from it; see below.

```console
$ azd ai eval results show
ITEM  EVALUATOR      RESULT  SCORE  INPUT                REASON
1     answer_length  pass    14.0   a short answer       -
2     answer_length  pass    46.0   a considerably long… -

$ azd ai eval results compare
METRIC         TREATMENT RUN  BASELINE  TREATMENT  DELTA  P-VALUE  EFFECT
groundedness   evalrun_a1b2…  3.80      4.20       +0.40  0.031    small
```

`--baseline` defaults to the second most recent completed run and `--treatment` to the
most recent. That auto-selection is untested against real run history.

**TODO (April, spec review 2026-07-29):** `compare` and `export` belong at the **run**
level, not under `results` — *"compare is not at the items level… export should be at the
run level"*. `results show` should become `run output list`, paginating `output_items`.

## `schedule` — not on this branch

Scheduling is implemented and live-tested, but lives on
`feat/azure-ai-evaluations-schedule` rather than here. It is out of M1 so the first
release stays focused on the eval / run / results loop.

Re-adding it is four files plus one line: `internal/cmd/schedule.go`,
`internal/cmd/schedule_test.go`, `internal/cmd/schedule_live_test.go`,
`internal/pkg/eval_api/schedules.go`, and `newScheduleCommand()` in `root.go`.
Nothing else ever referenced it — the two error helpers it used to carry,
`IsNotFound` and `IsConflict`, now live in `internal/pkg/eval_api/errors.go`, which
is where they belonged anyway.

What the live tests on that branch establish:

- Every trigger shape the CLI can emit — cron, hourly, daily, weekly, monthly,
  interval, one-time — is accepted by the service and survives a round trip.
- **Schedules need a permission nothing else does.** A schedule fires later and runs
  as the project, so the project's managed identity must hold the **Foundry User**
  role on the project. Without it every create is refused with `PermissionDenied`.
  The tests skip, naming the missing role, rather than reporting a false regression.
- Service constraints: one schedule per project; no in-place edits; a schedule
  repeating a `--from-traces` run accepts only `--every hourly`.

## Summary of gaps

| Gap | Impact |
|---|---|
| No live test drives a CLI command | Flag parsing, prompting and rendering are unit-tested only |
| `compare` has no live coverage | Named in M1 exit criteria, unproven; needs two completed runs to test |
| `generate` has no live coverage | The most complex composite command |
| `TestLiveRun` skips | Agent-target runs, `--from-traces`, `--response-id` all unverified |
| `--image-tag` never used with a real image | The only supported way to give a code evaluator dependencies |
| `azd up` cannot configure a code evaluator | Reconciler passes empty options; no config fields for `data_schema`, `metrics`, `init_params`, `image_tag` |
| Default metric name `result` is invented | Real evaluators use semantic names (`groundedness`, `relevance`) |
