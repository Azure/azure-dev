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

function Write-ProtobufVarint {
    param(
        [System.IO.Stream] $Stream,
        [uint64] $Value
    )

    while ($Value -ge 0x80) {
        $Stream.WriteByte([byte](($Value -band 0x7f) -bor 0x80))
        $Value = $Value -shr 7
    }
    $Stream.WriteByte([byte]$Value)
}

function Write-ProtobufKey {
    param(
        [System.IO.Stream] $Stream,
        [int] $FieldNumber,
        [int] $WireType
    )

    Write-ProtobufVarint -Stream $Stream -Value ([uint64](($FieldNumber -shl 3) -bor $WireType))
}

function Write-ProtobufBytesField {
    param(
        [System.IO.Stream] $Stream,
        [int] $FieldNumber,
        [byte[]] $Value
    )

    Write-ProtobufKey -Stream $Stream -FieldNumber $FieldNumber -WireType 2
    Write-ProtobufVarint -Stream $Stream -Value ([uint64]$Value.Length)
    $Stream.Write($Value, 0, $Value.Length)
}

function Write-ProtobufStringField {
    param(
        [System.IO.Stream] $Stream,
        [int] $FieldNumber,
        [string] $Value
    )

    Write-ProtobufBytesField `
        -Stream $Stream `
        -FieldNumber $FieldNumber `
        -Value ([System.Text.Encoding]::UTF8.GetBytes($Value))
}

function Write-ProtobufEnumField {
    param(
        [System.IO.Stream] $Stream,
        [int] $FieldNumber,
        [uint64] $Value
    )

    Write-ProtobufKey -Stream $Stream -FieldNumber $FieldNumber -WireType 0
    Write-ProtobufVarint -Stream $Stream -Value $Value
}

function Write-ProtobufFixed64Field {
    param(
        [System.IO.Stream] $Stream,
        [int] $FieldNumber,
        [byte[]] $Value
    )

    if ($Value.Length -ne 8) {
        throw "A protobuf fixed64 field requires exactly eight bytes."
    }
    if (-not [System.BitConverter]::IsLittleEndian) {
        [array]::Reverse($Value)
    }

    Write-ProtobufKey -Stream $Stream -FieldNumber $FieldNumber -WireType 1
    $Stream.Write($Value, 0, $Value.Length)
}

function New-ProtobufMessage {
    param([scriptblock] $WriteFields)

    $stream = [System.IO.MemoryStream]::new()
    try {
        & $WriteFields $stream
        return ,$stream.ToArray()
    }
    finally {
        $stream.Dispose()
    }
}

function Write-Utf8JsonFile {
    param(
        [string] $Path,
        [object] $Value,
        [int] $Depth = 12
    )

    $json = $Value | ConvertTo-Json -Depth $Depth
    [System.IO.File]::WriteAllText(
        $Path,
        $json,
        [System.Text.UTF8Encoding]::new($false)
    )
}

function New-SyntheticTestData {
    param(
        [string] $Directory,
        [string] $RunId,
        [string] $ProjectId,
        [string] $Entity
    )

    $testId = [guid]::NewGuid().ToString("N")
    $traceId = [guid]::NewGuid().ToByteArray()
    $spanId = [guid]::NewGuid().ToByteArray()[0..7]
    $nowUnixNano = [uint64](
        [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds() * 1000000
    )
    $endUnixNano = $nowUnixNano + [uint64]1000000

    $serviceNameValue = New-ProtobufMessage {
        param($stream)
        Write-ProtobufStringField -Stream $stream -FieldNumber 1 `
            -Value "azd.ai.loom.smoke-test"
    }
    $serviceNameAttribute = New-ProtobufMessage {
        param($stream)
        Write-ProtobufStringField -Stream $stream -FieldNumber 1 -Value "service.name"
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $serviceNameValue
    }
    $resource = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $serviceNameAttribute
    }
    $scope = New-ProtobufMessage {
        param($stream)
        Write-ProtobufStringField -Stream $stream -FieldNumber 1 `
            -Value "azd.ai.loom.smoke-test"
    }

    $metricDataPoint = New-ProtobufMessage {
        param($stream)
        Write-ProtobufFixed64Field -Stream $stream -FieldNumber 3 `
            -Value ([System.BitConverter]::GetBytes($nowUnixNano))
        Write-ProtobufFixed64Field -Stream $stream -FieldNumber 4 `
            -Value ([System.BitConverter]::GetBytes([double]1))
    }
    $gauge = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $metricDataPoint
    }
    $metric = New-ProtobufMessage {
        param($stream)
        Write-ProtobufStringField -Stream $stream -FieldNumber 1 -Value "azd.loom.synthetic"
        Write-ProtobufStringField -Stream $stream -FieldNumber 2 -Value "Synthetic Loom ingestion smoke-test metric"
        Write-ProtobufStringField -Stream $stream -FieldNumber 3 -Value "1"
        Write-ProtobufBytesField -Stream $stream -FieldNumber 5 -Value $gauge
    }
    $scopeMetrics = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $scope
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $metric
    }
    $resourceMetrics = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resource
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $scopeMetrics
    }
    $metricsRequest = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resourceMetrics
    }

    $logBody = New-ProtobufMessage {
        param($stream)
        Write-ProtobufStringField -Stream $stream -FieldNumber 1 `
            -Value "Synthetic Loom smoke-test log $testId"
    }
    $logRecord = New-ProtobufMessage {
        param($stream)
        Write-ProtobufFixed64Field -Stream $stream -FieldNumber 1 `
            -Value ([System.BitConverter]::GetBytes($nowUnixNano))
        Write-ProtobufEnumField -Stream $stream -FieldNumber 2 -Value 9
        Write-ProtobufStringField -Stream $stream -FieldNumber 3 -Value "INFO"
        Write-ProtobufBytesField -Stream $stream -FieldNumber 5 -Value $logBody
        Write-ProtobufBytesField -Stream $stream -FieldNumber 9 -Value $traceId
        Write-ProtobufBytesField -Stream $stream -FieldNumber 10 -Value $spanId
    }
    $scopeLogs = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $scope
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $logRecord
    }
    $resourceLogs = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resource
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $scopeLogs
    }
    $logsRequest = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resourceLogs
    }

    $span = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $traceId
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $spanId
        Write-ProtobufStringField -Stream $stream -FieldNumber 5 `
            -Value "azd-loom-synthetic-span"
        Write-ProtobufEnumField -Stream $stream -FieldNumber 6 -Value 1
        Write-ProtobufFixed64Field -Stream $stream -FieldNumber 7 `
            -Value ([System.BitConverter]::GetBytes($nowUnixNano))
        Write-ProtobufFixed64Field -Stream $stream -FieldNumber 8 `
            -Value ([System.BitConverter]::GetBytes($endUnixNano))
    }
    $scopeSpans = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $scope
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $span
    }
    $resourceSpans = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resource
        Write-ProtobufBytesField -Stream $stream -FieldNumber 2 -Value $scopeSpans
    }
    $tracesRequest = New-ProtobufMessage {
        param($stream)
        Write-ProtobufBytesField -Stream $stream -FieldNumber 1 -Value $resourceSpans
    }

    $metricsFile = Join-Path $Directory "metrics.pb"
    $logsFile = Join-Path $Directory "logs.pb"
    $tracesFile = Join-Path $Directory "traces.pb"
    [System.IO.File]::WriteAllBytes($metricsFile, $metricsRequest)
    [System.IO.File]::WriteAllBytes($logsFile, $logsRequest)
    [System.IO.File]::WriteAllBytes($tracesFile, $tracesRequest)

    $agentTracesFile = Join-Path $Directory "agent-traces.json"
    Write-Utf8JsonFile -Path $agentTracesFile -Value @{
        run_id = $RunId
        resourceSpans = @(
            @{
                resource = @{
                    attributes = @(
                        @{
                            key = "service.name"
                            value = @{ stringValue = "azd.ai.loom.smoke-test" }
                        }
                    )
                }
                scopeSpans = @(
                    @{
                        scope = @{ name = "azd.ai.loom.smoke-test" }
                        spans = @(
                            @{
                                traceId = [Convert]::ToHexString($traceId).ToLowerInvariant()
                                spanId = [Convert]::ToHexString($spanId).ToLowerInvariant()
                                name = "azd-loom-synthetic-agent-span"
                                kind = 1
                                startTimeUnixNano = $nowUnixNano.ToString()
                                endTimeUnixNano = $endUnixNano.ToString()
                            }
                        )
                    }
                )
            }
        )
    }

    $graphQLFile = Join-Path $Directory "graphql-request.json"
    Write-Utf8JsonFile -Path $graphQLFile -Value @{
        query = "query Run(`$entity: String!, `$project: String!, `$name: String!) { project(name: `$project, entityName: `$entity) { run(name: `$name) { name displayName state summaryMetrics } } }"
        variables = @{
            entity = $Entity
            project = $ProjectId
            name = $RunId
        }
    }

    $fileStreamFile = Join-Path $Directory "file-stream-request.json"
    Write-Utf8JsonFile -Path $fileStreamFile -Value @{
        files = @{
            "output.log" = @{
                offset = 0
                content = @("Synthetic Loom smoke-test log $testId`n")
            }
        }
    }

    return [pscustomobject]@{
        MetricsFile = $metricsFile
        LogsFile = $logsFile
        TracesFile = $tracesFile
        AgentTracesFile = $agentTracesFile
        GraphQLFile = $graphQLFile
        FileStreamFile = $fileStreamFile
    }
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

