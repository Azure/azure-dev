param(
    [Parameter(Mandatory = $true)]
    [string] $TemporaryDirectory,
    [Parameter(Mandatory = $true)]
    [string] $EvidenceDirectory
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

foreach ($name in @("AZD_CONFIG_DIR", "AZURE_AI_PROJECT_ENDPOINT", "AZURE_LOCATION", "AZURE_RESOURCE_GROUP")) {
    $value = [Environment]::GetEnvironmentVariable($name)
    if ([string]::IsNullOrWhiteSpace($value) -or $value.StartsWith('$(')) {
        throw "Operational failure: required internal pipeline configuration is missing."
    }
}

$qualityGateThreshold = "0.8"
$evaluatorParameters = Get-Content -LiteralPath (Join-Path $PSScriptRoot "fixture/live/f1-parameters.json") -Raw |
    ConvertFrom-Json
$evaluatorThreshold = [string] $evaluatorParameters.threshold
$work = Join-Path $TemporaryDirectory "eval-scenario-$([guid]::NewGuid().ToString('N'))"
$project = Join-Path $work "project"
$raw = Join-Path $work "raw"
$startJson = Join-Path $raw "start.json"
$showJson = Join-Path $raw "show.json"
$exportJson = Join-Path $raw "export.json"
$artifact = Join-Path $EvidenceDirectory "results.json"
$qualityGateBreached = $false

New-Item -ItemType Directory -Path $raw -Force | Out-Null
try {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "fixture") -Destination $project -Recurse
    $subscription = az account show --query id --output tsv 2> (Join-Path $raw "account.log")
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($subscription)) {
        throw "Operational failure: the approved service connection has no active subscription."
    }
    $env:AZURE_SUBSCRIPTION_ID = $subscription
    azd config set auth.useAzCliAuth true *> (Join-Path $raw "config.log")
    if ($LASTEXITCODE -ne 0) {
        throw "Operational failure: azd could not use the service connection's Azure CLI session."
    }

    Push-Location $project
    try {
        azd up --no-prompt *> (Join-Path $raw "up.log")
        if ($LASTEXITCODE -ne 0) {
            throw "Operational failure: azd up could not reconcile the checked-in evaluation."
        }

        azd ai eval run start --eval ci-f1-quality --no-prompt --no-wait -o json `
            1> $startJson 2> (Join-Path $raw "start.log")
        if ($LASTEXITCODE -ne 0) {
            throw "Operational failure: the evaluation could not be started asynchronously."
        }
        try {
            $start = Get-Content -LiteralPath $startJson -Raw | ConvertFrom-Json
        } catch [ArgumentException] {
            throw "Operational failure: the start command did not return valid JSON."
        }
        if ($start.run_id -isnot [string] -or [string]::IsNullOrWhiteSpace($start.run_id)) {
            throw "Operational failure: the start command did not return run_id."
        }
        $runId = $start.run_id

        azd ai eval run show $runId --eval ci-f1-quality --wait `
            --fail-on "pass-rate=$qualityGateThreshold" --no-prompt -o json `
            1> $showJson 2> (Join-Path $raw "show.log")
        $gateExitCode = $LASTEXITCODE

        azd ai eval run output export $runId --eval ci-f1-quality --format json `
            --output-file $exportJson --no-prompt *> (Join-Path $raw "export.log")
        if ($LASTEXITCODE -ne 0) {
            throw "Operational failure: the evaluation results could not be exported."
        }

        python (Join-Path $PSScriptRoot "sanitize_results.py") `
            --run-summary $showJson --export $exportJson `
            --fixture (Join-Path $project "live/data/golden.jsonl") `
            --evaluator-threshold $evaluatorThreshold --quality-gate-threshold $qualityGateThreshold `
            --output $artifact
        if ($LASTEXITCODE -ne 0) {
            throw "Operational failure: the results did not match the safe completed-run contract."
        }

        $safe = Get-Content -LiteralPath $artifact -Raw | ConvertFrom-Json
        $qualityGateBreached = -not $safe.quality_gate.passed
        if (($safe.quality_gate.passed -and $gateExitCode -ne 0) -or
            ($qualityGateBreached -and $gateExitCode -ne 2)) {
            Remove-Item -LiteralPath $artifact -Force
            throw "Operational failure: the CLI exit code disagreed with the quality gate."
        }

        Write-Host "##vso[task.setvariable variable=EvalScenarioArtifactReady]true"
        Write-Host "Completed evaluation: $($safe.counts.passed) passed, $($safe.counts.failed) failed."
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force
}

if ($qualityGateBreached) {
    Write-Host "##vso[task.logissue type=error]Quality gate breached: completed evaluation missed pass-rate=$qualityGateThreshold."
    exit 2
}
