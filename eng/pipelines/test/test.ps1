$repomanOutputFile = "./repoman.json"
$repomanContent = "No changes detected."

if (Test-Path $repomanOutputFile) {
$repomanContent = Get-Content $repomanOutputFile -Raw | ConvertFrom-Json 
}
write-Host "-----------------count of J loop"
write-Host $repomanContent.count
write-Host "------------------------------------"

$output = ""
for ($j = 0; $j -lt $repomanContent.count; $j++) { 
write-Host "----------------- J "
write-Host $j
write-Host "------------------------------------"

$output = $output + "### Project: **$($repomanContent[$j].metadataName)**`n"
$results = $repomanContent[$j].results
for($i = 0; $i -lt $results.count; $i++) { 
write-Host "----------------- i "
write-Host $i
write-Host "------------------------------------"

$output = $output + @"
#### Remote: **$($results[$i].remote)**
##### Branch: **$($results[$i].branch)**

You can initialize this project with:
``````bash
azd init -t $($results[$i].org)/$($results[$i].repo) -b $($results[$i].branch)
``````

[View Changes]($($results[$i].branchUrl)) | [Compare Changes]($($results[$i].compareUrl))

---

"@
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