$projectUri = [uri]$ProjectEndpoint
$resolvedProjectId = $ProjectId
if ([string]::IsNullOrWhiteSpace($resolvedProjectId)) {
    $segments = $projectUri.AbsolutePath.Trim("/").Split("/")
    if ($segments.Length -lt 3 -or $segments[-2] -ne "projects") {
        throw "Could not derive the project ID. Provide a standard project endpoint or -ProjectId."
    }
    $resolvedProjectId = [uri]::UnescapeDataString($segments[-1])
}
$resolvedWandBEntity = if ($WandBEntity) { $WandBEntity } else { $projectUri.Host.Split(".")[0] }
$resolvedWandBProject = if ($WandBProject) { $WandBProject } else { $resolvedProjectId }

$commonArguments = @("--api-version", $ApiVersion, "--output", "json")
if (-not [string]::IsNullOrWhiteSpace($ProjectId)) {
    $commonArguments += @("--project-id", $ProjectId)
}

$hadProjectEndpoint = Test-Path Env:FOUNDRY_PROJECT_ENDPOINT
$previousProjectEndpoint = $env:FOUNDRY_PROJECT_ENDPOINT
$env:FOUNDRY_PROJECT_ENDPOINT = $ProjectEndpoint
$syntheticDataDirectory = $null
$syntheticData = $null

