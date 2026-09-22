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

Register the RLE development source and install the extension:

```powershell
azd extension source add `
  --name rle-dev `
  --type url `
  --location https://raw.githubusercontent.com/sujit-kamireddy/azure-dev/main/cli/azd/extensions/registry.rle-dev.json

azd extension install azure.ai.rle --source rle-dev
azd ai rle version
```

List registered extension sources at any time with:

```powershell
azd extension source list
```

### Extension updates

The extension checks the RLE development registry when an RLE command runs. A
non-breaking update does not interrupt the command. After the command completes,
the CLI displays:

```text
RLE extension update available: <version>
To update, run `azd extension update azure.ai.rle`
```

If the update path includes a release marked as breaking, lifecycle commands are
blocked until the extension is updated. Commands needed to inspect the extension,
such as `azd ai rle version` and help, remain available.

Update to the latest release from the registered source:

```powershell
azd extension update azure.ai.rle
azd ai rle version
```

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
only the selected sample, including its manifest, plus all project skills under
`.agents/skills`, including the canonical `rle-gym-openenv` authoring skill.
Compatible agents such as GitHub Copilot and OpenAI Codex discover
these project skills automatically and load their guidance when relevant.
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

The command adds or replaces the skills supplied by `rle-samples` under
`.agents/skills` and preserves unrelated project skills.

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

Execute one Loom-backed rollout of the manifest's exact `(name, version)`
identity, using the model declared in `[defaults.model]`:

```powershell
azd ai rle rollout --task '{"...": "..."}'
```

`--model` is only required when rle.toml has no `defaults.model.name` set, or
when running source-free from another folder:

```powershell
azd ai rle rollout code_rl --version 1.0.0 --model Qwen/Qwen3-32B --task-file task.json
```

`rollout` provisions everything a rollout needs and tears it down again: it
creates a real Loom training session for the model (from `--model`, falling
back to rle.toml's `defaults.model.name`), saves a sampler checkpoint, calls
RLE's Execute Rollout API with your `--task` (and, for Harness targets,
`--agent-input`), prints the resulting reward and trajectory summary, then
closes the Loom session — you never handle Loom session or checkpoint
identifiers directly. Use `--task`/`--task-file` for the sandbox reset payload
(Gym/OpenEnv), `--agent-input`/`--agent-input-file` for Harness targets.
`--lora-rank` (default `16`), `--rollout-id` (default: a generated GUID),
`--sequence-id` (default `0`, only meaningful when correlating a rollout to a
specific training step in a real training loop), and `--timeout` (default
`600` seconds) all have sensible defaults and rarely need to be set for ad hoc
testing.

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
successfully, with a warning — the compute is already spent. With `--monitor`,
an artifact-write failure instead returns an error because the dashboard cannot
open. Existing rollout directories are never overwritten; use a new rollout ID
or a different output root. The summary is published last, after the other files.

## Monitor a completed rollout (development only)

Open a local, read-only dashboard to explore a saved rollout's reward,
environment, model-call graph, and token metrics. Enable development mode:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
$env:AZD_AI_RLE_ENABLE_ALL = "true"
```

Run a rollout and open its dashboard when execution finishes:

```powershell
azd ai rle rollout --task-file task.json --monitor
```

Or reopen a saved rollout from the folder where it was executed:

```powershell
azd ai rle monitor --rollout-id 3c27c30f5fba261c3a7a3e856b4e1388
```

The dashboard includes **Rollout graph**, **Tokens and Metrics**, and **Rollout Stats**
tabs, with a light/dark mode toggle. Viewing saved results needs no Azure sign-in
and does not execute another rollout.

Keep the terminal running while using the dashboard. **Ctrl+C** stops the local
monitor without deleting saved artifacts.

| Option | When to use it |
| --- | --- |
| `--output-dir <path>` | Read from an artifact root other than `.output` in the current folder. Pass the parent of the rollout-ID directories, not an individual rollout folder. |
| `--no-browser` | On standalone `monitor`, print a link instead of opening the browser. Open the link and enter the local access code printed in the terminal. |

The automatically opened browser handles the local access code for you.
`--no-prompt` does not disable browser launching or stop the monitor;
`--output` is not supported.

Monitoring reads the existing [rollout artifacts](#rollout-artifacts), not remote
results. If the rollout directory is missing, it warns and exits without opening
a dashboard; check the ID and `--output-dir`. Incomplete or corrupt files return
an error.

**Current limits:** completed local snapshots only—no live updates, job monitoring,
or conversation text. Metrics appear only when included in the saved data.
Execution completion does not imply task success.

**Treat saved artifacts as sensitive:** they may contain customer content.
Keep `.output` out of source control and delete artifacts when no longer needed.

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
fine-tuning endpoint is derived from the same account as
`https://<account>.openai.azure.com`.
`--training-file` is required and must point to a regular local training dataset
file. The extension uploads it to the selected fine-tuning resource with the
`fine-tune` purpose, then uses the returned file ID when it submits the job.
`--validation-file` is optional and follows the same local-file upload flow.
Use `--endpoint` to target a different fine-tuning resource for a single
invocation.

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

## Prepare a development release

The release script currently builds the Windows AMD64 extension artifact. Run it
from `cli\azd\extensions\azure.ai.rle`.

For a normal non-breaking release, choose the semantic-version component to
increment:

```powershell
.\prepare-dev-release.ps1 -VersionBump patch
.\prepare-dev-release.ps1 -VersionBump minor
.\prepare-dev-release.ps1 -VersionBump major
```

The script preserves the existing prerelease suffix. For example,
`0.8.8-preview` becomes `0.8.9-preview` with `-VersionBump patch`. It updates
`version.txt` and `extension.yaml`, cross-compiles the extension for every
supported platform (`windows`, `darwin`, and `linux` on both `amd64` and
`arm64`), writes those artifacts under `artifacts\rle-dev\<version>`, and
updates `registry.rle-dev.json` with each artifact's checksum and GitHub URL.
`azd x pack` archives linux artifacts as `.tar.gz` and every other platform as
`.zip`. The script runs on any host Go can cross-compile from.

Mark a release as breaking only when users must update before continuing:

```powershell
.\prepare-dev-release.ps1 -VersionBump patch -BreakingChanges
```

Non-breaking is the default; do not pass `-BreakingChanges` for a normal release.
Review and commit the two version files, generated artifacts, and registry change
together. The registry URLs target `main`, so the release becomes installable
after those files are merged.
