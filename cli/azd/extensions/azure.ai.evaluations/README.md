# Azure Developer CLI (azd) Evaluations Extension

Define Foundry evaluations alongside your agent in `azure.yaml`, deploy them
with `azd up`, and run them from the terminal.

```bash
azd ai eval init          # scaffold evals/ next to your agent
azd ai eval generate      # synthesize a rubric and dataset from the agent
azd up                    # register datasets and evaluators, create the eval group
azd ai eval run start     # run the evaluation and summarize the results
```

Generation prints an interactive `init` next step, followed by guidance for
unattended use. When using `--no-prompt`, supply an independently selected
`--judge-model <deployment>`; conversation simulation also needs
`--simulation-model <deployment>`. The printed command never assumes that the
generation model should fill either role.

## What gets deployed

Eval resources are one service entry in `azure.yaml`, normally a `$ref` to a
file under `evals/`:

```yaml
# azure.yaml
services:
  ai-project:
    host: azure.ai.project
  evals:
    host: azure.ai.eval
    uses: [ai-project]
    $ref: ./evals/azure.eval.yaml
```

```yaml
# evals/azure.eval.yaml
datasets:
  - name: support-golden
    file: ./datasets/support-golden.jsonl

evaluators:
  - name: support-quality
    source: ./evaluators/support-quality.json

evals:
  - name: support-quality
    dataset: support-golden
    evaluation_level: turn
    evaluators:
      - evaluator: builtin.task_adherence
        initialization_parameters:
          model: gpt-4.1-nano
      - evaluator: support-quality
    target:
      type: agent
      name: support-agent
```

`azd up` reconciles **datasets → evaluators → eval groups**, in that order,
because a group references the versions the first two resolve to.

Relative paths inside the `$ref`'d configuration resolve against **that file's**
directory, so `./datasets/x.jsonl` above means `evals/datasets/x.jsonl`.

The same holds for a `$ref` on a single catalog entry: `file:` and `source:` are
registered as this extension's path keys, so a relative `source:` written inside
`evals/evaluators/quality.yaml` means `evals/evaluators/quality.json` — beside
the file it was written in, wherever that file is pulled in from.

A rubric kept in its own file is named by `source:`, which is what `generate`
writes:

```yaml
evaluators:
  - name: quality
    source: ./evaluators/quality.json
```

`definition:` takes the rubric itself rather than a path, and a `$ref` there
fills that field with the file's contents:

```yaml
evaluators:
  - name: quality
    definition:
      $ref: ./evaluators/quality.json    # the rubric itself, not a pointer to one
```

An entry declared this way is read and deployed normally, but the rubric lives
in the referenced file, so `azd ai eval generate` will not update it in place and
says so rather than writing a second declaration of the same rubric beside the
directive. Edit the referenced file, or generate under a different name.

### Simulating multi-turn conversations

Choose how conversation datasets are used during `init`:

```bash
# Score completed message transcripts, without invoking an agent.
azd ai eval init --conversation-mode static --dataset completed-transcripts --judge-model judge-deployment

# Create conversations from scenario seeds, then grade the resulting messages.
azd ai eval init --conversation-mode simulation --target support-agent --dataset retail-seeds --simulation-model simulator-deployment --judge-model judge-deployment --num-conversations 1 --max-turns 5 --no-prompt
```

`--conversation-mode` implies `--source dataset` and
`--evaluation-level conversation` when they are omitted. Without this flag,
interactive init offers **Static** or **Simulation** for a conversation dataset;
`--no-prompt` and `--output json` default to static. Static mode writes neither
`target:` nor `simulation:` and rejects `--target`, because completed transcripts
are scored as they stand. Trace-backed conversations continue to use
`--source traces --evaluation-level conversation` and filter by the selected agent.

| Init flag | Applies to | Meaning |
|---|---|---|
| `--conversation-mode static\|simulation` | Conversation datasets | Completed messages or scenario-seed simulation. |
| `--simulation-model` | Simulation only | Deployment that plays the simulated user; required, or prompted interactively. |
| `--num-conversations` | Simulation only | Conversations per seed, 1 to 5; default 1. |
| `--max-turns` | Simulation only | Maximum turns, 1 to 20; omission preserves the service default. |