try {
    if (-not $SkipWriteOperations) {
        $syntheticDataDirectory = Join-Path `
            ([System.IO.Path]::GetTempPath()) `
            "azd-loom-smoke-$([guid]::NewGuid().ToString("N"))"
        [System.IO.Directory]::CreateDirectory($syntheticDataDirectory) | Out-Null
        $syntheticData = New-SyntheticTestData `
            -Directory $syntheticDataDirectory `
            -RunId $RunId `
            -ProjectId $resolvedWandBProject `
            -Entity $resolvedWandBEntity
    }

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
        "--min", $MinStep.ToString([System.Globalization.CultureInfo]::InvariantCulture),
        "--max", $MaxStep.ToString([System.Globalization.CultureInfo]::InvariantCulture)
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
                "--file", $syntheticData.MetricsFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest OTLP logs" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "logs",
                "--run-id", $RunId,
                "--file", $syntheticData.LogsFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest OTLP traces" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "traces",
                "--run-id", $RunId,
                "--file", $syntheticData.TracesFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Ingest agent traces" `
            -CommandArguments (@(
                "ai", "loom", "run", "ingest", "agent-traces",
                "--run-id", $RunId,
                "--file", $syntheticData.AgentTracesFile
            ) + $commonArguments)

        Invoke-AzdTest -Name "Execute W&B GraphQL request" `
            -CommandArguments (@(
                "ai", "loom", "run", "wandb", "graphql",
                "--file", $syntheticData.GraphQLFile
            ) + $commonArguments)

        $fileStreamArguments = @(
            "ai", "loom", "run", "wandb", "file-stream",
            "--run-id", $RunId,
            "--file", $syntheticData.FileStreamFile
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
    if (
        $syntheticDataDirectory -and
        (Test-Path -LiteralPath $syntheticDataDirectory) -and
        ([System.IO.Path]::GetFileName($syntheticDataDirectory) -like "azd-loom-smoke-*")
    ) {
        try {
            [System.IO.Directory]::Delete($syntheticDataDirectory, $true)
        }
        catch {
            Write-Warning "Could not remove temporary synthetic test data: $($_.Exception.Message)"
        }
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
