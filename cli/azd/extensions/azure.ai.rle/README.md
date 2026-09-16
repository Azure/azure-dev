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

Harness scaffolds are separately preview-gated:

```powershell
$env:AZD_AI_RLE_HARNESS_INIT_ENABLE = "true"
```

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
[rle]
schema_version = "1.0.0"
name = "code_rl"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"
```

A Foundry Hosted Agent-backed harness:

```toml
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

### Version-scoped defaults and metadata

`rle.schema_version` is required when reusable `defaults` or `metadata` is
present. The current schema is `1.0.0`; `init` writes it into newly generated
manifests. Identity-only legacy manifests without defaults or metadata remain
valid.

```toml
[rle]
schema_version = "1.0.0"
name = "mycoderle"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"

[defaults.model]
name = "Qwen/Qwen3-32B"
renderer_name = "qwen3_disable_thinking"

[defaults]
seed = 17

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

[defaults.loom]
checkpoint_id = ""
lora_rank = 32
sampler = "default"

[metadata]
owner = "rle-platform"
```

All defaults are optional. The CLI normalizes surrounding whitespace, requires
positive finite numeric defaults, and limits `reasoning_effort` to `low`,
`medium`, or `high`. `seed` may be any integer. Metadata is limited to 64
trimmed, non-empty string pairs; keys are at most 128 characters and values
are at most 1,024 characters. Do not put credentials, tokens, connection
strings, or secret endpoints in metadata.

The CLI sends the manifest as the RLE create payload's `version`,
`schemaVersion`, `defaults`, and `metadata` fields. The RLE version owns
reusable defaults; training and evaluation jobs own datasets, job identity,
result locations, credentials, graders, response format, tools, and explicit
per-job overrides. Precedence is:

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

For noninteractive sample initialization, the positional name selects the
sample and target folder:

```powershell
azd ai rle init code_rl --no-prompt
```

With `AZD_AI_RLE_HARNESS_INIT_ENABLE=true`, `init` also offers
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
identity, defaults, and metadata. The pushed ACR image is version-tagged as:

```text
<registry>.azurecr.io/<project>-<environment>:<rle.version>
```

The published request includes `type`, `subtype`, the applicable HostedAgent
or BYOH configuration, schema version, defaults, and metadata from the
manifest.

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

## Build and install from source

From `cli\azd\extensions\azure.ai.rle`:

```powershell
azd extension install microsoft.azd.extensions
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```
