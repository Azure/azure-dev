param(
    [string] $AddUsers,
    [string] $RemoveUsers,
    [string] $Repos = 'Azure/azure-dev-pr',
    [string] $Permission = 'pull',
    [string] $RepoManPath = 'templates/'
)

."$PSScriptRoot/common.ps1"

function IsUser($user) {
    if (!$user) {
        return $false
    }

    $path = "users/$user"
    $err = $( $responseJson = gh api $path ) 2>&1

    if ($LASTEXITCODE) {
        Write-Host "Search for user $(Secretize $user) returned error: $err"
        return $false
    }

    $response = ConvertFrom-Json $responseJson
    if ($response.type -ne 'User') {
        Write-Host "Supplied user is not a user"
        Write-Host "User: $(Secretize $user)"
        Write-Host "Type: $($response.type)"
        return $false
    }

    return $true
}

function IsUserCollaborator($user, $repo) {
    $path = "repos/$repo/collaborators/$user"
    $err = $( gh api $path ) 2>&1
    if ($LASTEXITCODE) {
        if ($err.Where({ $_.Exception -and $_.Exception.Message -and $_.Exception.Message.Contains("HTTP 404") })) {
            return $false
        }
        throw $err
    }
    return $true
}

function GetInvitationInfo($repo) {
    $invitationsResponse = GetInvitations $repo

    $output = @()
    foreach ($invite in $invitationsResponse) {
        $output += @{ Id = $invite.id; User = $invite.invitee.login }
    }
    return $output
}

function AddUsers($users, $repo) {
    if (!$users) {
        return
    }
    $invitations = GetInvitationInfo $repo

    foreach ($user in $users) {
        $invite = $invitations.Where({ $_.User -eq $user })

        if (!$invite -and !(IsUserCollaborator $user $repo)) {
            Write-Host "  - Add: Adding $(Secretize $user)"
            $path = "repos/$repo/collaborators/$user"

            gh api $path --method PUT --field permission=$Permission | Out-Null

            if ($LASTEXITCODE) {
                Write-Host "ERROR: Could not invite collaborator $(Secretize $user)"
            }
        } else {
            Write-Host "  - Add: Do nothing, $(Secretize $user) is already invited or a collaborator"
        }
    }
}

function RemoveUsers($users, $repo) {
    if (!$users) {
        return
    }

    $invitations = GetInvitationInfo $repo
    foreach ($user in $users) {
        $invite = $invitations.Where({ $_.User -eq $user })

        if ($invite) {
            Write-Host "  - Remove: Revoke invite for $(Secretize $user)"
            $path = "repos/$repo/invitations/$($invite.Id)"
            gh api $path --method DELETE | Out-Null

            if ($LASTEXITCODE) {
                Write-Host "ERROR: Could not revoke invitation for $(Secretize $user)"
            }
        } elseif (IsUserCollaborator $user $repo) {
            Write-Host "  - Remove: Removing collaborator $(Secretize $user)"
            $path = "repos/$repo/collaborators/$user"
            gh api $path --method DELETE | Out-Null

            if ($LASTEXITCODE) {
                Write-Host "ERROR: Could not remove collaborator $(Secretize $user)"
            }
        } else {
            Write-Host "  - Remove: Do nothing, $(Secretize $user) is not a collaborator"
        }
    }
}

$DELIMETER = ','

$targetRepos = $Repos -split $DELIMETER | ForEach-Object { $_.Trim() }
$usersToAdd = ($AddUsers -split $DELIMETER) `
    | ForEach-Object { $_.Trim() } `
    | Where-Object { $_ -and (IsUser $_) }
$usersToRemove = ($RemoveUsers -split $DELIMETER) `
    | ForEach-Object { $_.Trim() } `
    | Where-Object { $_ -and (IsUser $_) }

foreach ($repo in $targetRepos) {
    Write-Host "Repository: $repo"
    AddUsers  $usersToAdd $repo
    RemoveUsers $usersToRemove $repo
}

exit 0