Explicit zero is invalid for both numeric flags. Simulation flags with static,
turn, or trace evaluation are rejected rather than ignored. Simulation needs an
agent target, seed dataset, simulation model, and judge model. Non-interactive
init reports all unresolved required inputs together, naming the flags to supply.
Init is add-only, preserves existing YAML and unknown fields, and makes no new
live lookups beyond the bounded built-in evaluator catalogue check.
For simulation, init checks every locally available seed row before writing
configuration, including files in declared datasets and local nested `$ref`
entries. Each row needs a text `test_case_description` containing more than
whitespace, cannot carry `messages`, `query`, or `response` fields (even empty
or null), and may specify a positive whole `desired_num_turns` no greater than
an explicit `--max-turns`. Omitted turn counts remain valid.
Interactive init reports invalid rows and asks for a corrected or different
dataset before confirmation; press Ctrl+C at that prompt to cancel without
authored changes. Under `--no-prompt` or `--output json`, invalid local rows
fail immediately without writing configuration.
Local files derive their dataset name from the filename without its extension.
If that name is already declared for a different file (or has no local file),
init refuses the collision rather than replacing the declaration or ignoring
the supplied path. Interactive init asks for another dataset; use a unique
filename to add the new file, or the existing dataset's name or file path to
reuse it. Equivalent paths to the same file are accepted, preserving references,
version pins, and other authored metadata.
When `--path` names a configuration file, dataset lookup uses that exact file,
while artifact paths remain relative to its directory.
Registered datasets with no local file are not fetched or checked by init.
The evaluator picker excludes custom evaluators whose local
`supported_evaluation_levels` explicitly excludes the selected level; an explicit
incompatible `--evaluator` is rejected. Missing or unfamiliar metadata remains
unknown, with authoritative compatibility checked when the eval is created.

The example above grades rows that already hold an exchange. A `simulation:`
block instead has the service hold the conversation first — a simulator model
plays the user against your deployed agent — and grades the transcript it
produces:

```yaml
evals:
  - name: retail-conversations
    dataset: retail-seeds
    evaluation_level: conversation
    simulation:
      model: gpt-4o-mini        # plays the user, not the judge or generation model
      num_conversations: 3      # per seed row, 1–5
      max_turns: 8              # 1–20; omit to leave it to the service
    evaluators:
      - evaluator: builtin.task_completion
        initialization_parameters:
          model: gpt-4.1-nano
    target:
      type: agent
      name: support-agent
```

`simulation:` requires `evaluation_level: conversation` and an agent target:
there is no turn to score before the conversation exists, and nothing to hold it
with if the target is a model. It is also exclusive with `source:` and
`max_samples:` — the run creates its conversations rather than collecting or
sampling ones that already happened. Every evaluator listed has to support
conversation level; one that does not is refused at deploy rather than bound to
a column the graded rows do not have.

The dataset holds **seeds**, not exchanges. One row describes one conversation
to have:

```jsonl
{"test_case_description": "A customer asks why a delivered order never arrived.", "desired_num_turns": 4}
{"test_case_description": "A customer disputes a charge and wants it reversed."}
```

Only `test_case_description` is required; it must contain non-whitespace text
describing the scenario the simulator opens with. `desired_num_turns` is optional
and must be a positive whole number when supplied. It is a request, not an
override: asking for more turns than `max_turns` allows is refused rather than
quietly truncated. Local seed files are checked row by row before `create` or
`azd up` publishes dependencies; the run checks registered seed rows as well.
Completed `messages` and turn-level `query`/`response` fields cannot be mixed
with simulation seeds, even when those fields are empty or null. Keep these
dataset modes in separate evaluations. Omitting `max_turns` leaves the bound to
the service; the CLI does not impose a per-row ceiling in its place.

Seed rows carry no `query` or `response`, because nobody has asked anything yet.
That is why the evaluators bind `messages` — the transcript the run produces —
and why a run over seed rows whose target reads a column the seeds do not have
is refused instead of scored against the seeded text.

`azd ai eval generate --evaluation-level conversation` writes seeds in this
shape and tags the registered dataset so a later run knows what it holds.
Its printed init command selects `--conversation-mode simulation`. Run that
command interactively to enter the simulation model, or add
`--simulation-model <deployment> --judge-model <deployment> --no-prompt` for
automation (also supply `--target` if generation had no agent).
The generation, simulation, and judge deployments are independent choices.
Init never copies the generation or judge model into the simulation model.
Generation declares artifacts only; it does not attach them to an existing eval
or replace its configuration. If a generated rubric declares an incompatible
evaluation level, the handoff warns and uses the built-in default instead; the
rubric remains in the catalogue.

