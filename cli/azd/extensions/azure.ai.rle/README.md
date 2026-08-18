# Azure AI RLE extension for azd

Quickstart for the `azd ai rle` preview extension. The extension manages an OpenEnv-style RLE environment lifecycle: init, build and run the environment container, test it through a playground UI or shell, and publish the environment image to the RLE control plane through your Foundry project endpoint.

## Prerequisites

Install:

- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Azure CLI (`az`): https://learn.microsoft.com/cli/azure/install-azure-cli
- Docker Desktop: https://www.docker.com/products/docker-desktop/
- Go, if building from source: https://go.dev/doc/install
- Git, if building from source: https://git-scm.com/downloads

Verify:

```powershell
azd version
docker version
az version
```

Sign in with Azure CLI before calling the Foundry project APIs:

```powershell
az login
```

The extension also supports credentials from `azd auth login` and the other development credentials in Azure's default credential chain.

## Install the extension from the nightly registry

```powershell
azd ext install azure.ai.rle -s https://aka.ms/azd/extensions/registry/nightly
```

Verify:

```powershell
azd ai rle --help
azd ai rle version
```

`version` is always available. The lifecycle commands are preview-gated; if commands such as `init`, `run`, `publish`, `list`, `show`, or `invoke` are hidden, enable the preview flag in your terminal:

```powershell
$env:AZD_AI_RLE_ENABLE = "true"
```

Remote invocation accepts HTTPS OpenEnv URLs on the configured Foundry project origin or under the RLE-managed `hyena.infra.ai.azure.com` domain. Other origins, embedded credentials, insecure HTTP URLs, and custom ports are rejected.

## Configure the Foundry project endpoint

RLE control-plane APIs are called relative to the Foundry project endpoint. APIM maps the project endpoint request to the workspace-scoped RLE service internally, so the extension does not require a separate control-plane endpoint.

Set the Foundry project endpoint once in the terminal where you run `publish`:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"
```

For example, RLE environment registration is sent to:

```text
<FOUNDRY_PROJECT_ENDPOINT>/rl_environments?api-version=2025-11-15-preview
```

Publish also needs an ACR registry endpoint:

```powershell
$env:AZURE_CONTAINER_REGISTRY_ENDPOINT = "<registry>.azurecr.io"
```

Authenticate Docker to ACR before deploying:

```powershell
az acr login --name <registry>
```

## Quickstart

### 1. Initialize an environment session

Default echo session:

```powershell
azd ai rle init
cd .\echo_env
```

The default echo session downloads the Hugging Face `OpenEnv` repo, copies `envs/echo_env` into the session folder, and writes `.azd-rle.json` with the local `environmentName`. Existing state files that use the legacy `name` property remain supported.

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

`run` builds a local Docker image from the current source folder, removes any stale local container for the same environment name, starts a fresh container, waits for `/health`, opens the playground UI at `/web`, and keeps an OpenEnv shell attached. When the shell exits or Ctrl+C is received, `run` removes the local container.

If `.azd-rle.json` does not exist, `run` creates it with only the inferred local environment name.

Use a custom host port:

```powershell
azd ai rle run --port 9000
```

`run` looks for `Dockerfile` at the source root, then `server\Dockerfile`. If the Dockerfile is elsewhere, pass it explicitly:

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

### 3. Publish/register

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"
$env:AZURE_CONTAINER_REGISTRY_ENDPOINT = "<registry>.azurecr.io"
azd ai rle publish --version-bump major
```

Publish reads the Foundry project endpoint from `FOUNDRY_PROJECT_ENDPOINT` and the ACR registry from `AZURE_CONTAINER_REGISTRY_ENDPOINT`. It derives the project route segment from `/api/projects/<project>`, builds the Docker image as `<registry>.azurecr.io/<project>-<environment>:latest`, pushes it to ACR, registers that image by calling `<FOUNDRY_PROJECT_ENDPOINT>/rl_environments`, and saves the project/environment details in `.azd-rle.json`.

Use `--version-bump major` (default), `--version-bump minor`, or `--version-bump patch` to control the environment version that RLE creates.

The publish command prints a CLI-friendly summary using `environmentId`, `foundryProjectEndpoint`, `acrImage`, `environmentVersion`, `createdAt`, and `updatedAt`.

If needed, override the Dockerfile path the same way as local run:

```powershell
azd ai rle publish --dockerfile server\Dockerfile
```

### 4. List deployed environments

List all RLE environments in the configured Foundry project:

```powershell
azd ai rle list
```

The command uses `FOUNDRY_PROJECT_ENDPOINT` when it is set. Otherwise, it uses the project endpoint saved in the current folder's `.azd-rle.json`. Use JSON output for scripting:

```powershell
azd ai rle list --output json
```

### 5. Show environment details

Show the full details for a specific environment, including version history:

```powershell
azd ai rle show code_rl
```

When run from a published environment folder, the environment name and Foundry
project endpoint can come from `.azd-rle.json`. Environment details and version
history are still retrieved from the Foundry APIs:

```powershell
azd ai rle show
```

### 6. Invoke remotely

Remote invoke creates a temporary instance group and one instance through the public RLE routes:

- Named invoke without `--version`: create the group at `/rl_environments/<environmentName>/instance_groups`; RLE resolves the latest version.
- Saved state or explicit `--version`: create the group at `/rl_environments/<environmentName>/versions/<version>/instance_groups`.
- Create and poll the instance under the resolved version at `/instance_groups/<groupId>/instances`.

It opens a generic local playground that proxies authenticated OpenEnv operations to the environment and keeps the shell attached. When invoke exits, it deletes the temporary instance and then its group using cleanup contexts that are independent from Ctrl+C:

```powershell
azd ai rle invoke --timeout 60
```

To invoke an existing environment without its source code or `.azd-rle.json`, set the Foundry project endpoint and provide the environment name:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<account>.services.ai.azure.com/api/projects/<project>"
azd ai rle invoke code_rl
```

The unversioned instance-group response supplies the resolved latest version used for all subsequent instance requests. Pin a specific published version when needed:

```powershell
azd ai rle invoke code_rl --version 2.1.0
```

Cloud-only invocation does not create or modify `.azd-rle.json`. If the selected disk image is not ready, group creation fails before an instance is created.

## Build and install from source

Use this path only when you are developing the extension itself. From `cli\azd\extensions\azure.ai.rle`:

```powershell
azd extension install microsoft.azd.extensions
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```

After code changes, rerun:

```powershell
azd x build
azd x pack
azd x publish
azd extension install azure.ai.rle --source local --force
```
