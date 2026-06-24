# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Local setup

### 1. Install prerequisites

Install:

- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Go: https://go.dev/doc/install
- Git, for local development and for fetching the managed Loom recipe: https://git-scm.com/downloads
- Python: https://www.python.org/downloads/
- uv, for running the Loom training recipe: https://docs.astral.sh/uv/getting-started/installation/

Verify:

```powershell
azd version
go version
git --version
python --version
```

`az login` is not required for the current `init`/`deploy` flow because deploy calls the RLE control plane directly.

### 2. Check out the branch

```powershell
git fetch origin
git checkout farhannawaz/rle-cli
cd cli\azd\extensions\azure.ai.rle
```

### 3. Configure the RLE control plane

```powershell
$env:RLE_ENDPOINT = "http://localhost:5000"
$env:RLE_ACR_IMAGE = "devrle.azurecr.io/coding_env:latest"
```

`http://localhost:5000` is also the built-in default, so you can omit `RLE_ENDPOINT` when using a local RLE control plane.
`RLE_ACR_IMAGE` is required by deploy and is expanded from the generated `rle.yaml`.

For `invoke`, provide the Azure AI project endpoint as a parameter:

```powershell
az login
```

### 4. Install the extension into azd

Run these commands from the extension directory:

```powershell
azd extension install microsoft.azd.extensions
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

`azd x build` builds the extension artifacts. `azd x pack` and `azd x publish`
register them in the local azd extension source, and `azd extension install`
makes the `azd ai rle` command group available in any terminal.

Verify:

```powershell
azd ai rle --help
azd ai rle version
```

After making local code changes, rebuild and update the installed extension with:

```powershell
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

### 5. Initialize a local RLE session

```powershell
azd ai rle init code_rl
```

Init creates a local session folder named `code_rl`, including an OpenEnv-style FastAPI package, `Dockerfile`, `rle.yaml`, and azd-managed dependencies under `.azd-rle\deps`.

Deploy from the session folder:

```powershell
cd .\code_rl
azd ai rle deploy --project omi-build-demo-uae
```

Deploy creates or updates the RLE environment and saves the project plus environment id/version locally in `.azd-rle.json`.

### 6. Run Loom training

```powershell
azd ai rle invoke `
  --recipe code_rl_with_rle `
  --project-endpoint "https://omi-build-demo-uae.services.ai.azure.com/api/projects/omi-build-demo-uae"
```

Invoke runs the selected Loom recipe's `train_azure.py` entrypoint with values from `.azd-rle.json`.
It passes the deployed RLE environment id, project, and control-plane endpoint to the Loom recipe,
which uses `rle_sdk` to lease sandboxes and call `reset`/`step` during training.

Invoke fetches Loom branch `code_rl_with_rle` into `.azd-rle\recipes\loom`
and uses the RLE SDK wheel copied by `init`. You do not need a separate local Loom checkout
or a separately installed RLE SDK package. The managed Git checkout is shallow, single-branch,
and tagless (`--depth 1 --single-branch --no-tags`). In the future this recipe dependency can
move from Git to a published package.

After fetching Loom, invoke patches only the managed copy of `loom-cookbook\pyproject.toml`
so `uv` resolves `azure-ai-finetuning-sessions` from the fetched Loom checkout and `rle-sdk`
from `.azd-rle\deps`.

The default invoke settings are:

```text
num_tasks=4
model_name=Qwen/Qwen3-32B
renderer_name=qwen3_disable_thinking
max_tokens=1200
lora_rank=32
group_size=4
groups_per_batch=1
max_steps=1
loss_fn=importance_sampling
seed=42
eval_every=999999
save_every=999999
remove_constant_reward_groups=true
```

Override the recipe, task count, or model with flags, for example:

```powershell
azd ai rle invoke `
  --recipe code_rl_with_rle `
  --project-endpoint "https://omi-build-demo-uae.services.ai.azure.com/api/projects/omi-build-demo-uae" `
  --num-tasks 4 `
  --model-name "Qwen/Qwen3-32B"
```
