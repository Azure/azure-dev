# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Local setup

### 1. Check out the branch

```powershell
git fetch origin
git checkout farhannawaz/rle-cli
cd cli\azd\extensions\azure.ai.rle
```

### 2. Start the local RLE control plane

Follow the existing [RLE service setup](https://msdata.visualstudio.com/Vienna/_git/vienna?path=/src/azureml-api/src/RLE).

The examples below assume the RLE control plane is running at:

```text
http://localhost:5000
```

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

### 4. Create an RLE environment

```powershell
$env:AZD_RLE_CONTROL_PLANE = "http://localhost:5000"
$project = "demo"
$image = "devrle.azurecr.io/coding_env:latest"
$environmentName = "coding-env-e2e"

azd ai rle create $environmentName `
  --project $project `
  --image $image
```

Copy the generated environment id from the output.

You can list or show environments with:

```powershell
azd ai rle list --project $project
azd ai rle show <environment-id> --project $project
azd ai rle versions <environment-id> --project $project
```

### 5. Create a sandbox

Disk image conversion starts automatically after environment creation. If sandbox creation returns `conversion status: Pending`, wait and retry the same command.

```powershell
azd ai rle sandbox create <environment-id> --project $project
```

After the sandbox is created, inspect it with:

```powershell
azd ai rle sandbox list <environment-id> --project $project
azd ai rle sandbox show <environment-id> <sandbox-id> --project $project
```
