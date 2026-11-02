$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$pattern = '^/subscriptions/([0-9a-f-]{36})/resourceGroups/([^/]+)/providers/' +
    'Microsoft\.DBforPostgreSQL/flexibleServers/([a-z0-9-]+)$'
if ($env:DATABASE_SERVER_ID -notmatch $pattern) {
    throw 'Expected a PostgreSQL Flexible Server resource ID from provisioning.'
}
$subscription = [Guid]::Parse($Matches[1]).ToString()
$group = $Matches[2]
$server = $Matches[3]
try {
    & (Join-Path $PSScriptRoot 'Update-DatabaseFirewall.ps1')
    . (Join-Path $PSScriptRoot 'Initialize-DatabaseIdentity.ps1')
    Initialize-DatabaseIdentity -ServerId $env:DATABASE_SERVER_ID -DatabaseHost $env:DATABASE_HOST `
        -PrincipalId $env:APPLICATION_PRINCIPAL_ID -AdministratorName $env:DATABASE_ADMIN_NAME
} finally {
    & az postgres flexible-server firewall-rule delete --subscription $subscription --resource-group $group `
        --server-name $server --name bootstrap-local-ip --yes --output none --only-show-errors
    if ($LASTEXITCODE -ne 0) {
        throw "Remove temporary firewall rule 'bootstrap-local-ip' from '$server'; automatic cleanup failed."
    }
}
Write-Host 'Non-admin database identity configured; temporary bootstrap access removed. Petclinic will initialize its tables.'
