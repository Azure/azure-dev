param(
    [string] $PRNumber,
    [string] $BodyFile
)

if (Test-Path $BodyFile) {
    $repomanContent = Get-Content $BodyFile -Raw | ConvertFrom-Json
    $itemCount = $repomanContent.count
    $branchName =  "pr/$PRNumber"
    for ($j = 0; $j -lt $repomanContent.count; $j++) { 
        $metaDataName = $($repomanContent[$j].metadataName)
        $results = $repomanContent[$j].results

        if ($results.count -gt 0 ) {
            $orgName = $results[0].org;
            $repo= $results[0].repo;

            Write-host "Delete PR Branch : /repos/$orgName/$repo/git/refs/heads/$branchName"
         
            gh api --method Delete /repos/$orgName/$repo/git/refs/heads/$branchName | jq
        }
    }
}