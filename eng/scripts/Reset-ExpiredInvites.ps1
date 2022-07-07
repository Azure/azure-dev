param(
    $Repo,
    $Permission = 'pull'
)

."$PSScriptRoot/common.ps1"

$invitations = GetInvitations $Repo
$targetInvitations = $invitations.Where({ $_.expired -eq $true })

foreach ($invitation in $targetInvitations) {
    Write-Host "Re-inviting $(Secretize $invitation.invitee.login) (id: $($invitation.Id))"
    gh api "repos/$Repo/invitations/$($invitation.id)" --method DELETE | Out-Null
    gh api "repos/$Repo/collaborators/$($invitation.invitee.login)" --method PUT --field permission=$Permission | Out-Null
}
