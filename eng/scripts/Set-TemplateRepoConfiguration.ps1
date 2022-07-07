param(
    $Org,
    $RepoName
)

function SetPermission($org, $repoName, $team, $permission) {
    Write-Host "Setting $permission for $team on $org/$repoName"
    $err = $( gh api "orgs/$org/teams/$team/repos/$org/$repoName" `
        --method PUT `
        --field permission=$permission ) 2>&1

    if ($LASTEXITCODE) {
        Write-Error "Could not set permission: $err"
        exit 1
    }
}

gh auth status
if ($LASTEXITCODE) {
    Write-Error "Need to be authenticated with gh CLI (try `gh auth login` before running)"
    exit 1
}

$repo = "$Org/$Reponame"

Write-Host "Setting repo configuration"
gh repo edit $repo --template --enable-issues=false --enable-wiki=false | Out-Null

SetPermission $Org $RepoName 'azure-dev-admin' 'admin'
SetPermission $Org $Reponame 'azure-dev-read' 'pull'

Write-Host "Inviting azure-sdk as admin"
gh api "repos/$repo/collaborators/azure-sdk" `
    --method PUT `
    --field permission=admin | Out-Null