Simulation run summaries, `run show`, and `run output list` retain the run's
dataset name and version and distinguish **requested configuration** from
**observed results**:

- Seed scenarios count the validated dataset rows submitted to the run.
- Repetitions are the requested conversations per seed, not completed conversations.
- Maximum turns is a requested ceiling. An omitted ceiling leaves the service
  default and is not an observed conversation length.
- Conversation evaluation results use the service's `result_counts`, keeping
  failed verdicts separate from errored and skipped evaluations.

The CLI has no verified service counters for generated conversations, completed
conversations, or actual turns. These are shown as **not
reported**, never calculated by multiplying seeds and repetitions or treating
evaluation totals as successful generation. Older runs without recorded
settings also show **not reported** for those settings. Static conversation
and turn-level runs keep their existing output.

When a waited `run start` successfully reads all output rows for its mean-score
summary, it also shows **observed conversation output**. This block counts
unique `datasource_item.id` values and the associated output-item lifecycle
statuses, not generated or completed conversations. A completed output item can
still have failed evaluation verdicts. Duplicate conversation IDs count once;
conflicting or unknown statuses and rows without conversation IDs are reported
separately. These observations describe all rows returned by that listing, not
a guarantee that every requested conversation produced output. Paged or filtered
listings, and detail views that have not fetched all rows, do not supply this
block. No additional output fetch or transcript-based turn inference is used.

JSON retains the service's run fields, including unrecognized nested fields;
it does not add estimated conversation or turn counts. Newly submitted
simulation runs record configuration under `metadata.azd_simulation_*`, with
`metadata.azd_run_mode` identifying the simulation mode. The JSON handoff from
`run start --no-wait` is unchanged; read `run show -o json` for the run object.

### Repeated deploys do not create redundant versions

Before publishing dependencies, `azd ai eval create <name>` validates the selected
eval, its local JSONL/rubric files, and its registered dataset/evaluator references,
including version pins. An unavailable reference lookup is an error, not a reason
to publish optimistically. Unrelated invalid evals do not block this targeted
command; `azd up` validates the entire evaluation service before publishing any
of its dependencies. Validation does not write private reconciliation state.
Local rows used to invoke an agent or model must carry the `query` field the
target reads. Static dataset-only evaluations do not impose this target
requirement. Rubric dimension weights, when supplied, must be whole numbers
from 1 to 10; `pass_threshold`, when supplied, must be a number from 0 to 1.
These authored parameters are validated before any dependency is published.

This is not a transaction across Foundry resources. If a later service operation
fails, successfully published shared versions are retained, not deleted. Fix the
reported error and repeat the same command to reuse unchanged artifacts.
For a partial `create -o json` failure, the single output document includes
`status: "failed"`, the resolved `artifacts` with their versions and `published`
flags, the error, and a `recovery_command`. The command still exits nonzero.

Datasets are fingerprinted locally, because the dataset API exposes no content
hash and comparing against the service would mean downloading the blob on every
deploy. Evaluator definitions are compared against the service, but only on the
keys you authored — the service adds `data_schema`, `init_parameters` and
`metrics` of its own.

An evaluator reference inherits an explicit `version` from its catalog entry
unless the reference sets its own version. Changing or removing that inherited
pin changes the immutable eval criteria and creates a new eval. An unchanged
effective pin keeps the same eval, including when the pin moves between the
catalog and reference. With neither pin set, the evaluator continues tracking
the service's latest version without recreating the eval on each new version.
Renaming before older pin fingerprints have been migrated can reuse the prior
eval only when its stored criteria confirm the same effective pins and no other
declared eval owns it.

Eval groups are immutable, so a change to a group's evaluators, target or
  sampling creates a new group and a new id. The id is cached in the extension's
  own private state (`eval.state`) so repeat runs stay comparable. That is not
  an azd environment value: it does not appear in `azd env get-values`, which
  shows only what you put there.

Stored-response evaluations (`source.type: responses`) use Foundry's
`azure_ai_source` schema with `scenario: responses`. A deployment replaces an
older custom-schema response eval with a compatible eval once, even when the
declaration is unchanged. The old eval and its runs are retained; subsequent
unchanged deployments reuse the new ID. Other evaluation modes keep their
custom schemas and are not migrated. Switching a declaration from stored
responses to another source also creates an eval with the required custom schema.

