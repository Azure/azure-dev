# Azure AI RLE extension for azd

The `azd ai rle` preview extension manages a versioned RLE lifecycle: run an
existing environment locally, publish its declared release to an RLE-enabled
Foundry project, and execute and inspect rollouts.

Every source folder is defined by a host-agnostic `rle.toml` manifest. The
manifest owns the immutable RLE identity: `rle.name` plus `rle.version`.
Foundry project endpoints, registry locations, service IDs, credentials, and
mutable deployment state are intentionally not stored in the manifest.

## Prerequisites

Install:

- [Azure Developer CLI (`azd`)](https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd)
- [Azure CLI (`az`)](https://learn.microsoft.com/cli/azure/install-azure-cli)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) or
  [Podman](https://podman.io/) for local builds and `run`; not required for `publish --remote`

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
completes, the CLI displays the notice on stderr, keeping stdout unchanged for
command results, including `--output json`:

```text
RLE extension update available: <version>
To update, run `azd extension update azure.ai.rle --source rle`
```

If the update path includes a release marked as breaking, lifecycle commands are
blocked until the extension is updated. Commands needed to inspect the extension,
such as `azd ai rle version` and help, remain available.
`azd ai rle jobs cancel <job-id>` also bypasses update checks, so stopping an
existing billable job never requires upgrading the extension first.

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

Experimental training commands (`train`, `jobs`, and `jobs cancel`) are hidden from
help by default. Show them for the RLE team's own iteration with:

```powershell
$env:AZD_AI_RLE_ENABLE_ALL = "true"
```
## Manifest contract

`rle.toml` mirrors the RLE service `task` and `harness` objects. `[harness]` has these keys:

| Key | Values | Notes |
| --- | --- | --- |
| `type` | `gym` (default), `byoh`, `package` | Lowercase. Omitted means `gym`. `package` requires `[task] format = "harbor"`. |
| `name` | package name, for example `opencode` | Valid only with `type = "package"`. |
| `endpoint` | absolute `https` URL | Required for `byoh`; omitted for `gym`. No credentials, query, or fragment. |
| `protocol` | `responses`, `rle-invocation` | Optional for `byoh`; omitted for `gym`. |

When `protocol` is omitted for `byoh`, the service resolves it: `responses` when
`endpoint` is your own Foundry project agent URL
(`https://<account>.services.ai.azure.com/api/projects/<project>/agents/<name>`),
otherwise `rle-invocation`. `responses` is a Foundry hosted agent; `rle-invocation`
is RLE's own bring-your-own-harness POST/poll contract. For `responses`, give the agent URL only; RLE appends `/endpoint/protocols/openai/responses`. Agent versions are not targetable.

`[task]` has one optional key, `format` (`json` default, `harbor`).
`format = "harbor"` requires `[harness] type = "package"` and a harness `name`;
conversely, a package harness requires Harbor tasks. Publish and rollout support
this combination; local `run` supports only Gym/OpenEnv environments.
See [Harbor tasks](#harbor-tasks) for a manifest and publishing example.
Unknown values are rejected.

```toml
[rle]
name = "code_rl"
version = "1.0.0"

[task]
format = "json"   # json (default) | harbor

[harness]
type = "gym"      # gym (default) | byoh | package
```

This replaces the previous `type` (`Gym`/`Harness`), `subtype`
(`OpenEnv`/`HostedAgent`/`BYOH`), `agent_name`, `agent_version`, and
`invocation_url` keys. There is no backward compatibility: manifests that still set
any of them fail to load with a message naming the replacement.

A gym environment (the default; `[harness]` may be omitted entirely or just set `type`):

```toml
schema_version = "1.0.0"

[rle]
name = "code_rl"
version = "1.0.0"

[harness]
type = "gym"
```

A bring-your-own harness backed by a Foundry hosted agent (`responses`):

```toml
schema_version = "1.0.0"

[rle]
name = "support_rle"
version = "1.0.0"

[harness]
type = "byoh"
endpoint = "https://myacct.services.ai.azure.com/api/projects/myproj/agents/support-agent"
protocol = "responses"
```

A bring-your-own harness backed by a custom https service (`rle-invocation`):

```toml
schema_version = "1.0.0"

[rle]
name = "customer_rle"
version = "1.0.0"

[harness]
type = "byoh"
endpoint = "https://harness.example.com/rle/"
protocol = "rle-invocation"
```

RLE versions (`rle.version`) must be `major.minor.patch`.

### Environment protocol

RLE always drives environments with the MCP environment protocol, so `rle.toml` has no
`environment_protocol` / `environmentProtocol` key and the CLI no longer sends one to the
service. MCP Gym samples submit their final response using `GradeAction.answer`, rather than
a schema-driven `rollout.gym_openenv.model_response_field`.

Manifests from before this change must remove `environment_protocol` (root) or
`environmentProtocol` (under `[rle]`): the manifest is parsed in strict mode, so either key
now fails to load.

### Version-scoped configuration

`schema_version` is a root-level manifest field, separate from the immutable
`rle.version`. It is required when `[model]`, `[train]`, or `[rollout]` is present. The current
schema is `1.0.0`; set it explicitly when authoring a manifest.
Identity-only legacy manifests remain valid for registration, but `train` requires
the complete training configuration below.

```toml
schema_version = "1.0.0"

[rle]
name = "mycoderle"
version = "1.0.0"

[harness]
type = "gym"

[model]
name = "Qwen/Qwen3-32B"
renderer_name = "qwen3_ext"

[train.grpo]
learning_rate = 2e-5
group_size = 8
groups_per_batch = 128
lora_rank = 32
num_epochs = 1
eval_every = 20
save_every = 20
remove_constant_reward_groups = false
min_trainable_group_members = 2
```

Registration can carry partial configuration. `train` instead requires explicit
`schema_version`, a valid `[harness]`, `[model].renderer_name`, and all nine
`[train.grpo]` fields shown above. It never fills missing values from registered
environments, recipe classes, or service defaults. `max_steps` and `sampling_seed`
are optional and remain absent when not supplied.

`learning_rate` must be finite and positive. `group_size`, `groups_per_batch`,
`lora_rank`, `num_epochs`, and optional `max_steps` must be positive integers.
`min_trainable_group_members` must be between 1 and `group_size`. `eval_every`,
`save_every`, and optional `sampling_seed` accept zero and must be nonnegative
integers. Integers must fit the service's signed 32-bit contract. An explicit
`remove_constant_reward_groups = false` is preserved, not treated as missing.

`publish` sends the manifest's `harness`, `schema_version`, `model`, `train`,
and `rollout` settings as registration configuration. Training submission is
separate: it resolves the user's current local manifest without reconciling
registered settings. Datasets, model selection, suffix, and job identity belong
to the training invocation.

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

## Prepare an existing RLE project

Use an existing environment implementation and add a valid `rle.toml` using the
[manifest contract](#manifest-contract) above. The extension does not scaffold
projects, download samples, or install agent skills. Existing projects and
previously installed skills are left unchanged.

Run local and publishing commands from the folder containing `rle.toml`.
For local Gym/OpenEnv execution, include the environment's Dockerfile at the
source root or under `server/`, or select it with `--dockerfile`.
For BYOH, deploy your harness separately and set `[harness].endpoint` to its
HTTPS endpoint.

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

The host port defaults to 8000; explicitly supplied ports must be between 1 and
65535. Local execution uses `AZD_CONTAINER_RUNTIME` (Docker by default), including
native Podman when selected. If the port cannot be bound, the error distinguishes
an occupied port from other bind failures.

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

Use `azd ai rle publish --remote` to build and push images with ACR Tasks instead
of a local container engine. See [remote builds](docs/remote-builds.md) for
permissions, costs, Harbor task contexts, caching, and recovery.

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

The published request includes the `harness` object (`type`, plus `endpoint` and
`protocol` for byoh), schema version, and defaults from the manifest.
The success message identifies the release by name and version, for example
`Published environment 'math_rl' version 1.0.0`. The JSON details printed
afterward still include `environmentId`.

## Inspect and run releases

List the model identifiers and renderers supported by RLE training and Loom-backed rollouts:

```powershell
azd ai rle models
azd ai rle models --output json
```

Use the listed publisher-qualified model value consistently for `--model` and
`[model].name` in `rle.toml`. Use its renderer for `[model].renderer_name` or the
rollout `--renderer` override.

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

The list table displays `NAME`, `VERSION`, `TYPE`, `PROTOCOL`, `DISK IMAGE`,
and `UPDATED`; show displays the same columns without `NAME`. Missing type,
protocol, or disk image conversion status is shown as `-`, not inferred as
ready. Use `--output json` with either command for full details, including
environment IDs, images, and defaults.
Task conversion diagnostics in `show` tables have terminal control characters
replaced with spaces. JSON output preserves the original service diagnostics.

Execute one Loom-backed rollout of the manifest's exact `(name, version)`
identity, using the model declared in `[model]`:

```powershell
azd ai rle rollout --task '{"...": "..."}'
```

When run from a local Gym/OpenEnv recipe, simple reset payloads such as
`{"seed": 32, "split": "train"}` also work in Windows PowerShell versions that
remove embedded JSON quotes from native-command arguments.

`--model` is only required when rle.toml has no `model.name` set.

```powershell
azd ai rle rollout --model Qwen/Qwen3-32B --task-file task.json
```

`rollout` provisions everything a rollout needs and tears it down again: it
creates a real Loom training session for the model (from `--model`, falling
back to rle.toml's `model.name`), saves a sampler checkpoint, calls
RLE's Execute Rollout API with your `--task`, prints the resulting reward and
trajectory summary, then closes the Loom session — you never handle Loom
session or checkpoint identifiers directly. Use `--task`/`--task-file` for the
sandbox reset payload; it is the only input a rollout takes. For Harness
targets, RLE derives the agent-visible input server-side from the reset
observation — there is no caller-supplied agent input to set.
`--rollout-id` (default: a generated GUID) and `--sequence-id` (default `0`, only
meaningful when correlating a rollout to a specific training step in a real
training loop) rarely need to be set for ad hoc testing.

If Loom startup polling fails or is canceled after a session is allocated, the extension
attempts to close it with an independent 30-second timeout. If cleanup also fails, the
error includes the session ID and warns that its resources may remain allocated.
The same cleanup timeout applies after execution. If closing the Loom session
fails, the command returns an error even when execution completed, names the
session whose resources may remain allocated, and does not open the dashboard.
Previously reported execution errors are preserved alongside cleanup failures.
When execution and cleanup both fail, a separate stderr warning identifies the
session whose resources may remain allocated, even if azd displays only the
primary structured execution error.

`--mode train` (the default) always uses Loom, since only Loom returns the
token ids and logprobs training needs, and rejects `--loom`. `--mode eval`
instead samples a model deployment in the Foundry project: `--model` names the
deployment, for example `--model gpt-5-mini`, and no Loom session or checkpoint
is created. Foundry rollouts are text-level: they carry no token ids and are
never trainable. Add `--loom` to run the eval on a Loom session instead.
`--sequence-id` and `--renderer` configure Loom, so they are rejected for a Foundry eval.

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

### Harbor tasks

Run a [Harbor](https://github.com/laude-institute/harbor) task with a managed agent by declaring
it in `rle.toml`, publishing the task directory once per environment version, then rolling it out
by name (`--task` or `--task-file` with a JSON object holding the task `name`):

```bash
azd ai rle publish --task-dir ./hello-alpine
azd ai rle rollout --task '{"name":"hello-alpine"}'
```

Publish builds and pushes the task image to ACR and uploads its bundle; RLE converts the image to
a disk image. The rollout request carries only the task name, and RLE runs `harbor run` in an ACA sandbox and
returns the reward and trajectory. Harbor tasks support only managed agents (`terminus-2`,
the default, or `opencode`); BYOH and Hosted Agent harnesses are rejected.

Bundle uploads require HTTPS URLs without userinfo credentials and do not follow redirects.
Upload errors do not include the signed storage URL.

In `rle.toml`:

```toml
[task]
format = "harbor"

[harness]
type = "package"
name = "opencode"
```

`[task]` is optional and `format` is required only for Harbor. `--task` or `--task-file` is required when `format = "harbor"` and must hold `{"name": "<published task>"}`.

### Rollout artifacts

The Execute Rollout response carries the whole Capture Proxy graph — token ids,
logprobs, loss masks and per-turn metadata — which is far too large to print.
RLE stores rollout results and graphs on the backend; service-backed
[job monitoring](#real-service-monitoring-default-endpoint) fetches them on demand
when available. Closing a capture session does not mean its persisted result is
deleted. Local artifacts are an export for offline viewing and processing, not
the only stored copy. Each rollout writes its export to `.output/<rollout-id>/`
and prints what it wrote:

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
  --model Qwen/Qwen3-32B `
  --training-file .\training.jsonl
```

`FOUNDRY_PROJECT_ENDPOINT` identifies the project that owns the named RLE. The
fine-tuning endpoint for `train`, `jobs`, and `jobs cancel` is derived from the same
account as `https://<account>.openai.azure.com`; there is no separate endpoint override.
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

### Cancel a training job

Cancel the high-level training job with the job ID returned by `train` or `jobs`:

```powershell
azd ai rle jobs cancel ftjob-7898670d3ae8453e89e591f9
```

Like `train`, `jobs cancel` is experimental and hidden from help unless
`AZD_AI_RLE_ENABLE_ALL=true`. Sign-in and `FOUNDRY_PROJECT_ENDPOINT` are required.
No local manifest, dataset, log mirror, or running monitor is needed.

Cancellation uses the [Foundry Azure OpenAI fine-tuning cancel API](https://learn.microsoft.com/rest/api/microsoft-foundry/azureopenai/fine-tuning#cancel-fine-tuning-job):
`POST /openai/v1/fine_tuning/jobs/<job-id>/cancel`. It does not cancel an
individual RLE rollout or Loom session. Use the same endpoint that submitted
the job, derived from the Azure OpenAI account associated with the Foundry project.

The command prints the returned job status and exits without polling or opening
a browser. A successful request does not imply the job has reached `cancelled`
unless that is the returned status. Use `--output json` for the returned job
object. Service failures return an error and include a request ID when available.
The command does not delete local artifacts or stop an existing monitor.
Ctrl+C in `train` or `monitor` still stops local monitoring, not the remote job.

### Real-service monitoring (default endpoint)

**Train opens the job monitor after submission by default**, even with redirected output or `--no-prompt`;
pass `--no-follow` to skip it and exit immediately instead. Keep the process
running to use the UI; Ctrl+C stops monitoring, not training. Explicit `--output` is rejected on
this path rather than silently ignored.

The remote training job initiates rollouts. The CLI does not open a separate
artifact stream or mirror training artifacts to disk. When local monitoring is
stopped, the CLI confirms that it did not cancel the remote job; that job may
continue or reach a terminal state independently.

If the monitor cannot start, the command reports that the job was already
submitted and local monitoring failed. Use the printed monitor command to
reopen it rather than rerunning `train`, which would submit a new job.

Reopen the same monitor from any machine with project access:

```powershell
azd ai rle monitor --job-id ftjob-7898670d3ae8453e89e591f9
```

Both commands read the same project-scoped APIs, with no local run mirror.
Job status comes directly from the fine-tuning API through the Foundry project URL;
configuration, metrics, and rollouts come from the RLE APIs.
The Summary shows registered config, available metric charts, and lightweight
rollout summaries. Full rollout results/graphs are fetched **only on click**.
An unregistered job shows a waiting state; unavailable/expired results leave
metadata visible. The producer must register the same job ID, publish complete
metric rows with a nonnegative integer `step` (`step_id` alone is not sufficient),
and associate rollouts with the job. `sequence_id` is optional; rollouts are listed in
creation order. Fine-tuning retries retain the same job ID, so when rollout summaries report
multiple `session_id` values the UI separates them into distinct training attempts. Session-based
labels remain accurate when the monitor's bounded history no longer includes the initial attempt.

Opening any rollout also reads its persisted progress events from
the RLE backend. The **Rollout events** section lists each event as a message with
a chevron; select the message to expand its full payload, including sequence,
elapsed time, phase, and status. Events are available even when no result or graph
is available. Events are ordered oldest first and loaded in pages of 100; use
**Load more events** for the next page, or the existing **Refresh rollout** button
at the top of the page to reread the rollout and its events. Events are fetched only
when the rollout is opened or explicitly requested; they are not polled in the
background. Event read failures leave the rollout metadata and previously loaded
events visible and offer a retry.
Progress persistence is best-effort: an empty history or sequence gaps do not imply
that no execution took place. Saved local rollout monitoring is unchanged.

The CLI caches data in memory: the latest 5,000 metric rows (up to 32 MiB) and the
latest 10,000 rollout summaries. The UI identifies truncated history. Restarting reloads persisted
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
| Fine-tuning job status | 1 minute until terminal |
| Full rollout result | On click only |
| Rollout events (all statuses) | On open or Refresh rollout; additional pages on Load more events (no automatic polling) |

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
Submission still uses the fine-tuning service. Status polling uses
`GET <Foundry project URL>/openai/v1/fine_tuning/jobs/{jobId}` with Foundry
authentication, without an `api-version` query parameter or direct fine-tuning
project headers. The response's top-level `id` must match the requested job,
and its non-empty top-level `status` drives polling, even before RLE job registration.
Registration/config, metrics, and rollout reads still use the Foundry project's
RLE service with `api-version=2025-11-15-preview`.
Only the new RLE job envelope is supported. The dashboard reads registered configuration
directly from the `metadata` object, not from `metadata.configuration`.
Invalid identities, missing/blank status, and missing/non-object metadata are
reported as read errors. Legacy `job_id` responses and root-level configuration
are not supported. Neither the RLE job's status nor `metadata.status` drives status polling.
Only `succeeded`, `failed`, and `cancelled` are treated as terminal job statuses,
matching the fine-tuning API v1 contract.

### Training settings in `rle.toml`

Every `train` invocation validates the current folder's `rle.toml` before
authentication, file uploads, or job submission. Missing GRPO settings are
reported together, with the actual `[train.grpo]` field names and instructions
to supply them locally. Unknown TOML keys, invalid bounds, and incompatible
harness fields are rejected locally as well.

Use the complete [version-scoped configuration](#version-scoped-configuration)
example above. `--model` and `--training-file` are required invocation settings;
`--validation-file` and `--suffix` are optional. These flags are not read from
`[train]`. `--model` selects the FT request's top-level training model even when
`[model].name` declares a different registration or ad hoc rollout model.
`[model].renderer_name` must explicitly identify the renderer for the selected
training model; there is no renderer fallback.

The FT request has `method.type = "rl_environment"` and
`method.rl_environment` containing `name`, `version`, `harness`,
`schema_version`, `model: { renderer_name: ... }`, `train: { grpo: ... }`,
and `rollout`. It does not contain a duplicate model name, registration-only
`imageTag`, flattened `hyperparameters`, or `max_episode_steps`.

`rollout` is a sibling of `train`, never nested in it. Omitting `[rollout]`, or
declaring an empty `[rollout]`, sends the valid empty object `{}`. A supplied
`[rollout.gym_openenv]` requires a gym harness and a non-empty
`model_response_field`; its complete supported subtree is preserved:

```toml
[rollout.gym_openenv]
model_response_field = "response"
```

This is a CLI-to-FT snapshot, not a new Execute Rollout request override.
The upstream Execute Rollout API has no per-request rollout override; non-MCP
Gym execution still uses the registered `model_response_field`.

## List RLE-backed fine-tuning jobs (experimental)

`azd ai rle jobs` lists all visible fine-tuning jobs that use the `rl_environment`
method, including active and terminal jobs. No `list` subcommand is required.
Use `azd ai rle jobs cancel <job-id>` to request cancellation of one job.
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
verifies that filter before it renders results. Both table and JSON output use
the fine-tuning API's status directly, not the RLE job API's status.

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
