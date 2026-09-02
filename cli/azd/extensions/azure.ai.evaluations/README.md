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

That holds for the configuration as a whole. It does **not** hold for a `$ref`
on a single catalog entry: azd rebases only the path keys it owns, so a relative
`source:` written inside `evals/evaluators/quality.yaml` still resolves against
`azure.eval.yaml` and will not be found.

A `$ref` fills the field that holds it, so a rubric kept in its own file is
pulled in at `definition:` — no second file to find, and nothing to rebase:

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
| `azd ai eval dataset` | `create` · `update` · `list` · `show` · `delete` · `versions list` |
| `azd ai eval evaluator` | `create` · `update` · `list` · `show` · `delete` · `versions list` |
| `azd ai eval run` | `start` · `list` · `show` · `cancel` · `delete` · `output list` · `output show` · `output export` |
| `azd ai eval job` | `list` · `show` · `cancel` · `delete` |

`create` and `update` both publish a new immutable version; the server
auto-increments and nothing mutates in place.

`delete` asks before removing anything and takes `--force` to skip the
question, which is what a pipeline passes. `job delete` is the exception: it
discards a record of finished work, not the artifact the job produced.

Every command supports `-o json` and `--no-prompt`, so the whole surface is
usable from CI.

## Evaluators

Built-ins need no declaration — reference them as `builtin.<name>` and list
them with `azd ai eval evaluator list --builtin`.

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
3. `extensions.ai-agents.project.context.endpoint` in azd's global config,
   which `azure.ai.agents` writes and this extension only reads
4. `FOUNDRY_PROJECT_ENDPOINT` in the host environment, then
   `AZURE_AI_PROJECT_ENDPOINT`

Level 3 is worth knowing about: it is machine-wide rather than per-project, so
a project context left behind by `azd ai agent` somewhere else takes precedence
over the variable exported in this shell. `--debug` prints which level answered.

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

go test -tags live ./internal/cmd/ ./tests/live/
```

They clean up every resource they create.

### Debug logging

Request tracing is off by default. `--debug`, or `AZD_EXT_DEBUG=true`, writes
it to a dated log file rather than the terminal.

## TODO before release

Both are files the azd extensions team owns, so they are not changed here:

- [ ] **`cli/azd/extensions/registry.json`** — add the `azure.ai.evaluations`
  entry. Until it exists `azd extension install azure.ai.evaluations` cannot
  resolve, so the extension is only reachable through `azd x pack` +
  `azd x publish` into the local source registry.
- [ ] **`microsoft.foundry/extension.yaml`** — add the dependency, but only
  after the registry entry lands. Declaring a dependency that cannot resolve
  breaks installing the bundle.