An explicit `id:` or a rerun by eval ID cannot change an immutable eval's
schema. An incompatible response eval fails before starting a run. Remove the
explicit `id:`, deploy the response-source declaration, then run it by name.
Legacy rerun sources with bare response-ID rows are also rejected; running the
declaration by name builds the required `item` envelopes without invoking an
agent or changing the selected response IDs.

### Recovering partial generation

Dataset and evaluator generation are independent. If one fails, a successful
artifact remains registered, downloaded, and declared in the catalog. A failed
catalog update is reported separately from a failed generation or download;
it does not discard the downloaded artifact.

Use the printed `azd ai eval job show <job-id> --dataset` or `--evaluator`
command to inspect or collect the existing job without starting another one.
The recovery command preserves the configuration path, output directory,
project endpoint, and environment. If submission returned no job ID, inspect
the printed `job list` command first: a lost response does not prove that the
service never accepted the job. If a new generation is needed, repeat the
original command with **only the failed artifact selector**, keeping that
artifact's original input flags. Do not regenerate the successful artifact.

With `-o json`, generation emits one document keyed by `dataset` and `evaluator`,
including each outcome's `status`, `job_id`, and, on failure, `error`,
`recovery_command`, and `retry_guidance`. Status is `submitted`, `succeeded`,
`failed`, or `catalog_failed`. Any failed outcome makes the command exit nonzero.

Generation changes catalog declarations, not an existing eval's references.
When an existing eval does not reference a generated artifact, the command
explains that it remains declaration-only. Use `init` to create a new eval or
deliberately edit a compatible eval's references. A trace-backed eval cannot
also consume a dataset; keep it unchanged and create a separate dataset-backed
eval instead.

## Commands

### Dataset identity and row caps

Runs over registered datasets send the service-issued version ID, not inline
copies of the rows. This applies to static scoring, agent and model targets,
and datasets whose declaration still has `file:` after publication. A declared
`version:` wins over the version recorded by deployment; otherwise the recorded
version is used, or the latest service version when none is recorded. Lookup,
authorization, and missing-ID errors stop the run rather than switching to inline
data. Registered rows are downloaded only to validate their shape before submission.

The current run API exposes no supported row-subset option on a registered
`file_id` source. A positive `--max-samples` or `max_samples:` therefore fails
explicitly for registered datasets. Remove the cap, pass `--max-samples 0` to
override a configured cap, or deliberately publish and select a smaller dataset.
The CLI does not publish temporary subset datasets automatically.

Inline rows and row caps remain available for genuinely unregistered local files,
after the service confirms the dataset is absent. A complete, valid empty version
listing (or a not-found response) is checked with first-version lookups. Only
not-found responses to those lookups permit inline rows; malformed listings,
incomplete pagination, and authorization or service failures stop the run.
`--max-samples` is also rejected for source-backed runs
and reruns selected by eval ID, where it cannot change the repeated source.
Source-backed runs reject configured `max_samples:` too; use `source.max_traces`
for trace limits or select `source.response_ids` explicitly.

Reruns retain a previous registered `file_id` unchanged. A legacy run with inline
rows attributed to a registered version must instead be started from its declared
eval by name: replacing those possibly capped rows with a whole version would
silently change what gets scored.

| Group | Commands |
|---|---|
| `azd ai eval` | `init` · `generate` · `create [name]` · `list` · `show <eval>` · `delete <eval>` |
| `azd ai eval dataset` | `create` · `update` · `list` · `show` · `download` · `delete` · `versions list` |
| `azd ai eval evaluator` | `create` · `update` · `list` · `show` · `download` · `delete` · `versions list` |
| `azd ai eval run` | `start` · `list` · `show` · `cancel` · `delete` · `output list` · `output show` · `output export` |
| `azd ai eval job` | `list` · `show` · `cancel` · `delete` |

`create` and `update` both publish a new immutable version; the server
auto-increments and nothing mutates in place.

`delete` asks before removing anything and takes `--force` to skip the
question, which is what a pipeline passes. `job delete` is the exception: it
discards a record of finished work, not the artifact the job produced.

Every command supports `-o json` and `--no-prompt`, so the whole surface is
usable from CI.

`azd ai eval run output list --failed-only` displays a page of failing test
cases, not the full run's failure count. Its footer separates the number shown
on that page from the service-reported failures and total test cases for the
whole run. For example, a page of 10 failures can belong to a run with 12
failures among 18 test cases. Follow the printed page token to read the rest,
or use `run output export` to save the complete results. Errored rows remain
separate from failed verdicts and can be selected with `--status errored`.

