# `azd ai eval` — Implementation Handoff

> Self-contained brief for building the **`azure.ai.evaluations`** azd extension (`azd ai eval`). Everything below is verified against the shipping `azure.ai.agents` extension, azd core, and RAISvc source. Design source of truth is `spec.md` in `foundrysdk_specs/specs/evaluations/azd_eval_extension/` — if the two disagree, the spec wins.
>
> **Uncommitted working document.** Not part of any PR.

---

## 0. TL;DR

A new azd extension — id `azure.ai.evaluations`, namespace `azd ai eval` — that is a thin Go client over the **existing** Foundry evaluations data plane, plus **one azd service-target provider** for `host: azure.ai.evals`.

**Non-negotiables**

1. **Two-tier commands.** Atomic (`dataset` / `evaluator` / `run` / `results`) map ~1:1 to the API. Composite (`init` / `generate` / `run`) are wrappers, never the only path.
2. **No `deploy` command.** Deployment is `azd up` / `azd deploy` invoking our service-target provider. We ship no deploy verb.
3. **`generate` is separate from deploy.** Generate once, deploy the artifacts to many environments.
4. **`init` touches no network.**
5. **Everything is built on APIs that exist today.** No service changes.
6. **Deterministic.** `-o json` + `--no-prompt` everywhere; a supplied flag fully suppresses its prompt.

**Out of scope for M1:** scheduled/continuous eval, baseline comparison (both exist server-side — M2), non-agent targets, traces as a run data source, eval by response/run id, `optimize` (stays in the agents extension).

---

## 1. Where the code lives & how to build it

### 1.1 Paths
- **Repo:** `Azure/azure-dev`.
- **New extension:** `cli/azd/extensions/azure.ai.evaluations/`
- **Reference to copy:** `cli/azd/extensions/azure.ai.agents/` — closest analog, currently hosts `azd ai agent eval …`. **Read it first.**

### 1.2 File layout (mirror the agents extension)
```
cli/azd/extensions/azure.ai.evaluations/
├── internal/
│   ├── cmd/             # cobra commands, one file per group; listen.go wires the provider
│   ├── pkg/eval_api/    # data-plane client (lifted, see §1.4)
│   ├── pkg/dataset_api/ # dataset client (lifted)
│   └── project/         # azure.yaml service-entry model + YAML round-trip
├── schemas/             # JSON schemas
├── tests/
├── extension.yaml       # manifest
├── go.mod / go.sum
├── main.go
└── version.txt
```

**`extension.yaml`** — note the `service-target-provider` capability and `providers` block; both are required for `azd up` to route to us:
```yaml
# yaml-language-server: $schema=../extension.schema.json
id: azure.ai.evaluations
namespace: ai.eval                  # dotted → CLI surface `azd ai eval`
displayName: Foundry evaluations (Beta)
description: Define and run Foundry evaluations from your terminal. (Beta)
usage: azd ai eval <command> [options]
version: 1.0.0-beta.1               # keep version.txt in sync
requiredAzdVersion: ">=1.27.1"
language: go
capabilities:
  - custom-commands
  - lifecycle-events
  - service-target-provider
  - metadata
providers:
  - name: azure.ai.evals
    type: service-target
    description: Deploys evaluation datasets, evaluators, and eval groups to Foundry
```

### 1.3 Stack facts (verified)
- **Go 1.26.x**, **cobra**. Entry point:
  ```go
  package main
  import (
      "azureaieval/internal/cmd"
      "github.com/azure/azure-dev/cli/azd/pkg/azdext"
  )
  func main() { azdext.Run(cmd.NewRootCommand()) }
  ```
- SDK module `github.com/azure/azure-dev/cli/azd` (agents pins `v1.28.0`); surface is `pkg/azdext`.
- Data plane over REST using the **azcore pipeline**, not raw `net/http`: bearer-token policy scoped to **`https://ai.azure.com/.default`**, plus `azsdk.NewMsCorrelationPolicy()` and `azsdk.NewUserAgentPolicy(...)`.
- Dev loop:
  ```bash
  azd ext install microsoft.azd.extensions       # one-time
  cd cli/azd/extensions/azure.ai.evaluations
  azd x build                                    # build + install locally
  azd x watch                                    # ongoing
  ```

### 1.4 What to lift from `azure.ai.agents`

Measured, non-test:

