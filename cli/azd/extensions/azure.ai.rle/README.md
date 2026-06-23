# Azure AI RLE extension for azd

The `azure.ai.rle` extension adds the `azd ai rle` command group.

## Commands

```bash
azd ai rle create <name>
azd ai rle modify <name>
azd ai rle version
```

The initial `create` and `modify` commands are scaffolded command entry points. They currently return structured not-implemented errors until the RLE service workflow is added.

## Build

```bash
go build ./...
```

## Test

```bash
go test ./...
```

## Private team testing

Use this workflow to test the extension from a private branch without publishing anything publicly.

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

Then verify the command is available:

```powershell
azd ai rle --help
azd ai rle version
azd ai rle create test-rle
azd ai rle modify test-rle
```

The `create` and `modify` commands currently return structured not-implemented errors. That is expected until the RLE service workflow is added.

To share with teammates, push this extension code to a private branch. Each teammate can pull the branch and run the same commands locally. The generated registry uses absolute paths to artifacts on the local machine, so each teammate should generate their own local registry from their checkout.

To update an existing local install after making code changes, rerun:

```powershell
$registry = Join-Path $env:USERPROFILE ".azd\rle-registry.json"

azd x build
azd x pack -o .\registry-artifacts
azd x publish --registry $registry --artifacts ".\registry-artifacts\*.zip,.\registry-artifacts\*.tar.gz"
azd extension install azure.ai.rle --source rle-local --force
```