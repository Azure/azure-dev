# Azure AI RLE extension for azd

Quickstart for the `azd ai rle` preview extension. The extension manages an OpenEnv-style RLE environment lifecycle: init, build and run the environment container, test it through a playground UI or shell, and deploy the environment image to the RLE control plane.

## Prerequisites

Install:

- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Docker Desktop: https://www.docker.com/products/docker-desktop/
- Go, if building from source: https://go.dev/doc/install
- Git, if building from source: https://git-scm.com/downloads

Verify:

```powershell
azd version
docker version
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

The extension defaults to the local RLE control plane at `http://localhost:5000`. To target another control plane:

```powershell
$env:RLE_ENDPOINT = "https://<rle-control-plane>"
```

Deploy uses an ACR image for the registered RLE environment. Set the registry once:

```powershell
$env:AZURE_CONTAINER_REGISTRY_ENDPOINT = "<registry>.azurecr.io"
```

## Quickstart

Discovery for all commands is currently disabled using azd env flag. To enable:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
```

### 1. Initialize an environment session

Default echo session:

```powershell
azd ai rle init
cd .\echo_env
```

The default echo session downloads the Hugging Face `OpenEnv` repo, copies `envs/echo_env` into the session folder,
and writes `.azd-rle.json` with the local environment name.

The copied session does not keep `.git` metadata from the upstream repository.

Name the copied echo session:

```powershell
azd ai rle init code_rl
```

For an existing source folder, skip `init` and run commands directly from that folder.

### 2. Run locally

```powershell
azd ai rle run
```

`run` builds a local Docker image from the current source folder, removes any stale local container for the
same environment name, starts a fresh container, waits for `/health`, opens the playground UI at `/web`, and
keeps an OpenEnv shell attached. When the shell exits or Ctrl+C is received, `run` removes the local container.

If `.azd-rle.json`
does not exist, `run` creates it with only the inferred local environment name.

Use a custom host port:

```powershell
azd ai rle run --port 9000
```

`run` looks for `Dockerfile` at the source root, then `server\Dockerfile`. If the Dockerfile is elsewhere,
pass it explicitly:

```powershell
azd ai rle run --dockerfile server\Dockerfile
```

Rebuild automatically while editing local source:

```powershell
azd ai rle run --watch
```

The shell supports the standard OpenEnv commands:

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

### 3. Deploy/register

```powershell
$env:AZURE_CONTAINER_REGISTRY_ENDPOINT = "<registry>.azurecr.io"
azd ai rle deploy --project-id <project-id>
```

Deploy builds the Docker image as `<registry>.azurecr.io/<project-id>-<environment>:latest`, pushes it to ACR, registers that image with the RLE control plane, and saves the project/environment details in `.azd-rle.json`.
The deploy command prints a CLI-friendly summary using `environmentId`, `acrImage`, `version`, `createdAt`, and `updatedAt`.

If needed, override the Dockerfile path the same way as local run:

```powershell
azd ai rle deploy --project-id <project-id> --dockerfile server\Dockerfile
```

### 4. Invoke remotely

Remote invoke uses the deployed environment, leases a sandbox, opens the sandbox `/web` UI when available
(or a local proxy UI otherwise), keeps the shell attached, and releases the sandbox when the shell exits:

```powershell
azd ai rle invoke --timeout 60
```