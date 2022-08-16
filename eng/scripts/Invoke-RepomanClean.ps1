param(
    [string] $CommitId,
    [string] $Repo,
    [string] $RunnerTemp = [System.IO.Path]::GetTempPath()
)

$pulls = ConvertFrom-Json (gh api "/repos/$Repo/commits/$CommitId/pulls" )
$closedPRs = $pulls.Where({ $_.state -eq 'closed' }) 

foreach($pr in $closedPRs) {
    $PRNumber = $pr.number
    $targetBranchName =  "pr/$PRNumber"

    $projectsJson = repoman list --format json | Out-String
    $projects = ConvertFrom-Json $projectsJson

    foreach ($project in $projects) {
        $projectPath = $project.projectPath
        $templatePath = $project.templatePath.Replace($projectPath, "")
        Write-Host @"

repoman clean `
    -s $projectPath `
    -o $RunnerTemp `
    -t $templatePath `
    --branch $targetBranchName `
    --https

"@

        repoman clean `
            -s $projectPath `
            -o $RunnerTemp `
            -t $templatePath `
            --branch $targetBranchName `
            --https

        if ($LASTEXITCODE) {
            Write-Error "Error running repoman clean. Exit code: $LASTEXITCODE"
            exit $LASTEXITCODE
        }
    }
}