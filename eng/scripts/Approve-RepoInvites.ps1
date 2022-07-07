param(
    $Owner='Azure-Samples'
)

$err = $( $invitationsJson = gh api "user/repository_invitations" --paginate ) 2>&1

if ($LASTEXITCODE) {
    Write-Error "Could not fetch invitations: $err"
    exit 1
}

$invitations = ConvertFrom-Json $invitationsJson
$targetInvitations = $invitations.Where({
    $_.repository.owner.login -eq $Owner `
    -and $_.expired -ne $true
})

foreach ($invite in $targetInvitations) {
    Write-Host "Accepting invite for $($invite.permissions) on repo $($invite.repository.full_name)"
    gh api "user/repository_invitations/$($invite.id)" --method PATCH
}
