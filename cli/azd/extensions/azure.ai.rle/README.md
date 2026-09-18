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
- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Git](https://git-scm.com/downloads), used by `init` to download samples

Sign in before calling Foundry project APIs:

```powershell
az login
```

The extension also supports `azd auth login` and other development credentials
from Azure's default credential chain.

Install from the nightly registry:

```powershell
azd ext install azure.ai.rle -s https://aka.ms/azd/extensions/registry/nightly
```

The lifecycle commands are preview-gated:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
```

Harness scaffolds, samples hidden from the default catalog, and other
internal-only surfaces are gated by a single flag for the RLE team's own
iteration:

```powershell
$env:AZD_AI_RLE_ENABLE_ALL = "true"
```


`azd ai rle init --type Harness` scaffolds only the RLE-side container; the
harness/agent side is always specific to your own agent. For a complete,
runnable pattern of both halves wired together (including a Dockerfile,
mock tools, a grader, and the deploy → wire → register → publish flow), see
[`examples/harness/hosted-agent`](https://github.com/sujit-kamireddy/rle-samples/tree/main/examples/harness/hosted-agent)
and
[`examples/harness/byoh`](https://github.com/sujit-kamireddy/rle-samples/tree/main/examples/harness/byoh)
in the [`rle-samples`](https://github.com/sujit-kamireddy/rle-samples) repository.

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
only the selected sample, including its manifest. When a target folder is
specified, `init` updates `rle.name` to match that folder:

```powershell
azd ai rle init my_environment
```

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
values explicitly when scripting:

```powershell
azd ai rle init support_rle `
  --type Harness --subtype HostedAgent `
  --rle-version 1.0.0 `
  --agent-name support-agent --agent-version 12 `
  --no-prompt

azd ai rle init customer_rle `
  --type Harness --subtype BYOH `
  --rle-version 1.0.0 `
  --base-url https://harness.example.com/rle/ `
  --no-prompt
```

Harness scaffolds contain:

```text
<environment-name>/
|-- rle.toml
|-- Dockerfile
`-- server/
    |-- __init__.py
    `-- env.py
```

`server/env.py` uses OpenEnv's supported app factory and exposes `/health`,
`/schema`, `/metadata`, `/ws`, `/web`, `/reset`, `/step`, `/grade`, and a
starter mock-tool route. Update its task setup, mocks, and grader before
publishing.

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

## Inspect and invoke releases

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

Invoke the manifest's exact `(name, version)` identity:

```powershell
azd ai rle invoke --timeout 60
```

To invoke source-free from another folder, provide both parts of the identity:

```powershell
azd ai rle invoke code_rl --version 1.0.0
```

Remote invocation creates a temporary instance group and instance through the
RLE public routes, waits for the runtime to become healthy, opens a local
authenticated playground, and removes the temporary resources when the shell
exits.

## Submit an RLE-backed fine-tuning job (experimental)

`azd ai rle train` submits a reinforcement fine-tuning job to a fine-tuning
resource's public `/openai/v1/fine_tuning/jobs` API, using the `rl_environment`
method: a published RLE environment supplies the reward signal instead of a
grader, so no training file is required. `rl_environment` is currently hidden
from finetunesapi's public API surface and only completes for base models
the service has enabled for Loom-backed RL-environment training; job
creation fails for other models, or if the named RLE version is not
published and ready in the Foundry project set by `FOUNDRY_PROJECT_ENDPOINT`.

This command is gated behind `AZD_AI_RLE_ENABLE_ALL` in addition to
`AZD_AI_RLE_ENABLE`, since it targets an unreleased method and the CLI shape
is still subject to change:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
$env:AZD_AI_RLE_ENABLE_ALL = "true"
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"
$env:AZD_AI_RLE_TRAIN_ENDPOINT = "https://<resource>.openai.azure.com"

azd ai rle train `
  --rle-name code_rl --rle-version 1.0.0 `
  --model Qwen/Qwen3-32B
```

`FOUNDRY_PROJECT_ENDPOINT` identifies the project that owns the named RLE.
`--training-file` and `--validation-file` are optional and only meaningful
for methods other than `rl_environment`; the RLE itself supplies the reward
signal. Use `--endpoint` instead of `AZD_AI_RLE_TRAIN_ENDPOINT` to target a
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
