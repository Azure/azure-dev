param(
    [string] $TargetBranchName,
    [string] $RemoteName,
    [string] $ResultsFileLocation,
    [string] $RunnerTemp = [System.IO.Path]::GetTempPath(),
    [switch] $WhatIf
)

$projectsJson = repoman list --format json | Out-String
$projects = ConvertFrom-Json $projectsJson

$additionalParameters = @()
if (-not $WhatIf) {
    $additionalParameters += '--update'
}

if ($ResultsFileLocation) {
    $additionalParameters += "--resultsFile", $ResultsFileLocation
}

foreach ($project in $projects) {
    $projectPath = $project.projectPath
    $templatePath = $project.templatePath.Replace($projectPath, "")

    Write-Host @"
repoman generate `
    -s $projectPath `
    -o $RunnerTemp `
    -t $templatePath `
    --branch "$TargetBranchName" `
    --remote "$RemoteName" `
    --https `
    --fail-on-update-error `
    $($additionalParameters -join ([System.Environment]::NewLine + "    "))
"@

    & repoman generate `
        -s $projectPath `
        -o $RunnerTemp `
        -t $templatePath `
        --branch `"$TargetBranchName`" `
        --remote "$RemoteName" `
        --https `
        --fail-on-update-error `
        $additionalParameters

    if ($LASTEXITCODE) {
        Write-Error "Error running repoman generate. Exit code: $LASTEXITCODE"
        exit $LASTEXITCODE
    }
}