param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $ProjectEndpoint,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $RunId,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $SecondRunId,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $TraceId,

    [string] $ProjectId,
    [string] $ApiVersion = "v1",
    [string[]] $MetricName = @("loss"),
    [string[]] $SystemMetricName = @("system/cpu"),
    [string] $SpanName = "chat",
    [double] $MinStep = 0,
    [double] $MaxStep = 100,
    [ValidateRange(1, [int]::MaxValue)]
    [int] $Take = 10,

    [string] $MetricsFile,
    [string] $LogsFile,
    [string] $TracesFile,
    [string] $AgentTracesFile,
    [string] $GraphQLFile,
    [string] $FileStreamFile,
    [string] $WandBEntity,
    [string] $WandBProject,

    [string] $AzdPath = "azd",
    [switch] $SkipBuild,
    [switch] $SkipWriteOperations,
    [switch] $StopOnFailure
)

$ErrorActionPreference = "Stop"

$results = [System.Collections.Generic.List[object]]::new()

function Add-TestResult {
    param(
        [string] $Name,
        [string] $Status,
        [int] $ExitCode
    )

    $results.Add([pscustomobject]@{
        Test = $Name
        Status = $Status
        ExitCode = $ExitCode
    })
}

function Invoke-AzdTest {
    param(
        [string] $Name,
        [string[]] $CommandArguments,
        [switch] $Required
    )

    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan

    try {
        & $AzdPath @CommandArguments
        $exitCode = $LASTEXITCODE
    }
    catch {
        Write-Host $_.Exception.Message -ForegroundColor Red
        $exitCode = 1
    }

    if ($exitCode -eq 0) {
        Add-TestResult -Name $Name -Status "Passed" -ExitCode 0
        Write-Host "Passed: $Name" -ForegroundColor Green
        return
    }

    Add-TestResult -Name $Name -Status "Failed" -ExitCode $exitCode
    Write-Host "Failed: $Name (exit code $exitCode)" -ForegroundColor Red
    if ($Required -or $StopOnFailure) {
        throw "Stopping after failed test: $Name"
    }
}

function Resolve-RequiredFile {
    param(
        [string] $ParameterName,
        [string] $Path
    )

    if ([string]::IsNullOrWhiteSpace($Path)) {
        throw "-$ParameterName is required unless -SkipWriteOperations is set."
    }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "The file supplied through -$ParameterName does not exist."
    }

    return (Resolve-Path -LiteralPath $Path).Path
}

if ($MaxStep -lt $MinStep) {
    throw "-MaxStep must be greater than or equal to -MinStep."
}
if ($MetricName.Count -eq 0) {
    throw "Provide at least one value through -MetricName."
}
if ($SystemMetricName.Count -eq 0) {
    throw "Provide at least one value through -SystemMetricName."
}

$azdCommand = Get-Command $AzdPath -ErrorAction SilentlyContinue
if ($null -eq $azdCommand) {
    throw "Could not find azd. Install azd 1.32.0 or later, or provide -AzdPath."
}

if (-not $SkipWriteOperations) {
    $MetricsFile = Resolve-RequiredFile -ParameterName "MetricsFile" -Path $MetricsFile
    $LogsFile = Resolve-RequiredFile -ParameterName "LogsFile" -Path $LogsFile
    $TracesFile = Resolve-RequiredFile -ParameterName "TracesFile" -Path $TracesFile
    $AgentTracesFile = Resolve-RequiredFile -ParameterName "AgentTracesFile" -Path $AgentTracesFile
    $GraphQLFile = Resolve-RequiredFile -ParameterName "GraphQLFile" -Path $GraphQLFile
    $FileStreamFile = Resolve-RequiredFile -ParameterName "FileStreamFile" -Path $FileStreamFile
}

$commonArguments = @("--api-version", $ApiVersion, "--output", "json")
if (-not [string]::IsNullOrWhiteSpace($ProjectId)) {
    $commonArguments += @("--project-id", $ProjectId)
}

$hadProjectEndpoint = Test-Path Env:FOUNDRY_PROJECT_ENDPOINT
$previousProjectEndpoint = $env:FOUNDRY_PROJECT_ENDPOINT
$env:FOUNDRY_PROJECT_ENDPOINT = $ProjectEndpoint

