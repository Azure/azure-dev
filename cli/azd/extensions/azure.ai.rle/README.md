# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Local setup

### 1. Check out the branch

```powershell
git fetch origin
git checkout farhannawaz/rle-cli
cd cli\azd\extensions\azure.ai.rle
```

### 2. Configure the RLE control plane

```powershell
$env:RLE_ENDPOINT = "https://rle-controlplane.orangeground-ba9696de.eastus2.azurecontainerapps.io"
$env:RLE_PROJECT_NAME = "demo-3"
$env:RLE_ACR_IMAGE = "devrle.azurecr.io/coding_env:latest"
```

To target a local control plane instead, set `RLE_ENDPOINT` to `http://localhost:5000`.

### 3. Install the extension into azd

Run these commands from the extension directory:

```powershell
azd extension install microsoft.azd.extensions

azd x build
azd x pack -o .\registry-artifacts

$registry = Join-Path $env:USERPROFILE ".azd\rle-registry.json"
New-Item -ItemType Directory -Force -Path (Split-Path $registry) | Out-Null
if (-not (Test-Path $registry)) {
  '{"schemaVersion":"1.0","extensions":[]}' | Set-Content -Path $registry -Encoding utf8
}

azd x publish --registry $registry --artifacts ".\registry-artifacts\*.zip,.\registry-artifacts\*.tar.gz"
azd extension source remove rle-local 2>$null
azd extension source add -n rle-local -t file -l $registry
azd extension install azure.ai.rle --source rle-local --force
```

Verify:

```powershell
azd ai rle --help
azd ai rle version
```

### 4. Initialize a local RLE session

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

### 5. Training placeholder

```powershell
azd ai rle invoke
```

This command is a placeholder for now. Later, it will trigger the actual training job for the deployed RLE environment.
