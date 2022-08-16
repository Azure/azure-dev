param(
    [string] $repomanOutputFolder = [System.IO.Path]::GetTempPath()
)

$repomanOutputFile = Join-Path -Path $repomanOutputFolder -ChildPath 'repoman.json'
$repomanContent = "No changes detected."

if (Test-Path $repomanOutputFile) {
  $repomanContent = Get-Content $repomanOutputFile -Raw | ConvertFrom-Json
}

$output = $repomanContent

if ($repomanContent -ne "No changes detected." ) {
  $output = ""

  foreach($project in $repomanContent){
    $output = $output + "### Project: **$($project.metadataName)**`n"
    $results = $project.results

    foreach($remotePushResult in $results){
      $output = $output + @"
#### Remote: **$($remotePushResult.remote)**
##### Branch: **$($remotePushResult.branch)**

You can initialize this project with:
``````bash
azd init -t $($remotePushResult.org)/$($remotePushResult.repo) -b $($remotePushResult.branch)
``````

[View Changes]($($remotePushResult.branchUrl)) | [Compare Changes]($($remotePushResult.compareUrl))

---

"@
    }
  } 
}
$tag ='<!-- #comment-repoman-generate -->'
$content = @"
$tag
## Repoman Generation Results
Repoman pushed changes to remotes for the following projects:
$output
"@
$file = New-TemporaryFile

Set-Content -Path $file -Value $content
Write-Host "##vso[task.setvariable variable=CommentBodyFile]$file"