After a terminal run, waited `run start` summaries and `run show` details
include an unfiltered output-list command and a JSON export command, both with
the resolved eval and run identities. Suggested commands use the immutable eval
ID when known, rather than a friendly name that might point to a different
eval after a later deployment. Friendly labels remain in the human run header;
service JSON is not rewritten. A failed-only listing is additional
guidance when the service reports failed verdicts, not a replacement for the
unfiltered listing. Errored rows get a separate `--status errored` command;
they are not included by `--failed-only`.

An operationally failed run can have no result counts or output rows. Its
follow-up commands inspect **available** output and export the run's diagnostics
plus any available results; they do not imply that grading succeeded or that
failing rows exist. `run show` also prints the service's run-level failure
message when one was returned, removing URL credentials, query strings, and
fragments from the human message. `--output json` keeps its existing run document
and exit behavior without appending human guidance.

`run output show <item>` uses the lookup ID from the listing in its human
header. The service may return a result-version URI as the detail object's
`id`; JSON keeps that returned identity rather than replacing it with the
lookup ID.

For rubric results, the detail view displays returned
`properties.dimension_scores` alongside the overall evaluator score. Each
dimension can include its score, applicability, weight, and full reason.
Applicability is not a pass/fail verdict, and missing values are not treated
as zero or false. Separate dimension metrics in `results` remain supported.
The CLI does not derive dimension results from the rubric definition when
they are absent from the response.

Output-item JSON preserves unrecognized nested service fields, including
evaluator `properties` and `sample` details; modeled scores keep their existing
numeric normalization. These fields can contain prompts, answers, and other
sensitive evaluation content. Prefer a private destination with
`run output list --output-file` or `run output export --output-file` over
writing JSON into shared terminal or CI logs.

A command that needs an eval and was not told which one offers a picker.
Closing that picker is an answer, not a failure: the command says the selection
was cancelled and exits 0, at every command that offers it. Under `-o json`
nothing is written, so stdout still parses.

`azd ai eval create` closes with a link to the eval in the Portal, for a
newly created eval and for one that already existed unchanged.

### Downloading a dataset

`azd ai eval dataset download <name> --version <version> --output-file <path>`
supports single-file datasets even when their download credentials grant access
to the parent container. Container-backed downloads require a complete listing
with exactly one file and dataset metadata reporting `isSingleFile: true`.
Folders (including one-file folders) and multi-file datasets require
`--output-dir` and retain their relative layout.

Single-file container downloads without `--output-file` land as
`<name>-<version><extension>` under `--output-dir` (the current directory by
default), while folders land under `<name>-<version>/`. Omitting `--version`
selects the latest version. Existing destinations require `--force` to replace,
including with `--no-prompt`. JSON output reports the resolved version, path,
file count, and single-file status.

## Evaluators

Built-ins need no declaration — reference them as `builtin.<name>` and list
them with `azd ai eval evaluator list --builtin`.

`init` offers a few common built-ins in its picker; that is a shortlist, not
the catalogue. Any built-in the project publishes works with
`--evaluator builtin.<name>`, including ones the picker never shows.

`init` checks that reference against the project's catalogue when it can reach
one, so a name that does not exist is refused there rather than at `create`.
When no project is reachable — offline, unauthenticated, or outside an azd
environment — the reference is left as written and `init` behaves as it always
has. The check never turns a working offline `init` into a failure.

Evaluators do not share an input contract, so the CLI reads each one's
published contract and shapes the request to match. An evaluator needing an
input your dataset does not carry is reported before the request is sent, with
the column named, rather than as a service-side rejection.

A custom rubric is a JSON list of weighted dimensions:

```json
{
  "dimensions": [
    { "id": "accuracy", "description": "The answer is factually correct.", "weight": 5 },
    { "id": "tone", "description": "The answer is polite and professional.", "weight": 2 }
  ]
}
```

`weight` is an **integer from 1 to 10**. Weights do not need to sum to
anything.

## Choosing a project

The project endpoint is resolved in this order:

1. `--project-endpoint`
2. `FOUNDRY_PROJECT_ENDPOINT` in the active azd environment, then
   `AZURE_AI_PROJECT_ENDPOINT` there
3. `extensions.ai-projects.context.endpoint` in azd's global config, which
   `azd ai project` writes and this extension only reads. A config that has not
   been migrated yet falls back to `extensions.ai-agents.project.context.endpoint`,
   the key `azure.ai.agents` used before `azd ai project show` moved it.