try {
    if (-not $SkipBuild) {
        Push-Location $PSScriptRoot
        try {
            Invoke-AzdTest -Name "Build and install azure.ai.loom" -CommandArguments @("x", "build") -Required
        }
        finally {
            Pop-Location
        }
    }

    Invoke-AzdTest -Name "Show Loom command help" `
        -CommandArguments @("ai", "loom", "--help")

    Invoke-AzdTest -Name "List runs" `
        -CommandArguments (@("ai", "loom", "run", "list", "--take", $Take) + $commonArguments)

    Invoke-AzdTest -Name "List run history keys" `
        -CommandArguments (@("ai", "loom", "run", "history-keys", "--run-id", $RunId) + $commonArguments)

    Invoke-AzdTest -Name "Get run summary" `
        -CommandArguments (@("ai", "loom", "run", "summary", "--run-id", $RunId, "--take", $Take) + $commonArguments)

    Invoke-AzdTest -Name "List run metrics" `
        -CommandArguments (@("ai", "loom", "run", "metrics", "--run-id", $RunId, "--take", $Take) + $commonArguments)

    $systemMetricArguments = @(
        "ai", "loom", "run", "system-metrics",
        "--run-id", $RunId,
        "--take", $Take
    )
    foreach ($name in $SystemMetricName) {
        $systemMetricArguments += @("--name", $name)
    }
    Invoke-AzdTest -Name "Get run system metrics" `
        -CommandArguments ($systemMetricArguments + $commonArguments)

    Invoke-AzdTest -Name "Get run logs" `
        -CommandArguments (@("ai", "loom", "run", "logs", "--run-id", $RunId, "--take", $Take) + $commonArguments)

    Invoke-AzdTest -Name "Get run log records" `
        -CommandArguments (@("ai", "loom", "run", "log-records", "--run-id", $RunId, "--take", $Take) + $commonArguments)

    $compareArguments = @(
        "ai", "loom", "run", "compare",
        "--run-id", $RunId,
        "--run-id", $SecondRunId,
        "--min", $MinStep,
        "--max", $MaxStep
    )
    foreach ($name in $MetricName) {
        $compareArguments += @("--metric", $name)
    }
    Invoke-AzdTest -Name "Compare runs" `
        -CommandArguments ($compareArguments + $commonArguments)

    Invoke-AzdTest -Name "List run traces" `
        -CommandArguments (@("ai", "loom", "run", "trace", "list", "--run-id", $RunId, "--take", $Take) + $commonArguments)

    Invoke-AzdTest -Name "Show run trace" `
        -CommandArguments (@(
            "ai", "loom", "run", "trace", "show",
            "--run-id", $RunId,
            "--trace-id", $TraceId
        ) + $commonArguments)

    Invoke-AzdTest -Name "Request trace chat" `
        -CommandArguments (@(
            "ai", "loom", "run", "trace", "chat",
            "--run-id", $RunId,
            "--trace-id", $TraceId
        ) + $commonArguments)

    $spanFilter = @{
        '$expr' = @{
            '$eq' = @(
                @{ '$getField' = "span_name" },
                @{ '$literal' = $SpanName }
            )
        }
    } | ConvertTo-Json -Depth 5 -Compress
    Invoke-AzdTest -Name "Query run spans" `
        -CommandArguments (@(
            "ai", "loom", "run", "span", "query",
            "--run-id", $RunId,
            "--filter", $spanFilter,
            "--include-details",
            "--limit", $Take
        ) + $commonArguments)

    if ($SkipWriteOperations) {
        foreach ($name in @(
            "Ingest OTLP metrics",
            "Ingest OTLP logs",
            "Ingest OTLP traces",
            "Ingest agent traces",
            "Execute W&B GraphQL request",
            "Send W&B file stream"
        )) {
            Add-TestResult -Name $name -Status "Skipped" -ExitCode 0
        }
    }
    else {
        Invoke-AzdTest -Name "Ingest OTLP metrics" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "metrics",
                "--run-id", $RunId,
                "--file", $MetricsFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest OTLP logs" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "logs",
                "--run-id", $RunId,
                "--file", $LogsFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest OTLP traces" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "traces",
                "--run-id", $RunId,
                "--file", $TracesFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest agent traces" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "agent-traces",
                "--run-id", $RunId,
                "--file", $AgentTracesFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Execute W&B GraphQL request" `
            -CommandArguments (@(
                "ai", "loom", "run", "wandb", "graphql",
                "--file", $GraphQLFile
            ) + $commonArguments)

        $fileStreamArguments = @(
            "ai", "loom", "run", "wandb", "file-stream",
            "--run-id", $RunId,
            "--file", $FileStreamFile
        )
        if (-not [string]::IsNullOrWhiteSpace($WandBEntity)) {
            $fileStreamArguments += @("--entity", $WandBEntity)
        }
        if (-not [string]::IsNullOrWhiteSpace($WandBProject)) {
            $fileStreamArguments += @("--wandb-project", $WandBProject)
        }
        Invoke-AzdTest -Name "Send W&B file stream" `
            -CommandArguments ($fileStreamArguments + $commonArguments)
    }
}
finally {
    if ($hadProjectEndpoint) {
        $env:FOUNDRY_PROJECT_ENDPOINT = $previousProjectEndpoint
    }
    else {
        Remove-Item Env:FOUNDRY_PROJECT_ENDPOINT -ErrorAction SilentlyContinue
    }
}

Write-Host ""
Write-Host "Loom smoke-test results" -ForegroundColor Cyan
$results | Format-Table -AutoSize | Out-Host

$failed = @($results | Where-Object Status -eq "Failed")
if ($failed.Count -gt 0) {
    Write-Host "$($failed.Count) test(s) failed." -ForegroundColor Red
    exit 1
}

Write-Host "All executed tests passed." -ForegroundColor Green
exit 0
