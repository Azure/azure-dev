# Azure AI RLE extension for azd

The `azd ai rle` preview extension manages a versioned RLE lifecycle: initialize
an environment or harness scaffold, run it locally, and publish the declared
release to an RLE-enabled Foundry project.

Every source folder is defined by a host-agnostic `rle.toml` manifest. The
manifest owns the immutable RLE identity: `rle.name` plus `rle.version`.
Foundry project endpoints, registry locations, service IDs, credentials, and
mutable deployment state are intentionally not stored in the manifest.

## Prerequisites

Install:

- [Azure Developer CLI (`azd`)](https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd)
- [Azure CLI (`az`)](https://learn.microsoft.com/cli/azure/install-azure-cli)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) or
  [Podman](https://podman.io/)
- [Git](https://git-scm.com/downloads), used by `init` to download samples

Docker is the default container CLI. To use Podman directly for local builds,
container operations, and image pushes, check `podman info` and set the following
in the terminal where you run RLE. On Windows/macOS, initialize a Podman machine
once with `podman machine init` if needed, and start it with `podman machine start`
if it is stopped. Native Linux does not require a machine.

```powershell
$env:AZD_CONTAINER_RUNTIME = "podman"
$env:DOCKER_COMMAND = "podman"
az acr login --name "<registry>"
```

Bash equivalent:

```bash
export AZD_CONTAINER_RUNTIME=podman
export DOCKER_COMMAND=podman
az acr login --name "<registry>"
```

Sign in with `az login` before registry login. `DOCKER_COMMAND` configures Azure
CLI registry login; `AZD_CONTAINER_RUNTIME` independently selects the executable
RLE uses. Native Podman does not need Docker Desktop, `DOCKER_HOST`, or Buildx.
Use `docker` for both variables to select Docker Desktop instead.
An unset or empty `AZD_CONTAINER_RUNTIME` defaults to `docker`; an unavailable
executable produces a command error without falling back to another runtime.

Sign in before calling Foundry project APIs:

```powershell
az login
```

The extension also supports `azd auth login` and other development credentials
from Azure's default credential chain.

### Service request IDs

When `azd ai rle train` cannot submit a fine-tuning job, or an RLE API call fails,
the error includes `Request ID: <id>` when the service supplies a request ID in
its response headers or error correlation details. Share this ID with the team
along with the command and error message to help investigate the failure.
Failures without a service response, such as authentication or network failures,
may not have a service request ID.

Register the appropriate RLE source and install the extension.

For the internal development team:

```powershell
azd extension source add `
  --name rle-dev `
  --type url `
  --location "https://raw.githubusercontent.com/<owner>/<repository>/main/cli/azd/extensions/registry.rle-dev.json"

azd extension install azure.ai.rle --source rle-dev
azd ai rle version
```

Replace `<owner>/<repository>` with the GitHub repository configured as `origin`
in the implementation checkout.

Private preview customers use the public distribution branch:

```powershell
azd extension source add `
  --name rle `
  --type url `
  --location https://raw.githubusercontent.com/Azure/azure-dev/rle-releases/cli/azd/extensions/registry.rle.json

azd extension install azure.ai.rle --source rle
azd ai rle version
```

If the extension was previously installed from `rle-ext` or a local build,
register the `rle` source above and reinstall from it:

```powershell
azd extension install azure.ai.rle --source rle --force
```

The private preview label describes product access; this distribution branch
and its binaries are public.

List registered extension sources at any time with:

```powershell
azd extension source list
```

### Extension updates

The extension resolves its installed source from the active azd profile and
checks that source's registered registry. Dev and customer installations use
identical binaries; their source registrations keep update checks isolated.
Custom source aliases are supported, and update suggestions use the installed
alias rather than a build-time channel name. URL and file registries are supported.
The resolver honors `AZD_CONFIG_DIR` and verifies that the running executable is
the one recorded for the installed RLE extension.
A non-breaking update does not interrupt the command. After the command
completes, the CLI displays:

```text
RLE extension update available: <version>
To update, run `azd extension update azure.ai.rle --source rle`
```

If the update path includes a release marked as breaking, lifecycle commands are
blocked until the extension is updated. Commands needed to inspect the extension,
such as `azd ai rle version` and help, remain available.

Update to the latest release from the registered source:

```powershell
azd extension update azure.ai.rle --source rle
azd ai rle version
```

Development installations instead suggest `--source rle-dev`, matching their
registered source. If source resolution or registry access fails, the command
continues with an explicit warning on stderr; it never falls back to another
channel. `version`, help, and metadata remain available. Unversioned `dev` builds
skip update checks, and versioned standalone binaries warn rather than borrow
another installed binary's source. Bundle sources without a live registry are
reported as unsupported for RLE update checks.

Harness scaffolds, samples hidden from the default catalog, and other
internal-only surfaces are gated by a single flag for the RLE team's own
iteration:

```powershell
$env:AZD_AI_RLE_ENABLE_ALL = "true"
```


`azd ai rle init --type Harness` always copies a complete, runnable pattern of
both halves wired together (agent implementation, Dockerfile, mock tools, a
grader, and the RLE wrapper) into `<folder-name>/agent` and `<folder-name>/rle`,
so you have something that runs end to end out of the box instead of starting
from scratch. Run/publish from `<folder-name>/rle`; build and deploy
`<folder-name>/agent` yourself, then update `rle.toml`'s
`baseUrl`/`agentName`/`agentVersion` to match.

See
[`examples/harness/hosted-agent`](https://github.com/sujit-kamireddy/rle-samples/tree/main/examples/harness/hosted-agent)
and
[`examples/harness/byoh`](https://github.com/sujit-kamireddy/rle-samples/tree/main/examples/harness/byoh)
in the [`rle-samples`](https://github.com/sujit-kamireddy/rle-samples) repository
for the samples `init` copies.

## Manifest contract

`rle.toml` uses the RLE control-plane type and subtype values exactly:

| `rle.type` | `rle.subtype` | Required fields |
| --- | --- | --- |
| `Gym` | `OpenEnv` | None |
| `Harness` | `HostedAgent` | `agentName`, `agentVersion` |
| `Harness` | `BYOH` | `baseUrl` |

`BYOH` is the concise wire abbreviation for Bring Your Own Harness.
`HostedAgent` denotes the Foundry Hosted Agent backing a harness; the subtype
does not embed a platform name so the manifest remains host-agnostic.

A Gym: OpenEnv environment:

```toml
schema_version = "1.0.0"

[rle]
name = "code_rl"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"
```

A Foundry Hosted Agent-backed harness:

```toml
schema_version = "1.0.0"

[rle]
name = "support_rle"
version = "1.0.0"
type = "Harness"
subtype = "HostedAgent"
agentName = "support-agent"
agentVersion = "12"
```

A bring-your-own harness (BYOH):

```toml
schema_version = "1.0.0"

[rle]
name = "customer_rle"
version = "1.0.0"
type = "Harness"
subtype = "BYOH"
baseUrl = "https://harness.example.com/rle/"
```

`rle.version` is the RLE release version and is distinct from
`agentVersion`, which identifies the Hosted Agent backing a harness. RLE versions must be
`major.minor.patch`. Hosted Agent versions must be a positive integer or
`draft-<positive-unix-timestamp>`. BYOH harness URLs must be absolute HTTPS URLs
without credentials, a query string, or a fragment.

### Environment protocol

RLE always drives environments with the MCP environment protocol, so `rle.toml` has no
`environment_protocol` / `environmentProtocol` key and the CLI no longer sends one to the
service. MCP Gym samples submit their final response using `GradeAction.answer`, rather than
a schema-driven `defaults.gym_openenv.model_response_field`.

Manifests from before this change must remove `environment_protocol` (root) or
`environmentProtocol` (under `[rle]`): the manifest is parsed in strict mode, so either key
now fails to load.

### Version-scoped defaults

`schema_version` is a root-level manifest field, separate from the immutable
`rle.version`. It is required when reusable `defaults` is present. The current
schema is `1.0.0`; `init` writes it into newly generated manifests.
Identity-only legacy manifests without defaults remain valid.

```toml
schema_version = "1.0.0"

[rle]
name = "mycoderle"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"

[defaults.model]
name = "Qwen/Qwen3-32B"
renderer_name = "qwen3_disable_thinking"

[defaults.reinforcement]
max_episode_steps = 5

[defaults.reinforcement.hyperparameters]
n_epochs = 3
batch_size = 8
learning_rate_multiplier = 0.1
eval_interval = 20
eval_samples = 100
compute_multiplier = 1.0
reasoning_effort = "medium"

[defaults.grpo]
group_size = 8
groups_per_batch = 128
max_steps = 150

```

All defaults are optional. The CLI normalizes surrounding whitespace, requires
positive finite numeric defaults, and limits `reasoning_effort` to `low`,
`medium`, or `high`.

The CLI sends the manifest as the RLE create payload's `version`,
`schemaVersion`, and `defaults` fields. The RLE version owns reusable defaults;
training and evaluation jobs own datasets, job identity, result locations,
credentials, graders, response format, tools, and explicit per-job overrides.
Precedence is:

```text
explicit job override > pinned RLE default > Training Jobs/model default
```

`defaults.reinforcement.hyperparameters.batch_size` and
`defaults.grpo.groups_per_batch` are distinct fields. The CLI never maps one
to the other.

## Configure runtime deployment context

The CLI resolves the Foundry project endpoint from the terminal environment:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"
```

Publishing also needs the ACR registry endpoint:

```powershell
$env:AZURE_CONTAINER_REGISTRY_ENDPOINT = "<registry>.azurecr.io"
az acr login --name <registry>
```

The endpoint and registry are deployment context, not source configuration,
and therefore never appear in `rle.toml`.

## Initialize an RLE

Run `init` and select `Gym: OpenEnv`, then select a sample:

```powershell
azd ai rle init
```

The Gym: OpenEnv path reads the available environments from
[rle-samples](https://github.com/sujit-kamireddy/rle-samples). It downloads
only the selected sample, including its manifest, plus all project skills,
including the canonical `rle` authoring skill, which covers Gym/OpenEnv and
Harness (BYOH and HostedAgent) environments alike.
The skills are installed twice, into `.agents/skills` and `.claude/skills`,
because no single directory reaches every agent: Claude Code discovers project
skills only under `.claude/skills`, OpenAI Codex only under `.agents/skills`,
and GitHub Copilot under either. Compatible agents discover these project
skills automatically and load their guidance when relevant.
Each initialization copies the skills currently available on the sample
repository's `main` branch. Existing projects keep that snapshot and are not
updated automatically by `init`.
When a target folder is specified, `init` updates `rle.name` to match that
folder:

```powershell
azd ai rle init my_environment
```

To install the skills into an existing project, or update them to the latest
versions from `rle-samples/main`, run this command from the project root:

```powershell
azd ai rle skill install
```

The command adds or replaces the skills supplied by `rle-samples` in both
`.agents/skills` and `.claude/skills`, and preserves unrelated project skills.
Either every skill in every directory is updated or none of them are: a failure
part way through restores what was there before.

The samples repository's `examples/gym/openenv/catalog.toml` controls which
samples are offered; entries with `visible = false` are hidden from both the
interactive picker and `--sample <name>`. Samples with no catalog entry
default to visible. Set `AZD_AI_RLE_ENABLE_ALL=true` to bypass the catalog
filter and reveal every sample directory, e.g. to try out a sample before it
is marked visible:

```powershell
$env:AZD_AI_RLE_ENABLE_ALL = "true"
```

For noninteractive sample initialization, the positional name selects the
sample and target folder:

```powershell
azd ai rle init code_rl --no-prompt
```

With `AZD_AI_RLE_ENABLE_ALL=true`, `init` also offers
`Harness: HostedAgent` and `Harness: BYOH`. Supply control-plane type/subtype
values explicitly when scripting. `--agent-name`, `--agent-version` and
`--base-url` are optional overrides applied on top of the sample's own
manifest:

```powershell
azd ai rle init support_rle `
  --type Harness --subtype HostedAgent `
  --agent-name support-agent --agent-version 12 `
  --no-prompt

azd ai rle init customer_rle `
  --type Harness --subtype BYOH `
  --base-url https://harness.example.com/rle/ `
  --no-prompt
```

```text
<environment-name>/
|-- agent/     # the harness/agent implementation -- build and deploy this yourself
`-- rle/       # the RLE wrapper -- run/publish from here
    |-- rle.toml
    |-- Dockerfile
    `-- server/
```

Run/publish from `<environment-name>/rle`; once you deploy your own copy of
`<environment-name>/agent`, update `rle.toml`'s
`baseUrl`/`agentName`/`agentVersion` to match.

## Run locally

Run from the folder that contains `rle.toml`:

```powershell
azd ai rle run
```

`run` derives its local image and container identity from `rle.name`, builds
the Dockerfile, waits for `/health`, opens the `/web` playground, and keeps an
OpenEnv shell attached. It removes the local container when the shell exits.

Use a custom port or Dockerfile path when needed:

```powershell
azd ai rle run --port 9000
azd ai rle run --dockerfile server\Dockerfile
azd ai rle run --watch
```

The shell supports standard OpenEnv commands:

```text
rle> health
rle> reset {"seed":0}
rle> step {"message":"hello"}
rle> state
rle> exit
```

## Publish a declared release

Run publish from the folder containing the manifest:

```powershell
azd ai rle publish
```

For a new RLE name, `rle.version` must be `1.0.0`. For an existing release,
change it to the direct next major, minor, or patch version before publishing.
For example, after `1.2.0`, use `2.0.0`, `1.3.0`, or `1.2.1`.

The CLI sends `rle.version` directly as the immutable control-plane `version`
field and never translates it into legacy `versionBump`. The control plane
validates the first release and every later direct successor against its
allocation high-water mark, which remains authoritative if historical versions
were deleted. The CLI verifies that the service returns the manifest's exact
identity and defaults. The pushed ACR image is version-tagged as:

```text
<registry>.azurecr.io/<project>-<environment>:<rle.version>
```

The published request includes `type`, `subtype`, the applicable HostedAgent
or BYOH configuration, schema version, and defaults from the manifest.
The success message identifies the release by name and version, for example
`Published environment 'math_rl' version 1.0.0`. The JSON details printed
afterward still include `environmentId`.

## Inspect and run releases

List RLEs in the selected Foundry project:

```powershell
azd ai rle list
```

Show version history for a named RLE, or omit the name to use `rle.name` from
the current manifest:

```powershell
azd ai rle show code_rl
azd ai rle show
```

The list table displays `NAME`, `VERSION`, `TYPE`, `SUBTYPE`, `DISK IMAGE`,
and `UPDATED`; show displays the same columns without `NAME`. Missing type,
subtype, or disk image conversion status is shown as `-`, not inferred as
ready. Use `--output json` with either command for full details, including
environment IDs, images, and defaults.

Execute one Loom-backed rollout of the manifest's exact `(name, version)`
identity, using the model declared in `[defaults.model]`:

```powershell
azd ai rle rollout --task '{"...": "..."}'
```

When run from a local Gym/OpenEnv recipe, simple reset payloads such as
`{"seed": 32, "split": "train"}` also work in Windows PowerShell versions that
remove embedded JSON quotes from native-command arguments.

`--model` is only required when rle.toml has no `defaults.model.name` set, or
when running source-free from another folder:

```powershell
azd ai rle rollout code_rl --version 1.0.0 --model Qwen/Qwen3-32B --task-file task.json
```

`rollout` provisions everything a rollout needs and tears it down again: it
creates a real Loom training session for the model (from `--model`, falling
back to rle.toml's `defaults.model.name`), saves a sampler checkpoint, calls
RLE's Execute Rollout API with your `--task`, prints the resulting reward and
trajectory summary, then closes the Loom session — you never handle Loom
session or checkpoint identifiers directly. Use `--task`/`--task-file` for the
sandbox reset payload; it is the only input a rollout takes. For Harness
targets, RLE derives the agent-visible input server-side from the reset
observation — there is no caller-supplied agent input to set.
`--lora-rank` (default `16`), `--rollout-id` (default: a generated GUID),
`--sequence-id` (default `0`, only meaningful when correlating a rollout to a
specific training step in a real training loop), and `--timeout` (default
`600` seconds) all have sensible defaults and rarely need to be set for ad hoc
testing.

The rollout itself travels over RLE's Execute Rollout WebSocket rather than the
HTTP API. The Foundry data-plane gateway ends an Execute Rollout HTTP request
after roughly 120 seconds, which is shorter than a real Harness rollout, so a
rollout that actually succeeded comes back over HTTP as a 408 carrying no
result; the upgraded socket is not subject to that cut. There is nothing to
configure. Where the WebSocket route is not published the upgrade is refused
before any rollout is requested, so the CLI reports it and falls back to the
HTTP API unchanged — and a failure *after* the upgrade is never retried on
HTTP, because by then the rollout may already have run.

While the WebSocket rollout runs, the CLI prints elapsed lifecycle milestones
for target resolution, sandbox and capture setup, model execution, result
collection, grading, and cleanup. Gym/OpenEnv rollouts also report each model
call and environment action. Completed actions include cumulative reward and
episode progress when the service provides it. These updates are best effort;
the final `completed` or `error` message remains authoritative.

### Rollout artifacts

The Execute Rollout response carries the whole Capture Proxy graph — token ids,
logprobs, loss masks and per-turn metadata — which is far too large to print
and is not retrievable afterwards, because the capture session is closed and
deleted as soon as the rollout returns. So every rollout writes it to
`.output/<rollout-id>/` and prints what it wrote:

```text
Artifacts: .output/c4c8a1a410d7968f8c0d8d0aed89a5fb
  ├── rollout.json    99.2 KB  the full capture graph exactly as the service returned it
  ├── summary.json     1.2 KB  outcome, capture stats and the sequence index — start here
  ├── turns.json        276 B  one entry per model call: token counts, finish reason, sampling params
  └── sequences/               one root-to-leaf path each — the unit a trainer consumes
      └── 0.json      74.3 KB  agent, 1 turn(s), 2050 tokens (1903 trainable) — input_ids, loss_mask, logprobs
```

- **`summary.json`** — the outcome (`reward`, `success`, `result`, `episode`)
  with the graph's own `stats`, `capture_level` and `validation`, plus an index
  of each sequence's shape and the file holding it. The arrays are deliberately
  left out so this stays readable.
- **`sequences/<n>.json`** — one root-to-leaf path: `input_ids`, `loss_mask`
  and `logprobs`, index-aligned and equal in length, with `turn_lengths` as the
  join key against a harness trace. This is what a trainer consumes.
- **`turns.json`** — one entry per model call in arrival order, including calls
  excluded from training, so an external trace can be reconciled against it.
- **`rollout.json`** — the full capture graph (`response.rollout`), not the entire
  execute response. Original JSON numbers and unknown graph fields are retained.

New summaries preserve optional `success` and `episode.ungraded`. An `artifact`
metadata block records the export version, local save time, project endpoint
without credentials, and the environment name/version used in the request.
It is local context, not an API outcome field.

On an eval capture the token arrays are empty by construction rather than by
failure; the tree says so rather than showing an unexplained small file.

Use `--output-dir` to write somewhere other than `.output`. A rollout that
succeeds but cannot write its artifacts still reports its reward and exits
successfully, with a warning — the compute is already spent. The warning also
notes that the dashboard was skipped. Existing rollout
directories are never overwritten; use a new rollout ID
or a different output root. The summary is published last, after the other files.

## Monitor a completed rollout

Open a local, read-only dashboard to explore a saved rollout's reward,
environment, model-call graph, and token metrics.

Every rollout opens its dashboard when execution finishes:

```powershell
azd ai rle rollout --task-file task.json
```

The dashboard is skipped when `--output` is set, or when the Loom session cannot
be closed or the artifacts cannot be saved (the rollout warns instead).

Or reopen a saved rollout from the folder where it was executed:

```powershell
azd ai rle monitor --rollout-id 3c27c30f5fba261c3a7a3e856b4e1388
```

The dashboard shows a compact final-response preview when the service reports one.
Captured `<think>` reasoning is hidden from the preview, and the complete response
can be expanded. The summary also derives tool activity and a captured-order call
timeline from model messages. It includes **Rollout graph**, **Model conversation**,
**Tokens and Metrics**, and **Rollout Stats** tabs, with a light/dark mode toggle.
The conversation tab appears when captured turns include request or response
messages. Viewing saved results needs no Azure sign-in and does not execute another
rollout.

Keep the terminal running while using the dashboard. **Ctrl+C** stops the local
monitor without deleting saved artifacts.

| Option | When to use it |
| --- | --- |
| `--output-dir <path>` | Read from an artifact root other than `.output` in the current folder. Pass the parent of the rollout-ID directories, not an individual rollout folder. |
| `--logs-root <path>` | With facade `--job-id` monitoring, read the run mirror `train` wrote somewhere other than `$LOOM_LOGS_ROOT` or `~/loom-runs`. Not supported for real-service monitoring. |
| `--no-browser` | On standalone `monitor`, print a link instead of opening the browser. Open the printed link yourself. |

`--no-prompt` does not disable browser launching or stop the monitor.
`--output` is not supported on standalone `monitor`.

Monitoring `--rollout-id` reads the existing [rollout artifacts](#rollout-artifacts),
not remote results. If the rollout directory is missing, it warns and exits without
opening a dashboard; check the ID and `--output-dir`. Incomplete or corrupt files
return an error.

**Current limits:** `--rollout-id` shows one completed local snapshot, with no
live updates; use `--job-id` to follow a training run as it goes. Conversation
text and metrics appear only when included in the saved data. Execution
completion does not imply task success.

**Treat saved artifacts as sensitive:** they may contain customer content.
Keep `.output` out of source control and delete artifacts when no longer needed.
The dashboard's local HTTP server binds to loopback (`127.0.0.1`) only and is not
reachable from other machines, but it does not require a credential — any other
process or user on the same machine can query it while `monitor` is running.

## Submit an RLE-backed fine-tuning job (experimental)

`azd ai rle train` submits a reinforcement fine-tuning job to a fine-tuning
resource's public `/openai/v1/fine_tuning/jobs` API, using the `rl_environment`
method. A published RLE environment supplies the reward signal instead of a
grader, but the service still requires a training file as the Loom job input.
`rl_environment` is currently hidden from finetunesapi's public API surface and
only completes for base models the service has enabled for Loom-backed
RL-environment training; job creation fails for other models, or if the named RLE
version is not published and ready in the Foundry project set by
`FOUNDRY_PROJECT_ENDPOINT`.

This command is gated behind `AZD_AI_RLE_ENABLE_ALL`, since it targets an
unreleased method and the CLI shape is still subject to change:

```powershell
$env:AZD_AI_RLE_ENABLE_ALL = "true"
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"

azd ai rle train `
  --rle-name code_rl --rle-version 1.0.0 `
  --model Qwen/Qwen3-32B `
  --training-file .\training.jsonl
```

`FOUNDRY_PROJECT_ENDPOINT` identifies the project that owns the named RLE. The
fine-tuning endpoint is normally derived from the same account as
`https://<account>.openai.azure.com`. Set `RLE_TRAIN_ENDPOINT` to route `train`,
and `jobs` to a facade or mock service instead. An explicit
`--endpoint` still takes precedence.
Job creation automatically acquires a Foundry token
(`https://ai.azure.com/.default`) and forwards it in `aml-user-token` for
evaluation authentication. The request's `Authorization` header still uses
the Cognitive Services token. No token parameter is required.
For development, set `RLE_ENV_OVERRIDE` to have `train` send its value in the
`rleEnvOverride` request header, which pins the RLE environment the job runs against.
A training file is required and must point to a regular local training dataset
file. The extension uploads it to the selected fine-tuning resource with the
`fine-tune` purpose, waits up to five minutes for the asynchronous file import
to reach `processed`, then uses the returned file ID when it submits the job.
The validation file is optional and follows the same local-file upload flow.
Use `--endpoint` to target a different fine-tuning resource for a single
invocation.

### Real-service monitoring (default endpoint)

Without `--endpoint` or `RLE_TRAIN_ENDPOINT`, **train opens the job monitor
after submission by default**, even with redirected output or `--no-prompt`;
pass `--no-follow` to skip it and exit immediately instead. Keep the process
running to use the UI; Ctrl+C stops monitoring, not training. `--no-browser`
prints the URL without opening a browser. Explicit `--output` is rejected on
this path rather than silently ignored.

Reopen the same monitor from any machine with project access:

```powershell
azd ai rle monitor --job-id ftjob-7898670d3ae8453e89e591f9
```

Both commands read the same project-scoped RLE APIs, with no local run mirror.
The Summary shows registered config, available metric charts, and lightweight
rollout summaries. Full rollout results/graphs are fetched **only on click**.
An unregistered job shows a waiting state; unavailable/expired results leave
metadata visible. The producer must register the same job ID, publish complete
metric rows with a nonnegative integer `step` (`step_id` alone is not sufficient),
and associate rollouts with the job. `sequence_id` is optional; rollouts are listed in
creation order. Fine-tuning retries retain the same job ID, so when rollout summaries report
multiple `session_id` values the UI separates them into distinct training attempts. Session-based
labels remain accurate when the monitor's bounded history no longer includes the initial attempt.

The CLI caches data in memory: the latest 5,000 metric rows (up to 32 MiB) and the
latest 1,000 rollout summaries. The UI identifies truncated history. Restarting reloads persisted
data. No training config, metric, graph, or log mirror files are created.
Credentials remain in the CLI process.

Polling is shared across browser tabs:

| Data | Interval |
| --- | --- |
| Config | 5 seconds until registered, then cached |
| New metrics (incremental) | 10 seconds |
| Rollout summaries, a full oldest-first list scan of the job | 1 minute |
| Running rollouts shown on the open Rollouts tab, point-read for status and results | 10 seconds |
| Other running rollouts (newest first, at most 50 reads per cycle) | 1 minute |
| Metric reread, for late rows | 60 seconds |
| RLE job status | 1 minute until terminal |
| Full rollout result | On click only |

Each rollout scan starts without a continuation token and follows tokens, including
across empty pages, until none is returned; summaries are merged by `rollout_id`.
Scans and rereads run in bounded page batches, and an unfinished scan continues on
the next tick. Large jobs therefore reread every summary page each minute.
Service errors include their operation and request IDs. Service
errors back off and respect `Retry-After`; existing data remains visible with an
error/freshness notice. `FeatureDisabled` stops automatic polling. After terminal job status, polling pauses once a 60-second settling
period and a final full scan have completed successfully. **Refresh service
data** checks for later arrivals without downloading any graphs.

The current API has no training logs, rollout split, training-step mapping, or
task ID. The real-service UI therefore omits logs, split/task columns, and
task-group-derived charts. Checkpoint filtering is available; exact
metric-step-to-rollout navigation is not inferred from checkpoint strings.
Execution status and the grader's task verdict are separate.

The project must expose the RLE persistence APIs and grant RLE read access.
Submission still uses the fine-tuning service. Status, registration/config,
metrics and rollout reads use the Foundry project's RLE service. Status uses
`GET /rl_environments/jobs/{jobId}?api-version=2025-11-15-preview`,
using the same API version as the other RLE reads.
Only the new job envelope is supported: its top-level `id` must match the requested
job, and its non-empty top-level `status` drives polling. The dashboard reads registered configuration
directly from the `metadata` object, not from `metadata.configuration`.
Invalid identities, missing/blank status, and missing/non-object metadata are
reported as read errors. Legacy `job_id` responses and root-level configuration
are not supported; `metadata.status` does not affect polling.
Only `succeeded`, `failed`, and `cancelled` are treated as terminal job statuses,
matching the fine-tuning API v1 contract.
`monitor --job-id` no longer accepts `--endpoint`; `RLE_TRAIN_ENDPOINT` does not
affect status polling, including the monitor opened by `train`.

### Facade monitoring (endpoint override)

An explicit `--endpoint` or non-empty `RLE_TRAIN_ENDPOINT` preserves the existing
facade path, even if an explicit endpoint happens to name an OpenAI resource.
On this path, `train` mirrors the run's artifacts to the local disk as they are
written and waits for the job to finish, by default:

```powershell
azd ai rle train ... --logs-root $HOME/loom-runs
```

Files land in `<logs-root>/rle-harness/<job id>/`, which is the layout the Loom
cookbook's `dashboard_server.py` discovers, so a run being mirrored can be opened in
the existing dashboard while it is still going:

```bash
python dashboard_server.py --root ~/loom-runs
```

`--logs-root` defaults to `$LOOM_LOGS_ROOT`, else `~/loom-runs`. Mirroring is
resumable: re-running `train` for the same job continues from the bytes
already on disk. If the stream drops, `train` reports it but still exits zero --
the job was accepted and is running on the service, and a non-zero exit would
say otherwise.

By default `train` also serves the run dashboard for the job it submitted, and
prints its address:

```text
Job monitor for ftjob-1234 (0 rollouts so far): http://127.0.0.1:41233/
```

The dashboard reads the same local mirror the stream is writing, so a mirrored
run is shown as a run rather than as a list of rollouts:

- **What it is** -- environment name and version, base model, renderer, Loom
  session, context window, dataset counts, and the hyperparameters the service
  actually applied. Facade defaults override recipe values, so this is where to
  check what a job is really training with. Training cases read `32 of 804`:
  what the run trains on, out of what the file holds.
- **How it is going** -- reward, task success, learning signal, gradient norm,
  policy entropy and KL, charted per step, with the latest value of each and
  how far it has moved since the previous reading.
- **What went wrong** -- a reward collapse or a run whose groups have stopped
  disagreeing is named in plain text above the charts, with what to change.
- **The run log**, tailed incrementally.

It opens empty, because the first rollout and the first step of a run both take
minutes. The page polls as the run records them, so rollouts and steps appear
without reloading, and any rollout can be opened while the run continues. The
dashboard stays up after the run finishes -- that is when its rollouts are
finally all there to read -- so stop it with Ctrl+C. `--no-browser` prints the
address without opening a browser.

With `--no-follow`, the command exits as soon as the job is accepted, so there is
nothing left to serve a dashboard from. It names the command that opens one
instead:

```text
Watch this run's rollouts as they land:
  azd ai rle monitor --job-id ftjob-1234
```

`monitor --job-id` shows the same run panels for any job that was mirrored on
this machine (that is, not submitted with `--no-follow`), reading `--logs-root`
(default `$LOOM_LOGS_ROOT`, else `~/loom-runs`). A job that was never mirrored
here still lists its rollouts -- those come from the service -- with the run
panels omitted.
### Training settings in `rle.toml`

Everything above can be recorded in the environment's own `rle.toml`, so a run
is one command instead of a line of flags nobody remembers:

```toml
[rle]
name = "code_rl"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"

[train]
model = "Qwen/Qwen3-32B"
training_file = "../job_data/train.jsonl"
validation_file = "../job_data/validation.jsonl"

[train.options]
learning_rate = 4e-05
max_steps = 30
group_size = 4
```

```powershell
cd rle
azd ai rle train
```

Any flag overrides the manifest for a single run, so a saved configuration stays
usable without being edited.

`[train]` is deliberately separate from `[defaults]`. `[defaults]` describes the
published environment version -- `publish` sends it to the service and refuses a
local copy that has drifted -- and published versions are immutable, so a value
that changes from run to run cannot live there. `[train]` is local and is never
published.

Option names under `[train.options]` are validated by the service, which rejects
an unknown one rather than dropping it: a typo that was quietly ignored would
produce a job that trained on defaults while its record claimed otherwise. The
error lists every accepted name.

`--model` and `--training-file` are not required flags, because the manifest can
supply both. They are still required settings, and a run that resolves neither
reports which one is missing and where it can be set.

### Smaller runs

`--task-count` trains on only the first N tasks of the dataset, which is the
quickest way to check an environment end to end without editing the dataset or
the manifest:

```powershell
azd ai rle train --task-count 8
```

It sets the `max_train_examples` training option, and takes precedence over the
same option in `[train.options]`.

## List RLE-backed fine-tuning jobs (experimental)

`azd ai rle jobs` lists fine-tuning jobs that use the `rl_environment` method.
It is gated by the same preview variables as `train` and derives the fine-tuning
resource from `FOUNDRY_PROJECT_ENDPOINT`:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
$env:AZD_AI_RLE_ENABLE_ALL = "true"
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"

azd ai rle jobs
azd ai rle jobs --output json
```

The command requests only `rl_environment` jobs from the fine-tuning API and
verifies that filter before it renders results. Use `--endpoint` to query a
different fine-tuning resource for a single invocation.

## Build and install from source

From `cli\azd\extensions\azure.ai.rle`:

```powershell
azd extension install microsoft.azd.extensions
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

## Prepare an internal or external release

Run the audience-specific release script from
`cli\azd\extensions\azure.ai.rle`. Both wrappers use
`prepare-release.ps1` for the shared build, packaging, registry, and validation
logic.

For a normal internal, non-breaking release, choose the semantic-version
component to increment:

```powershell
.\prepare-dev-release.ps1 -VersionBump patch
.\prepare-dev-release.ps1 -VersionBump minor
.\prepare-dev-release.ps1 -VersionBump major
```

The script preserves the existing prerelease suffix. For example,
`0.8.8-preview` becomes `0.8.9-preview` with `-VersionBump patch`. It updates
`version.txt` and `extension.yaml`, cross-compiles the extension for every
supported platform (`windows`, `darwin`, and `linux` on both `amd64` and
`arm64`), writes development artifacts under `artifacts\rle-dev\<version>`, and updates
the development registry or public `registry.rle.json` with each artifact's
checksum and GitHub URL.
`azd x pack` archives linux artifacts as `.tar.gz` and every other platform as
`.zip`. Registry URLs and source aliases are not embedded in the binaries;
the installed azd source determines update enforcement. The script runs on any host
Go can cross-compile from.

Both channels share `version.txt` and `extension.yaml`. A development release
prepares only the dev channel. An external release advances the version once and
prepares that same version for both dev and customer distribution. It builds
and packages once, then updates both registries and copies the identical archives
to the public checkout. Update checks remain isolated by installed source;
a development-only release does not notify customer installations.

Prepare an external release by supplying an existing, separate `Azure/azure-dev`
checkout. The script fetches and switches to the existing remote `rle-releases` branch:

```powershell
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -VersionBump patch
```

Both wrappers accept `-VersionBump major|minor|patch` and the optional
`-BreakingChanges` switch. When omitted, `-VersionBump` defaults to `patch`:

```powershell
.\prepare-dev-release.ps1
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev"
```

Mark a release as breaking only when users must update before continuing:

```powershell
.\prepare-dev-release.ps1 -VersionBump patch -BreakingChanges
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -VersionBump patch -BreakingChanges
```

Non-breaking is the default; do not pass `-BreakingChanges` for a normal release.
For internal releases, review and commit the two version files, generated
artifacts, and registry change together. The development registry targets the
implementation repository's `main` branch.
The repository owner/name is inferred from the local checkout's `origin` remote;
it is not tied to a particular fork. GitHub HTTPS and SSH origins are supported.
Missing or unsupported origins are rejected before the version changes.
The publication branch remains `main`, regardless of the current working branch.
Standalone `build.ps1` and `build.sh` build only the implementation and version
metadata, without selecting a distribution registry or source.

For external releases, `-AzureDevRepositoryPath` is required on each invocation.
Omitting it fails immediately without prompting for input. There is no
environment-variable fallback or automatic default. The script never creates a
checkout. Its fetch and push origin must be `Azure/azure-dev`. The checkout must
be clean and have no unfinished Git operations. The script fetches the remote
state and switches to `rle-releases`, creating a local tracking branch only from
the fetched `origin/rle-releases` if needed. It never creates a release branch
from the current local branch; a missing remote branch fails before building.
Single-branch clones are supported by adding the release branch to the remote's
fetch configuration before setting up local tracking.
The release branch must have no unpublished or diverged commits and is
fast-forwarded before building. Invalid state is rejected before the source
version is changed. Committed branch contents are not compared with upstream
`main`; previously committed files, including branch-specific ignore rules,
do not make the checkout dirty.

The external script reuses all six dev packages without rebuilding or repacking.
It copies and verifies all six customer packages in the supplied checkout before
updating the customer registry or reporting customer preparation success. Each
channel summary shows its own artifact directory and registry, followed by a
short reminder to review the changes and raise or update PRs in the corresponding
repositories. Individual archives and publication commands are not printed.
It does not stage files, create commits, push, or manage PRs. Publishing is manual
and requires working Git authentication and write access to `Azure/azure-dev`.
Pushing `rle-releases` updates the existing distribution PR.

New artifacts use version-specific branch URLs and SHA-256 checksums. Earlier
entries and their URLs remain unchanged. A published version must never be
overwritten; the script rejects an existing version. Commit the registry and
all six archives together. On `rle-releases`, the RLE ignore rules expose the six
customer archives in version directories to Git and Git GUI without force-staging,
while dev packages and other build output remain ignored.
Customers can install after the push without a PR merge.
External packages are stored at `azure.ai.rle/artifacts/<version>/` in the public
checkout. Development packages retain the existing `artifacts/rle-dev/<version>/`
layout and `registry.rle-dev.json` in the implementation checkout. The external
path setting is not used by `prepare-dev-release.ps1`. The obsolete fork external
registry and tracked packages have been removed; customer distribution is exclusively
in `Azure/azure-dev`. After copying and verifying all six customer package
checksums, the script deletes temporary `bin` build output. No separate external
staging directory is created. If copying or verification fails, local output is retained.
Dev packages, the dev registry, and source version-file changes are retained
for manual commit; the script does not reset the implementation checkout.
Do not delete this branch while customers use the registry. New releases update
the same branch and PR without changing previous artifact URLs.

For an external release, commit `version.txt`, `extension.yaml`, the dev registry,
and dev packages together in the implementation repository. Commit release tooling
changes there as well. Never include implementation source changes
in the public distribution PR. The external script does not change repository
visibility or migrate samples/SDK dependencies. Source already published remains
public. GitHub Release assets can replace binary commits in a follow-up while
retaining the same registry URL and keeping older downloads available.
