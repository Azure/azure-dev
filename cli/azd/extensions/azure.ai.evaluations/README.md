# Azure Developer CLI (azd) Evaluations Extension

Define Foundry evaluations alongside your agent in `azure.yaml`, deploy them
with `azd up`, and run them from the terminal.

```bash
azd ai eval init          # scaffold evals/ next to your agent
azd ai eval generate      # synthesize a rubric and dataset from the agent
azd up                    # register datasets and evaluators, create the eval group
azd ai eval run start     # run the evaluation and summarize the results
```

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

### Registered dataset identity

Runs bind registered datasets using the service-issued version ID, including
datasets whose catalog entry still has a local `file:` after publication.
An explicit `version:` takes precedence over the recorded publication version;
without either, the latest registered version is resolved from the service.
Run metadata records that same resolved version.

Registered versions cannot be sampled by this run API. A positive `max_samples:`
or `--max-samples` is refused rather than ignored or sent as anonymous inline
rows. Remove the cap, or publish and select a smaller dataset.

Genuinely unregistered local files still run inline and support a cap, but only
after a complete empty version listing (or a not-found response) and not-found
first-version probes confirm absence. Permissions, transient failures, and
malformed listings fail the run instead of silently selecting local data.

`job show --dataset` recovers the registered evaluation level even when the local
artifact already exists. It preserves edited bytes unless `--force` is given,
does not download content when preserving the file, and does not record a new
deployed fingerprint for those unverified local bytes. Job inputs and recorded
generation state keep precedence over the registered tag. A metadata lookup
failure is reported as a collection error; an untagged version stays unspecified.
Within registered metadata, an explicit `evaluation_level` wins over a recognized
`data_generation_type`, followed by the portal's `scenario: conversation_simulation`.
This recovers older service/portal seed datasets without guessing from unknown tags.
Echoed generation inputs remain internal to level recovery and are omitted from
job JSON output, including source prompts and instructions.

### Simulating multi-turn conversations

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
      model: model-connection/gpt-4.1-nano # plays the user, not the agent under test
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
with if the target is a model. It is also exclusive with `source:` and positive
`max_samples:` caps; `max_samples: 0` means uncapped. The run creates its
conversations rather than collecting or sampling ones that already happened.
Every evaluator listed has to support
conversation level; one that does not is refused at deploy rather than bound to
a column the graded rows do not have.

Omit `num_conversations` to use one conversation per seed, and omit `max_turns`
to use the service default. Explicit zero or null values for either of these
simulation counts are rejected by both file-based and inline service configuration
loaders.

The authored `simulation:` block accepts 1 to 5 conversations per seed and
1 to 20 turns when those defaults are explicitly set. These are azd's current
authoring limits from the CLI feature specification, not maxima imposed by the
Foundry preview service. They remain unchanged here; per-case settings follow
the override rules below.

`simulation.model` must name an existing connection and deployment as
`connection-name/model-deployment`. Bare deployment names are rejected before a
run is submitted; the CLI does not guess a connection or reuse the judge model.
This follows the published Foundry preview contract. Earlier live checks that
accepted bare deployment names used the older service behavior; they do not
establish live compatibility for this qualified-reference validation. The current
request shape is covered by local contract fixtures, not a new live run.

The dataset holds **seeds**, not exchanges. One row describes one conversation
to have:

```jsonl
{"test_case_description": "A customer asks why a delivered order never arrived.", "simulation_configuration": {"desired_num_turns": 4}}
{"test_case_description": "A customer disputes a charge and wants it reversed."}
```

Only `test_case_description` is required; it is the scenario the simulator opens
with and must contain 1 to 2,500 Unicode characters. Per-row turn settings belong
inside `simulation_configuration`, matching
the [published Foundry contract](https://github.com/Azure/azure-rest-api-specs/blob/main/specification/ai-foundry/data-plane/Foundry/src/openai/evaluations/user_conversation_simulation.tsp).
The optional `desired_num_turns` must not exceed the effective `max_num_turns`:
the per-row maximum overrides `simulation.max_turns`, and the service default is
20 when neither is set. Generation can return a flat top-level `desired_num_turns`.
When collecting generated conversation seeds, the CLI moves that value into
`simulation_configuration` in the downloaded local file. Canonical rows remain
byte-identical, and unrelated fields are preserved without rounding numeric IDs.
The returned artifact version identifies the original generation job's output,
not the normalized local bytes. The CLI clears stale local publication state
instead of recording a deployed fingerprint for those transformed bytes:
`azd ai eval create` or `azd up` publishes the file explicitly before a simulation run
binds the resulting service-issued version ID. Collection never silently publishes
a replacement version, and an existing edited file is still preserved unless
`--force` is supplied.

An independently registered dataset still carrying a flat turn field is rejected
at run time because the simulator would ignore it. Move the field into
`simulation_configuration` and explicitly publish a new version before running.

Runs send `data_mapping` for `test_case_description` and
`simulation_configuration` as column names, not `{{item...}}` templates. Registered
seed content stays bound by version ID rather than being rewritten inline.
The simulated eval's graded `messages` column is a required array of message
objects, not a string. Ordinary static dataset schemas keep their existing
optional-column behavior.

Seed rows carry no `query` or `response`, because nobody has asked anything yet.
That is why the evaluators bind `messages` — the transcript the run produces —
and why a run over seed rows whose target reads a column the seeds do not have
is refused instead of scored against the seeded text.

`azd ai eval generate --evaluation-level conversation` writes seeds in this
shape and tags the registered dataset so a later run knows what it holds.

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
missing or null result-count members remain missing or null. It does not add
estimated conversation or turn counts. Newly submitted
simulation runs record configuration under `metadata.azd_simulation_*`, with
`metadata.azd_run_mode` identifying the simulation mode. The JSON handoff from
`run start --no-wait` is unchanged; read `run show -o json` for the run object.

### Repeated deploys do not create redundant versions

Datasets are fingerprinted locally, because the dataset API exposes no content
hash and comparing against the service would mean downloading the blob on every
deploy. Evaluator definitions are compared against the service, but only on the
keys you authored — the service adds `data_schema`, `init_parameters` and
`metrics` of its own.

Eval groups are immutable, so a change to a group's evaluators, target or
  sampling creates a new group and a new id. The id is cached in the extension's
  own private state (`eval.state`) so repeat runs stay comparable. That is not
  an azd environment value: it does not appear in `azd env get-values`, which
  shows only what you put there.
## Commands

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
numeric normalization. Numeric dataset values retain their precision rather
than being rounded through floating-point decoding. These fields can contain
prompts, answers, and other sensitive evaluation content. Prefer a private destination with
`run output list --output-file` or `run output export --output-file` over
writing JSON into shared terminal or CI logs.

A command that needs an eval and was not told which one offers a picker.
Selecting **Cancel** is an answer, not a failure: the command says the selection
was cancelled and exits 0, at every command that offers it. Pressing Ctrl+C
interrupts the prompt and exits nonzero, without reporting a successful
cancellation. Under `--no-prompt` or `-o json`, no picker is shown; an ambiguous
eval still produces an error, and no cancellation prose is written to stdout.

`azd ai eval create` closes with a link to the eval in the Portal, for a
newly created eval and for one that already existed unchanged.

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