4. `FOUNDRY_PROJECT_ENDPOINT` in the host environment, then
   `AZURE_AI_PROJECT_ENDPOINT`

Level 3 is worth knowing about: it is machine-wide rather than per-project, so
a project selected with `azd ai project` somewhere else takes precedence over
the variable exported in this shell. `--debug` prints which level answered.

## Other environment variables

| Variable | Description |
| --- | --- |
| `AZURE_AI_PROJECT_ID` | The Microsoft Foundry project resource ID, used to build portal links for an eval and its runs. |
| `AZURE_AI_MODEL_DEPLOYMENT_NAME` | The model deployment `azd ai eval init` offers as the judge when one is not named on the command line. |
| `APPLICATIONINSIGHTS_CONNECTION_STRING` | A detection signal, not a credential this extension consumes: `azd ai eval init` and `generate` check only whether it is set, and default to a trace-backed source when it is. The value is never read or transmitted by the extension — Foundry reads the traces server-side. |

## Local development

### Prerequisites

- Go (the version in `go.mod`; `GOTOOLCHAIN=auto` fetches it)
- [azd](https://aka.ms/azd) and the extension developer kit:
  `azd ext install microsoft.azd.extensions`

### Build, test, install

```bash
azd x build          # compile and install into the local azd
azd x pack           # package the artifacts
azd x publish        # register in the local extension source
azd ext install azure.ai.evaluations --source local
```

```bash
go test ./internal/...   # unit tests
```

### Live integration tests

These talk to a real Foundry project, so they are excluded from the default
build by the `live` tag and additionally gated on an environment variable:

```bash
export AZURE_AI_EVAL_E2E_LIVE=1
export FOUNDRY_PROJECT_ENDPOINT=https://<account>.services.ai.azure.com/api/projects/<project>
export AZURE_AI_EVAL_MODEL=gpt-4.1-nano       # optional judge model
export AZURE_AI_EVAL_AGENT=<agent-name>       # optional, enables the run phase

go test -tags live ./internal/cmd/ ./tests/live/ ./tests/cli/
```

`./tests/cli/` drives the built binary rather than the packages, so it is the
half that catches a command wired up wrongly. Omitting it is how a live suite
that could never have compiled sat green in review.

The hero walkthrough is behind its own tag, because it scaffolds a project
end to end:

```bash
go test -tags hero ./tests/hero/
```

They clean up every resource they create.

### Debug logging

Request tracing is off by default. `--debug`, or `AZD_EXT_DEBUG=true`, writes it
to a dated log file in the temporary directory rather than the terminal, and
prints that path on stderr.

## Telemetry

When installed from the official registry, the extension reports the
`init.completed` usage event once `azd ai eval init` has written a scaffold and
wired the eval service into `azure.yaml`. Its `ext.source` attribute is exactly
one of:

- `traces` when the eval will grade rows read from project telemetry;
- `dataset` when it will grade rows from a declared dataset; or
- `unknown` for a source this extension does not recognize.

The event is reported after the scaffold is committed, not while the prompts
run, because a confirmation can send the reader back through the questions and
only the last pass describes what was written.

Event names, attribute keys, and their finite value sets live in
`internal/telemetry/events.go`. Do not call `ReportUsage` directly from command
code, and never include eval, dataset, evaluator, or agent names, file paths,
project endpoints, model deployments, or anything else a user typed. Failures
and their structured error codes are already reported separately by the
extension SDK; this event records usage only.

## TODO before release

The first two are files the azd extensions team owns, so they are not changed
here. The last two are not files at all — YAML alone does not provision a
pipeline, and no evaluations release check runs on this PR because of it.

- [ ] **`cli/azd/extensions/registry.json`** — add the `azure.ai.evaluations`
  entry. Until it exists `azd extension install azure.ai.evaluations` cannot
  resolve, so the extension is only reachable through `azd x pack` +
  `azd x publish` into the local source registry.
- [ ] **`microsoft.foundry/extension.yaml`** — add the dependency, but only
  after the registry entry lands. Declaring a dependency that cannot resolve
  breaks installing the bundle.
- [ ] **Register the release YAML as an Azure DevOps pipeline** under
  `azure-dev/extensions`, with access to the shared release infrastructure.
  Checking the file in does not create the pipeline, so nothing runs it.
- [ ] **Create the `ext-azure.ai.evaluations` issue label**, which is how
  issues are routed to this extension.
