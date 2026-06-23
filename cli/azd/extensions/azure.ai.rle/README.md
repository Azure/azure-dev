# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Commands

```bash
azd ai rle create <name> [--endpoint http://localhost:5000] [--account local] [--project demo]
azd ai rle list [--endpoint http://localhost:5000] [--account local] [--project demo]
azd ai rle show <environment-id> [--endpoint http://localhost:5000] [--account local] [--project demo]
azd ai rle versions <environment-id> [--endpoint http://localhost:5000] [--account local] [--project demo]
azd ai rle sandbox create <environment-id> [--endpoint http://localhost:5000] [--project demo]
azd ai rle sandbox list <environment-id> [--endpoint http://localhost:5000] [--project demo]
azd ai rle sandbox show <environment-id> <sandbox-id> [--endpoint http://localhost:5000] [--project demo]
azd ai rle modify <name>
azd ai rle version
```

The `create`, `list`, `show`, `versions`, and `sandbox` commands call the RLE control plane directly. The endpoint defaults to `AZD_RLE_CONTROL_PLANE`, then `RLE_CONTROL_PLANE`, then `http://localhost:5000`. If `RLE_BEARER_TOKEN` is set, it is sent as the bearer token.

The `modify` command is still a scaffolded command entry point and returns a structured not-implemented error until the RLE service workflow is added.

## Build

```bash
go build ./...
```

## Test

```bash
go test ./...
```

## Local laptop testing from a branch

Use this workflow to test the extension from a private branch without publishing anything publicly. Start the local RLE control plane by following the [RLE service setup](https://msdata.visualstudio.com/Vienna/_git/vienna?path=/src/azureml-api/src/RLE), then point this extension at that endpoint. The examples below assume the control plane is running at `http://localhost:5000` and is using the local project-scoped RLE routes.

### 1. Check out the branch

```powershell
cd C:\Users\<alias>\source\repos
git clone https://github.com/Azure/azure-dev.git
cd C:\Users\<alias>\source\repos\azure-dev
git fetch origin farhannawaz/rle-cli
git checkout farhannawaz/rle-cli
cd C:\Users\<alias>\source\repos\azure-dev\cli\azd\extensions\azure.ai.rle
```

If the repo is already cloned:

```powershell
cd C:\Users\<alias>\source\repos\azure-dev
git fetch origin farhannawaz/rle-cli
git checkout farhannawaz/rle-cli
cd C:\Users\<alias>\source\repos\azure-dev\cli\azd\extensions\azure.ai.rle
```

### 2. Install the local extension into azd

From the extension directory:

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

Then verify the command is available.

```powershell
azd ai rle --help
azd ai rle version
```

### 3. Run environment create and sandbox provisioning

Create an RLE environment from an ACR image. The first positional argument is the environment name. The server returns a generated environment id; use that id for later commands.

```powershell
azd ai rle create coding-env-e2e `
  --endpoint http://localhost:5000 `
  --project demo `
  --image devrle.azurecr.io/coding_env:latest
```

List environments and copy the generated environment id.

```powershell
azd ai rle list `
  --endpoint http://localhost:5000 `
  --project demo
```

Create a sandbox for the environment. The server creates the disk image asynchronously after environment creation, so this command can initially return `conversion status: Pending`. Retry the same command until it succeeds or reports `Failed`.

```powershell
azd ai rle sandbox create <environment-id> `
  --endpoint http://localhost:5000 `
  --project demo
```

When sandbox creation succeeds, the response includes the sandbox id, ADC sandbox id, status, and URL. You can query it later with:

```powershell
azd ai rle sandbox list <environment-id> `
  --endpoint http://localhost:5000 `
  --project demo

azd ai rle sandbox show <environment-id> <sandbox-id> `
  --endpoint http://localhost:5000 `
  --project demo
```

The `modify` command currently returns a structured not-implemented error. That is expected until the RLE service workflow is added.

To share with teammates, push this extension code to a private branch. Each teammate can pull the branch and run the same commands locally. The generated registry uses absolute paths to artifacts on the local machine, so each teammate should generate their own local registry from their checkout.

To update an existing local install after making code changes, rerun:

```powershell
$registry = Join-Path $env:USERPROFILE ".azd\rle-registry.json"

azd x build
azd x pack -o .\registry-artifacts
azd x publish --registry $registry --artifacts ".\registry-artifacts\*.zip,.\registry-artifacts\*.tar.gz"
azd extension install azure.ai.rle --source rle-local --force
```