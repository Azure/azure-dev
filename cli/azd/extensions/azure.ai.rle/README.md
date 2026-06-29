# Azure AI RLE extension for azd
<!-- cspell:ignore openenv devrle -->

Quickstart for the `azd ai rle` preview extension. The extension manages an OpenEnv-style RLE environment lifecycle: initialize a local session, run the environment container locally, invoke it through a shell, and register it with the RLE control plane.

## Prerequisites

Install:

- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Azure CLI (`az`): https://learn.microsoft.com/cli/azure/install-azure-cli
- Docker Desktop: https://www.docker.com/products/docker-desktop/
- Go, if installing from this local source checkout: https://go.dev/doc/install
- Git, if installing from this local source checkout: https://git-scm.com/downloads

Verify:

```powershell
azd version
az version
docker version
go version
git --version
```

Sign in before pulling private ACR images or deploying:

```powershell
az login
```

If the local or environment image is in a private ACR, also sign in to that registry:

```powershell
az acr login --name <acr-name>
```

## Install the extension from this checkout

From `cli\azd\extensions\azure.ai.rle`:

```powershell
azd extension install microsoft.azd.extensions
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

Verify:

```powershell
azd ai rle --help
azd ai rle version
```

After code changes, rerun:

```powershell
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

## Configure the RLE control plane

The extension defaults to `http://localhost:5000`. To target another control plane:

```powershell
$env:RLE_ENDPOINT = "https://<rle-control-plane>"
```

## Quickstart

### 1. Initialize an environment session

Default echo session:

```powershell
azd ai rle init
cd .\echo_env
```

The default echo session is driven by an embedded manifest. The generated `rle.yaml` includes:

```yaml
name: echo_env
template:
  name: echo_env
  kind: openenv
  environment:
    image: devrle.azurecr.io/echo-rl:latest
```

To initialize from an existing manifest instead:

```powershell
azd ai rle init -m .\rle.yaml
```

`-m` accepts a local path or HTTPS/GitHub blob URL. The session folder name is always inferred from `name` or `template.name` in the manifest.

### 2. Run locally

```powershell
azd ai rle run
```

`run` starts a Docker container and waits for `/health`. It uses `template.local.image` when present; otherwise it uses `template.environment.image`. The command does not build an image locally.

Use a custom host port:

```powershell
azd ai rle run --port 9000
```

The selected port is saved in `.azd-rle.json` and reused by local invoke.

### 3. Invoke locally

```powershell
azd ai rle invoke --local
```

Inside the shell:

```text
rle> health
rle> reset {"seed":0}
rle> step {"message":"hello"}
rle> state
rle> exit
```

Supported shell commands:

| Command | Calls |
|---|---|
| `health` | `GET /health` |
| `reset [json]` | `POST /reset` |
| `step <json-action>` | `POST /step` with `{ "action": <json-action> }` |
| `state` | `GET /state` |
| `metadata` | `GET /metadata` |
| `schema` | `GET /schema` |
| `exit` / `quit` | Exit shell |

Per-request timeout:

```powershell
azd ai rle invoke --local --timeout 60
```

### 4. Deploy/register

```powershell
azd ai rle deploy --project-id <project-id>
```

Deploy registers `template.environment.image` from `rle.yaml` with the RLE control plane and saves the project/environment details in `.azd-rle.json`.

## Manifest shape

```yaml
name: code_rl
description: Code RL OpenEnv environment.
template:
  name: code_rl
  kind: openenv
  local:
    image: devrle.azurecr.io/code-rl:latest
  environment:
    image: devrle.azurecr.io/code-rl:prod
```

`template.local.image` is optional. If omitted, local run/invoke use `template.environment.image`.

## Current limitations

- `run` is local only and requires Docker.
- `invoke` supports local shell mode only (`--local`).
- `deploy` requires `--project-id`; project selection/creation is not part of `init`.
- The extension does not build or push images. Images must already exist and be accessible.