| Source | Files | LOC | Gives you |
|---|---|---|---|
| `internal/pkg/agents/eval_api/` | 7 | 1,350 | `EvalClient` over `/data_generation_jobs`, `/evaluator_generation_jobs`, `/evaluators`, `/datasets`, `/openai/v1/evals`; LRO poller; artifact download; portal URLs; api-version constants |
| `internal/pkg/agents/dataset_api/` | 2 | 550 | Full pending-upload → blob → finalize → download |
| `internal/pkg/agents/opt_eval/` | 2 | 555 | eval.yaml config model (adapt, don't copy wholesale) |
| `internal/cmd/eval_*.go` | 10 | 2,890 | generate / run / show / list / update / progress UX |

**≈1,900 LOC of API client is effectively done.** Genuinely net-new:

1. **The service-target provider** (§4) — no prior art.
2. **Change detection** (§5) — no prior art.
3. **YAML round-trip merge** for `generate` writing `source:` back (§6).
4. The atomic command layer and the offline `init`.

> Do **not** port the `/evaluation_suites` client. That endpoint is abandoned — the eval group is the unit.

---

## 2. Data-plane API contract

### 2.1 Base, auth, api-versions
- **Base:** azd env `FOUNDRY_PROJECT_ENDPOINT`, shape `https://{resource}.services.ai.azure.com/api/projects/{project}/…`. Global `--project-endpoint` overrides.
- **Scope:** `https://ai.azure.com/.default`.
- **api-versions:** project-endpoint calls (datasets, evaluators) use **`2025-11-15-preview`**; data generation uses **`v1`**; **`/openai/v1/evals*` sends no api-version**.

### 2.2 Datasets
| Command | Calls |
|---|---|
| `dataset create` / `update` | `POST /datasets` *(first version only)* → `POST /datasets/{name}/versions/{v}/startPendingUpload` → `PUT <blob SAS>` → `PUT /datasets/{name}/versions/{v}` |
| `dataset list` | `GET /datasets` |
| `dataset show` | `GET /datasets/{name}/versions/{v}` |
| `dataset delete` | `DELETE /datasets/{name}/versions/{v}` |

Model — **note there is no content hash or etag**, which drives §5:
```go
type Dataset struct { Name, Version, BlobURI, Format, DataURI, ContentURI string }
```
A dataset is a **single `.jsonl`**. A directory today just picks the first `.jsonl`; no folder walk.

### 2.3 Evaluators
| Command | Calls |
|---|---|
| `evaluator upload` / `update` | *(code only)* pending-upload → blob upload; then `POST /evaluators/{name}/versions` |
| `evaluator show` | `GET /evaluators/{name}` — returns the definition inline |
| `evaluator builtins` | `GET /evaluators?type=Builtin` |

Built-ins are referenced as `builtin.<name>`; the prefix is stripped before the value goes into `testing_criteria[].evaluator_name`. Custom-evaluator upload needs the project MI to hold **Azure AI User**.

### 2.4 Eval groups and runs (OpenAI-compatible, no api-version)
```go
type CreateOpenAIEvalRequest struct {
    Name             string
    Metadata         map[string]string
    DataSourceConfig *DataSourceConfig   // {Type, ItemSchema, IncludeSampleSchema}
    TestingCriteria  []TestingCriterion  // the evaluators
}
type TestingCriterion struct {
    Type, Name, EvaluatorName string
    InitializationParameters  map[string]any     // threshold lives here
    DataMapping               map[string]string
}
type OpenAIEval struct { ID, Name string }      // ID is canonical; Name is NOT unique
```

| Command | Calls |
|---|---|
| create group | `POST /openai/v1/evals` |
| get / list | `GET /openai/v1/evals/{id}` · `GET /openai/v1/evals?limit=` |
| start run | `POST /openai/v1/evals/{evalId}/runs` |
| poll / list runs | `GET /openai/v1/evals/{evalId}/runs/{runId}` · `GET …/runs` |
| cancel | `POST /openai/v1/evals/{evalId}/runs/{runId}` with an empty body |
| results | `GET …/runs/{runId}` → `result_counts` + `per_testing_criteria_results` |

**The group carries evaluators, not the dataset.** The dataset goes on the **run**. `evaluation_level` is `turn` | `conversation`, service default **`turn`**.

**`data_source_config` and `data_mapping` are derived from each evaluator's published contract.** The original plan was to copy the agents extension's hardcoded mapping. Live testing showed that is wrong: it only suits agent-target quality evaluators and the service rejects the rest.

`GET /evaluators` returns a contract per evaluator:
```jsonc
"supported_evaluation_levels": ["turn"],
"definition": {
  "data_schema":     { "required": ["response", "instruction_id_list"], "properties": { … } },
  "init_parameters": { "required": ["deployment_name"], "properties": { … } }
}
```

`internal/cmd/build.go` reads it and, per criterion:
- binds each accepted input to the agent sample (`response`, `tool_calls`, `tool_definitions`) or to a dataset column `{{item.<field>}}`;
- declares the referenced columns in the item schema;
- filters `initialization_parameters` to the declared properties — no evaluator accepts `model`, and `builtin.ifeval` accepts nothing;
- validates `--level` against `supported_evaluation_levels`;
- reports a missing required column locally, naming it.

Two service rules are encoded: `messages` and `query`/`response` are mutually exclusive (the level selects), and `evaluation_level` is an **initialization parameter**, not run metadata. An evaluator with no published contract falls back to the agent-target shape.

Covered by `build_test.go`, and by `build_live_test.go` which posts a group for every built-in the project exposes.

### 2.5 Generation (LRO)
`POST /data_generation_jobs` and `POST /evaluator_generation_jobs`, each polled by `GET …/{id}`. The ~11-minute "timeout" is a **client poll budget (2 s × 300)**, not a service limit — raise it and default to `--no-wait` in CI.

### 2.6 M2 only — schedules and comparison
Both are **project-endpoint reachable** and **feature-gated per project**:
- `/schedules` — `PUT {id}` · `GET {id}` · `GET` · `DELETE {id}`; requires `FoundryFeature.Schedules_V1Preview`. Trigger is `Cron{Expression, StartTime, EndTime, Timezone}` or `Recurrence{Frequency, Interval, Schedule}`.
- Insights compare — `POST /insights` (async) or `POST /insights/sync`, body `{evalId, baselineRunId, treatmentRunIds}`; requires `FoundryFeature.Insights_V1Preview`.

---

## 3. Configuration model

Two files. Neither is loaded by azd core — **we parse both**.

**`evals/eval_generate.yaml`** — input to `generate`, never deployed. `agent.context.{instructions,tools}` are file paths; `local_dir` accepts a directory or an explicit file path.

**`evals/azure.yaml`** — the deployment spec, `$ref`'d from the root `azure.yaml`:
```yaml
# /azure.yaml
services:
  evals:
    host: azure.ai.evals
    uses: [ai-project]
    $ref: ./evals/azure.yaml
```
It carries three arrays: `evaluators[]`, `datasets[]`, `evalGroups[]` (see `spec.md` for the full shape).

**Why arrays on a service work.** azd core's `ServiceConfig` captures unknown keys:
```go
AdditionalProperties map[string]any `yaml:",inline"`
```
and hands them to the extension, which unmarshals them itself — the pattern `LoadServiceTargetAgentConfig` → `ServiceConfigProps` uses. `azure.ai.project` already carries `deployments[]` this way. **`$ref` resolution is ours too**: `pkg/foundry.ResolveFileRefs(cfg, projectRoot)`, called by the extension, not by azd.

---

## 4. The service-target provider (net-new, highest risk)

Wire it in `listen.go`, mirroring `azure.ai.agents`:
```go
func configureExtensionHost(host *azdext.ExtensionHost) {
    azdClient := host.Client()
    host.
        WithServiceTarget("azure.ai.evals", func() azdext.ServiceTargetProvider {
            return project.NewEvalServiceTargetProvider(azdClient)
        }).
        WithServiceEventHandler("postdeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
            return postdeployHandler(ctx, azdClient, args)
        }, &azdext.ServiceEventOptions{Host: "azure.ai.evals"})
}
```

`ServiceTargetProvider` requires `Initialize`, `Endpoints`, `GetTargetResource`, `Package`, `Publish`, `Deploy`. For eval, **`Package` and `Publish` are near no-ops**; `Deploy` does the work, in this fixed order:

1. **Datasets** — change-detect (§5); if changed, run the `dataset create` sequence.
2. **Evaluators** — `GET /evaluators/{name}`, compare the definition, upload only if different.
3. **Drift check** — if the server's latest version is ahead of the recorded one, fail with "sync first".
4. **Eval groups** — `POST /openai/v1/evals` with `testing_criteria` from the resolved evaluator versions. Groups are immutable, so only recreate when the resolved versions or options actually changed.
5. Persist resolved ids, versions, and fingerprints to the azd env.

**How azd reaches us:** `azd up` runs one DAG; per service it calls `GetServiceTarget()`, which does `serviceLocator.ResolveNamed(host, &target)`. If our extension is not installed, azd fails that service with *"install an extension that provides this host."* We implement **no sequencing or rollback across services** — `uses:` and the DAG handle that.

---

## 5. Change detection (net-new)

Without this, every `azd up` publishes a redundant version.

- **Datasets** — the API returns no hash or etag, so comparing against the server would mean downloading the blob every deploy. Instead: **SHA-256 the local file**, store it with the resolved version in the azd env, re-hash locally next deploy, skip when unchanged.
- **Evaluators** — definitions come back inline from `GET /evaluators/{name}`; compare directly, no cache needed.
- **Drift** — a *version* comparison, not content: server latest vs. the version recorded at last deploy.

**Open:** how to fingerprint a **code** evaluator (a folder). Suggest hashing sorted relative paths + contents, excluding `__pycache__` and `.pyc`.

---

## 6. `generate` writes back into `evals/azure.yaml`

After downloading artifacts, `generate` adds/updates `source:` references. Requirements:

- Match entries **by `name`**; update `source` in place; append when absent.
- **Preserve comments and key order** — use the `yaml.v3` Node API, not plain marshal/unmarshal.
- Do not clobber a field the user hand-edited other than `source`.
- If the array is itself a `$ref`, write into the referenced file.

There is no `emitDeploymentConfig` block — this is default behavior, not configurable.

---

## 6a. What generation is seeded from

The generation API takes an `agent` source that is meant to pull the agent's own instructions, and it fails for every agent (§11d). The client resolves that context itself, most specific first:

1. `--gen-instruction` / `--gen-instruction-file`
2. the file named by `agent.context.instructions`, resolved **relative to the spec that declared it**, not the working directory
3. the agent's published instructions — `GET /agents/{name}` → `versions.latest.definition.instructions`

Step 2 tolerates a missing file on purpose: `init` writes the path before the file exists, so treating the gap as an error would break the flow init scaffolds. Step 3 is what makes `init` → `generate` work with nothing authored.

The agent source is still sent. When the service starts honouring it, it contributes on top of the prompt; nothing has to be removed.

`agent.context.tools` is still read by nothing, so it is warned about rather than dropped silently, and `init` no longer scaffolds it — a warning for a field the user never chose is just noise.

---

## 7. Behavioral bugs to fix (measured in `azd ai agent eval`)

Treat each as an acceptance criterion.

1. **Path handling (highest priority).** `--out-file` is re-rooted under the agent directory; `--config` re-roots again. **Fix:** treat paths as relative to CWD (or `-C/--cwd`), used verbatim, single-rooted. Test `./x.yaml`, `../x/x.yaml`, absolute.
2. **Wizard overrides flags.** Prompts still fire when flags are supplied, and pre-filled prompts *append* typed input. **Fix:** a supplied flag fully suppresses its prompt; `--no-prompt` errors on a missing required value.
3. **`--evaluator` ignored during generation.** Passing `--evaluator` does not stop rubric generation. **Fix:** honor it, skip that generation.
4. **Client-side generation timeout.** Resolved: it is a client poll budget, not a service limit. Raise it; default `--no-wait` under `--no-prompt`.
5. **Shallow results.** `eval show` returns counts only. **Fix:** per-sample scores via `per_testing_criteria_results`.
6. **Auth friction.** Native azd token failed with "Reauthentication required"; workaround `azd config set auth.useAzCliAuth true`. Detect and surface clearly.

---

## 8. azd environment

Extensions read and write env values themselves via `azdClient.Environment().GetValue / SetValue` — azd sets none of these. The agents extension does this from lifecycle handlers.

| Key | Written by |
|---|---|
| `FOUNDRY_PROJECT_ENDPOINT` | consumed, not written |
| `EVAL_GROUP_ID` | provider during `azd up`; `run` when it creates the group |
| `EVAL_DATASET_VERSION`, artifact fingerprints | provider during `azd up` |
| `EVAL_RUN_ID` | `run` |

Setting `EVAL_GROUP_ID` manually targets a pre-existing group; `--eval-id` does the same per-invocation.

---

## 9. Build order

M1 is everything in the spec. Within it, build in dependency order:

| Step | Work | Done when |
|---|---|---|
| **1. Scaffold** | Extension skeleton, `extension.yaml`, `main.go`, root cobra command, local install via `azd x build` | `azd ai eval --help` works |
| **2. Lift the clients** | Copy `eval_api` + `dataset_api`, de-agent-scope, keep api-version constants | Unit tests pass against an `httptest` fake |
| **3. Atomic commands** | `dataset`, `evaluator`, `run`, `results` with `-o json` / `--no-prompt` | **E2E-1** below |
| **4. Config model** | `evals/azure.yaml` load, `$ref` resolve via `pkg/foundry.ResolveFileRefs`, validation | Round-trip test preserves comments |
| **5. Service-target provider** | `listen.go` wiring + `Deploy` reconciliation + change detection + drift | `azd up` creates all three resource kinds; second `azd up` is a no-op |
| **6. `init`** | Offline scaffold of both YAMLs | Runs with no network/auth |
| **7. `generate`** | Generation LROs, artifact download, write-back into `evals/azure.yaml` | **E2E-3** |
| **8. `run`** | Group resolve-or-create, run, poll, render | **E2E-2** |

Steps 1–3 are mostly mechanical. **Step 5 is the risk** — budget accordingly.

---

## 10. Testing

| Tier | Coverage | Auth | Where |
|---|---|---|---|
| **0 — offline** | flag parsing; YAML round-trip; path resolution (§7.1); flag→prompt suppression (§7.2); request bodies against an `httptest` fake (copy `eval_api_version_test.go`); schema validation | No | PR gate |
| **1 — `init` record/playback** | interactive prompt flows | No | PR gate |
| **2 — live golden path** | full flows against a real Foundry project | Yes | On-demand/scheduled, **not** the PR gate |

Tier 2: env-gate on `AZURE_AI_EVAL_E2E_LIVE=1`, build tag `//go:build linux` (needs a PTY), drive `init` prompts via `go-expect`+`vt10x`+`creack/pty`, everything else through `--no-prompt -o json`. `t.Cleanup` must delete every version it created.

**Golden paths**

- **E2E-1 — atomic:** `dataset create` → `evaluator upload` → `run start` → `results export`. Assert valid JSON, resolved versions, terminal run status, **per-sample** scores, and that re-running `create` yields the *next* version rather than an error.
- **E2E-2 — init → azd up → run:** `init` makes **zero network calls** (run it unauthenticated), writes both YAMLs, does not double the path; `azd up` creates the resources and pins versions back; a second `azd up` creates **no new versions**; `run` completes with no prompt.
- **E2E-3 — generate:** completes without a client timeout; a supplied `--evaluator` is honored; artifacts land locally and `evals/azure.yaml` gains correct `source:` entries with comments preserved.
- **E2E-4 — CI invariants:** every command with `--no-prompt -o json` is non-interactive, emits parseable JSON, and exits non-zero on a missing required value.

---

## 11. Decisions still open

| # | Question | Blocks | Suggested default |
|---|---|---|---|
| 1 | Host name `azure.ai.evals` — agreed? Not registered anywhere yet | Step 1 | Use it; renaming is cheap before publish |
| 2 | Which storage connection for evaluator pending-upload (`connectionName`) | Step 3 | Project default; expose a flag |
| 3 | Are **code** evaluators in M1, or rubric-only? | Steps 3, 5 | Rubric-only for M1 — removes the folder-hashing problem entirely |
| 4 | Where do fingerprints live — azd env or a lock file? | Step 5 | azd env, so they are environment-scoped |
| 5 | Do we depend on `azure.ai.projects` for the project service? | Step 1 | Yes, mirror the agents manifest |
| 6 | Bundling into `microsoft.foundry` — who owns it | Ship | Extensions team |

---

## 11b. Assumptions made while implementing

Recorded for review. Anything marked **corrected** was an assumption that live testing disproved; the code already reflects the correction.

| # | Assumption | Status |
|---|---|---|
| 1 | Dataset versions are decimal (`1.0`, `2.0`) | **Verified live** — `UploadNewVersion` advanced 1.0 → 2.0 |
| 2 | `--wait` defaults true for `run` | Held; matches the spec's blocking-by-default UX |
| 3 | `evaluation_level` travels as run **metadata** | **Corrected** — it is an `initialization_parameters` property on evaluators that declare it. Metadata had no effect |
| 4 | A cached eval group id that 404s means recreate | Held; not yet exercised live |
| 5 | `dataset create` accepts a file or a directory | Held; the upload helper scans a directory for the first `.jsonl` |
| 6 | Evaluator sameness compares only the `definition` block | Held; avoids server-assigned version/timestamp churn |
| 7 | `GetTargetResource` returns a subscription-only resource | Held; eval resources have no ARM resource |
| 8 | Rubric evaluators only in M1; code evaluators in M2 | Open decision 3 |
| 9 | One fixed data mapping suits all evaluators | **Corrected** — contracts differ per evaluator; the mapping is now derived from the published contract |
| 10 | Dataset URIs come back snake_case | **Corrected** — the project endpoint returns camelCase (`dataUri`). Both spellings are now bound |
| 11 | A dataset blob URI can be downloaded directly | **Corrected** — true only for uploads. A *generated* dataset's URI names the container, not the blob, with `isSingleFile` true either way, and downloading a container returns 409. The URI also carries no SAS, so a credential is always needed. Downloads now fetch a credential and list the container when the URI does not name a file |
| 22 | Agent-seeded data generation would be fixed service-side before ship | **Corrected** — traced to the AOAI generator, outside this repo. `generate` now reads the agent's instructions itself and passes them as the prompt source; the agent source is still sent so it contributes once fixed |
| 23 | `agent.context.instructions` and `.tools` were wired up | **Corrected** — both were written by `init`, declared on the config, and read by nothing. `instructions` is now honoured; `tools` is warned about and no longer scaffolded |
| 24 | The service would reject a missing generation model clearly | **Corrected** — it fails partway through the command with a message naming nothing the caller controls. Checked up front instead |
| 25 | `max_samples` was free-form | **Corrected** — the service requires 15–1000. The config already validated this; the floor is now documented in the spec |
| 26 | Schedule creation would be a POST to a collection | **Corrected** — `POST` 404s on every route. It is `PUT /schedules/{name}`, a named resource |
| 27 | The bodiless 400s meant `displayName`/`description`/`enabled` were required | **Corrected, and this one was my error** — in that probe only the *first* create succeeded and I read the rest as field validation. The real cause is one schedule per project. Re-tested from a drained state, a minimal body creates fine |
| 28 | A named PUT would update in place | **Corrected** — accepted, echoes the new body, changes nothing. `set` refuses an existing name instead of reporting a change that did not happen |
| 29 | Deleting and recreating under the same name would work as a replace | **Corrected** — the replacement never leaves `Creating` and cannot then be deleted. The `--replace` flag was removed before shipping |
| 30 | M4's "traces as a run data source" was awaiting service support | **Corrected** — `azure_ai_traces` is in the run data-source discriminator and the service executes it. The note was never re-tested. Shipped as `run --from-traces` |
| 31 | Traces were a generation input only, never a run's data source | **Corrected** — that comment described the *generation* API. The run API takes them directly |
| 32 | The traces window could be sent as `start_time`/`end_time` | **Corrected, and this one I shipped** — the data source has no start bound. `start_time` is accepted and discarded, leaving the default 7 days. It looked right only because the first value I tested, 7d, *is* the default; 30d silently queried a week. Now sends `lookback_hours` |
| 33 | M4's "evaluation by response id" was awaiting service support | **Corrected** — works today. The ids are not a list on the data source: they are JSONL rows plus a `data_mapping` to `response_id` |
| 34 | `target.type: model` was unsupported | **Corrected** — supported. The config rejected it by name *and* the test used it as the example of an unsupported type, so the gap read as deliberate in two places. Sample bindings now follow the target kind, since a model returns `output_text` where an agent returns `output_items` |
| 35 | A run could reference a registered dataset by name as a `file_id` | **Corrected** — `file_id` means an uploaded file; a dataset name is rejected with `invalid data source file ids`. Registered datasets are fetched and sent inline. Every earlier test used a local `source:`, so this path had never run |
| 36 | M4's "subsetting a registered dataset" needed service support | **Corrected** — the service cannot narrow a file reference, but fetching the rows client-side makes `--max-samples` mean the same thing for any dataset |
| 37 | One env key per resolved id was enough | **Corrected** — only true for a single-group config. With two, the second deploy handed the first group the second's id and both declarations pointed at one group. Ids are now keyed by name, as fingerprints already were |
| 38 | The remembered run id could be shared | **Corrected** — same shape as 37. Asking group A for its latest fetched group B's run inside A and 404'd |
| 39 | A dataset's `version:` was the version published | **Corrected** — it was passed to the helper that *counts from* its argument, so `1.0` published 2.0. It also meant two things: unchanged content resolved to it, changed content published above it |
| 40 | An evaluator's `version:` behaved like a dataset's | **Corrected** — the service assigns an evaluator's version on publish, so a pin alongside `source:` was never honoured. A config asking for 7 deployed 1 silently. Now refused |
| 41 | Criteria were being shaped from each built-in's published schema | **Corrected, and this one invalidated an earlier §11c row** — the schemas were fetched with an unfiltered list, which returns only the project's own evaluators. Every built-in fell back to `legacyInputs`. It matched query/response so nothing looked wrong; `task_completion` at conversation level published an empty `data_mapping` |
| 42 | A run needs a target | **Corrected** — a dataset holding both sides of the exchange has nothing to invoke, and the service runs it. The requirement was ours |
| 12 | `$ref` is resolved by azd core before the extension sees the config | **Corrected** — core leaves `$ref` for the owning extension. The provider now calls `foundry.ResolveFileRefs`, and relative `source:` paths are based on the included file's directory |
| 13 | Upstream artifact fingerprints are enough to know when to recreate a group | **Corrected** — editing the group's own target/evaluators/options changed nothing. The group declaration is fingerprinted too |
| 14 | The host is `azure.ai.evals` | **Corrected** — it is `azure.ai.eval`; the spec has been aligned |
| 15 | `run` only needed the composite form | **Corrected** — the spec lists `start`/`list`/`show`/`cancel`, and M1 requires every operation to be reachable atomically. All four now exist |
| 16 | `--project-endpoint` only selects the endpoint | **Corrected** — it also suppressed the azd environment name, silently disabling the cached eval-group and run ids. The name is now resolved independently |
| 17 | No evaluator accepts `model` | **Corrected** — true for built-ins, false for custom rubrics, which *require* `model`. The judge model is bound under whichever name the evaluator declares |
| 18 | An evaluator definition can be compared whole to detect changes | **Corrected** — the service enriches it on create, so only the authored keys can be compared |
| 19 | `GET /evaluators/{name}` returns the latest version | **Corrected** — it 404s; the version has to be resolved first, numerically |
| 20 | Rubric weights are free-form | **Corrected** — integers 1–10; the spec now says so |
| 21 | `--dataset` suppresses data generation | **Corrected** — only did so for a local path, not for a registered dataset name, which the flag also accepts |

---

## 11c. Verified end to end against a live project

| Flow | Result |
|---|---|
| `azd ai eval init` | Scaffolds `evals/azure.yaml` + `evals/eval_generate.yaml` matching the spec |
| `azd provision` → `azd deploy evals` → `azd up` | Provider runs; datasets and groups reconcile |
| Dataset first deploy | Published at version 1.0 |
| Dataset unchanged | Reported unchanged, nothing uploaded |
| Dataset edited | Published 2.0 and the group recreated |
| Group retargeted | New group id; two further no-op deploys reused it |
| `$ref` service entry | Deploys, and the fingerprint matches the equivalent inline config |
| `azd ai eval run` | Real run against a live agent, completed |
| `azd ai eval results show` | 3 passed / 1 failed, per-criterion breakdown, portal link |
| `evaluator builtins` | 10 built-ins with versions and type |
| Eval group create | Accepted for **all 10** built-ins, each with its own contract || `generate` rubric | Succeeds, writes the evaluator JSON |
| Build → pack → publish → install | Installs from the local registry; `azd ai eval --help` lists every command |
| Atomic surface | Every command group and subcommand the spec lists is present |
| `run start` / `list` / `show` / `cancel` | Exercised live, including the guard that refuses to cancel a finished run |
| `results export` | JSON and CSV both written |
| `-o json` | Valid JSON from every read command |
| `dataset` create/show/update/list/delete | Full lifecycle, 1.0 → 2.0, nothing left behind |
| `evaluator` upload/show/update/list/delete | Full lifecycle, version 1 → 2, nothing left behind |
| Deploy with a **custom** evaluator | Publishes once, redeploys are no-ops, an edit publishes the next version |
| Run with built-in **and** custom evaluators | 4 passed / 0 failed, both criteria reported |
| `--no-prompt` | Every required value fails fast naming the flag; nothing blocks |
| **Spec Example 1, verbatim**: `init` → `generate --max-samples 50` → `azd up` → `run` → `results show --failed-only -O` | All five steps from an empty directory. Dataset generated and downloaded, group `eval_78de667a…` deployed in 40s, `evalrun_6b2044cf…` completed, `results.json` written |
| Spec Examples 2, 3, 4 | Verified verbatim |
| `results compare` (M2) | Baseline vs treatment, `PairedTTest`, signed deltas and p-values; `-o json` valid |
| `generate` write-back | Adds the artifact reference and preserves comments, ordering and siblings |
| Generated dataset download | Container-URI case exercised: credential fetched, container listed, JSONL read |
| **Agent-seeded generation, nothing authored** | `init` → `generate` with no instruction file: seeded from the agent's published instructions, 14 rows generated, 13 of 14 on the agent's actual catalog/policies; `azd deploy` published them; the run scored 14 passed / 0 failed / 0 errored |
| Missing generation model | Fails before any network call, naming `--eval-model` and the spec field |
| **`schedule` (M2)** | Create, list, show and delete against the live project; trigger read back from the service as stored, not echoed. One-per-project and existing-name refusals both verified, each naming the schedule and the command to clear it. Delete waits out `Creating` and leaves the project empty |
| **`run --from-traces` (M4)** | Accepted and executed by the service, which stored the payload and normalised `7d` into `lookback_hours: 168` while honouring `max_traces`. The run fails only because this project's agent emits no GenAI traces, and now says exactly that |
| Failed runs | The reason reaches the caller instead of just the word "failed" |
| **`run --response-id` (M4)** | Three stored responses evaluated, 3 passed / 0 errored; the stored payload matched what was sent field for field |
| Sent-vs-stored audit | Every payload compared against what the service kept. Only the trace window was actually being dropped; inline content becoming a `file_id`, and `item_schema` being normalised to `schema.item`, are both benign |
| **`target.type: model` (M4)** | Group deployed with `response` bound to `{{sample.output_text}}`, ran, and scored 2 passed / 1 failed / 0 errored across coherence and fluency |
| **Registered dataset on a run (M4)** | A group with no local `source:` now runs: whole set scores 2 passed / 1 failed, `--max-samples 2` scores 2 rows. Previously a 400 |
| **Two groups in one config** | Distinct ids across repeated deploys, each running its own criteria. Previously the second deploy aliased them onto one group |
| Pinned dataset `version:` | `1.0` publishes 1.0; editing the file while pinned stops with an instruction. Previously published 2.0, then 3.0 |
| Evaluator declaration forms | `source:` alone publishes then reports unchanged; `version:` alone references; both together refused |
| Conversation-level evaluation | `task_completion` publishes `messages` bound to `{{item.messages}}` with `evaluation_level: conversation`, and runs 1 passed / 1 failed / 0 errored with no target |
| `--eval-group` on the id-taking commands | Each group's own runs and results reachable by name; an undeployed name refused by name |
| **All four spec examples, verbatim** | 1: `init` → `generate --max-samples 50` → `azd up` (dataset 10.0, evaluator 22, group created) → `run` completed → `results show --failed-only -O ./results.json` (1503 b). 2: BYO dataset + `builtin.task_adherence`, `run --max-samples 25` completed. 3: `dataset create`, `run start --eval-id --no-prompt -o json` parsed, `results export --format csv -O gate.csv` (108 b). 4: the unregistered-edit error, wording matching the spec |
| Repeated `azd up` | 2nd and 3rd deploys both report `Dataset golden is unchanged at version 3.0`; no new versions |
| Hand-set `EVAL_GROUP_ID` | Honoured on a single-group config — the group is reused, not recreated. The per-group fix had silently removed this documented path |
| `-o json` on list commands | `dataset`, `evaluator`, `schedule`, `run list` all emit a bare array. They previously leaked two different service envelopes, `value` and `data` |
| `--eval-id` on the sibling commands | Accepted by `run list\|show\|cancel` and `results show\|export\|compare`, matching `run start`; positional still wins |
| `results compare` on one-sample runs | Service sends `"standardDeviation": "NaN"` — a quoted string, since JSON has no NaN literal. Decoding into `float64` failed the whole comparison, discarding the `TooFewSamples` verdict that explains it. Now decodes, renders the undefined statistic as `-`, and emits `null` in JSON. The earlier pass only held because those runs had enough samples |
| **Scenario suite, 18 assertions on substance** | Every scenario checked on result counts and file contents rather than exit status: agent run (1 passed / 0 errored), `results show -O` parseable, CSV header + rows, JSON export valid, model target (2 scored), `--max-samples 1` scoring exactly 1 of 2 rows, conversation level (1 scored), `--response-id`, `--from-traces`, `results compare` table + JSON, schedule set/show/delete. 18/18 |
| `TestLiveRun` | Was **skipping** unless `AZURE_AI_EVAL_AGENT` is set, so the run phase had never executed in any "full suite green" claim. Now run against a real agent, and it asserts no errored samples and at least one scored — reaching a terminal state alone would stay green with a broken target |
| Schedule inherits the group's last run | A schedule repeats the most recent run, so `--from-traces` turns the next schedule into a trace evaluation, which the service restricts to hourly. Proved by experiment: daily accepted after an agent run, refused after a traces run on the same group, hourly accepted for that traces run. The bare service message named neither the cause nor the remedy |

## 11d. Blocked — needs the service team

**Agent-seeded data generation fails for every agent.** `POST /data_generation_jobs` with an `agent` source in `inputs.sources` is accepted (201) and then fails within seconds:

```
"error": { "code": "DataGenerationJobSystemError",
           "message": "Something went wrong during data generation. Please try again." }
```

**Ruled out, by probe.** The payload matches the published contract (`AgentDataGenerationJobSource` in `RAISvc/Contracts/DataGenerationJobs/Models/DataGenerationJobSource.cs`: `agent_name` + optional `agent_version`, which is exactly what is sent). Every identifier form fails the same way — name, `agent_version` pinned to `1`/`2`/`latest`, an assistant id, an assistant name, agent with and without a prompt source, and all three api-versions. **A nonexistent agent name fails identically**, so the agent is never resolved and the error carries no signal.

**Where it goes.** `{project}/data_generation_jobs` → RAISvc S2S client (`DependencyExtensions.cs`, targeting `FineTuningHostUri`) → FineTuning `foundryProxy/data_generation_jobs` → `FoundryProxyTransform.cs` rewrites the path to `{aoaiEndpointTarget}/openai/v1/data_generation_jobs`. Neither RAISvc nor FineTuning resolves the agent — FineTuning has no reference to `agent_name` anywhere. The failure is in the AOAI generator, outside this repo.

**What the CLI does instead.** The contract says the agent source exists to "fetch instructions / metadata from" the agent, which is a read the client can do itself. `generate` resolves the agent's instructions locally (§6a) and passes them as the prompt source. The agent source is still sent, so it starts contributing when the service is fixed, and the retry covers the failure until then.

**Related, worth reporting:** an invalid enum value anywhere in the request returns `"The dataGenerationJob field is required."` — a whole-body deserialization failure reported as a missing field. Same misleading shape as the `definition.type` case on evaluator upload.

**Also reported by the service, worth filing:** `results compare` returns `"standardDeviation": "NaN"` as a **quoted string** whenever a run has a single sample. JSON has no NaN literal, so this is the service's workaround, but it means a typed client must special-case the field or lose the whole comparison. The extension now decodes it (§11c); the service would be better emitting `null`.

---

## 11e. Probed and genuinely unavailable

Recorded because four M4 items were filed as "awaiting service support" and every one of them turned out to be already shipped. These three were checked rather than assumed, and they hold.

| Claim | How it was checked | Result |
|---|---|---|
| M3: the eval group is versioned | `Evaluation.cs` in `RAISvc/Contracts/UnifiedEvaluationV2` | No `version` property. Blocked |
| M3: the eval group binds a dataset | `DataSourceConfig.cs` derived types | `custom`, `logs`, `stored_completions`, and the `azure_ai_source` scenarios (`red_team`, `synthetic_data_gen`, `responses`, `traces`, `benchmark_preview`, `conversation_simulation_preview`). None binds a registered dataset. Blocked |
| M4: a prompt target exists | `Target.cs` `TargetType` enum | Values are `azure_ai_model`, `azure_ai_agent`, `azure_ai_assistant`, plus `azure_ai_traces` marked `[NotARequestDiscriminator]`. No prompt target. Blocked |
| An assistant target could be exposed | Live POST of a known-good run body with only `target` swapped to `{"type":"azure_ai_assistant","id":"asst_…"}` | **400** `Unsupported target type in TargetCompletionsEvalRunDataSource: AzureAIAssistant is invalid`. On the enum, refused by the run data source. Building the CLI surface would have shipped a dead path |

Unexposed capability seen while checking, out of the spec's scope and not implemented: `red_team`, `synthetic_data_gen`, `benchmark_preview` and `conversation_simulation_preview` data source configs, and an `EvalCsvRunDataSource`.

---

## 12. Source-of-truth index

| Doc / path | Gives you |
|---|---|
| `../azure.ai.agents/internal/pkg/agents/{eval_api,dataset_api}/` | The clients to lift. **Start here.** |
| `../azure.ai.agents/internal/cmd/eval_*.go` | Current command implementations, progress UX, api-version wiring |
| `../azure.ai.agents/internal/cmd/listen.go` | Service-target + lifecycle wiring to copy |
| `../azure.ai.agents/extension.yaml`, `main.go`, `go.mod` | Manifest, entrypoint, dependency versions |
| `cli/azd/pkg/azdext/` | Extension SDK: `ServiceTargetProvider`, `EventManager`, `Environment()` |
| `cli/azd/pkg/project/service_config.go` | `AdditionalProperties` inline capture |
| `cli/azd/pkg/foundry/includes.go` | `ResolveFileRefs` |
| `cli/azd/internal/cmd/up_graph.go` | What `azd up` actually runs |
| `foundrysdk_specs/.../azd_eval_extension/spec.md` | Design spec (authoritative) |
| `foundrysdk_specs/.../azd-agent-eval-public-preview-findings.md` | Measured bugs and timings in §7 |
| `foundrysdk_specs/.../custom_evaluator_upload/spec.md` | Evaluator upload flow, packaging, RBAC |

---

*Keep in sync with `spec.md`. Uncommitted working document.*
