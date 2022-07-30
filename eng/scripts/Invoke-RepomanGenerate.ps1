param(
    [string] $TargetBranchName,
    [string] $ResultsFileLocation = "$([System.IO.Path]::GetTempPath())/repoman.json",
    [string] $RunnerTemp = [System.IO.Path]::GetTempPath(),
    [switch] $WhatIf
)

if (Test-Path $ResultsFileLocation) {
    Remove-Item $ResultsFileLocation
}

$projectsJson = repoman list --format json | Out-String
$projects = ConvertFrom-Json $projectsJson

$projectPaths = $projects.projectPath

foreach ($projectPath in $projectPaths) {
    $additionalParameters = '--update'
    if ($WhatIf) {
        $additionalParameters = ''
    }

    Write-Host @"
repoman generate `
    -s $projectPath `
    -o $RunnerTemp `
    --branch "$TargetBranchName" `
    --https `
    --fail-on-update-error `
    --resultsFile $ResultsFileLocation `
    $additionalParameters
"@

    repoman generate `
        -s $projectPath `
        -o $RunnerTemp `
        --branch `"$TargetBranchName`" `
        --https `
        --fail-on-update-error `
        --resultsFile $ResultsFileLocation `
        $additionalParameters

    if ($LASTEXITCODE) {
        Write-Error "Error running repoman generate. Exit code: $LASTEXITCODE"
        exit $LASTEXITCODE
    }
}