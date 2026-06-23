# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Local setup

### 1. Install prerequisites

Install:

- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Go: https://go.dev/doc/install
- Git: https://git-scm.com/downloads

Verify:

```powershell
azd version
go version
git --version
```

`az login` is not required for the current `init`/`deploy` flow because deploy calls the RLE control plane directly. If the control plane later requires auth, set `RLE_BEARER_TOKEN` before deploy.

### 2. Check out the branch

```powershell
git fetch origin
git checkout farhannawaz/rle-cli
cd cli\azd\extensions\azure.ai.rle
```

### 3. Configure the RLE control plane

```powershell
$env:RLE_ENDPOINT = "https://rle-controlplane.orangeground-ba9696de.eastus2.azurecontainerapps.io"
$env:RLE_PROJECT_NAME = "demo-3"
$env:RLE_ACR_IMAGE = "devrle.azurecr.io/coding_env:latest"
```

To target a local control plane instead, set `RLE_ENDPOINT` to `http://localhost:5000`.

### 4. Install the extension into azd

Run these commands from the extension directory:

```powershell
azd extension install microsoft.azd.extensions
azd x build
```

`azd x build` normally builds and installs the local extension for the current user, so `azd ai rle` should be available immediately after it succeeds.

Verify:

```powershell
azd ai rle --help
azd ai rle version
```

### 5. Initialize a local RLE session

```powershell
azd ai rle init code_rl
```

Init creates a local session folder named `code_rl`, including an OpenEnv-style FastAPI package, `Dockerfile`, and `rle.yaml`.

Deploy from the session folder:

```powershell
cd .\code_rl
azd ai rle deploy
```

Deploy creates or updates the RLE environment and saves the environment id/version locally.

### 6. Training placeholder

```powershell
azd ai rle invoke
```

This command is a placeholder for now. Later, it will trigger the actual training job for the deployed RLE environment.
