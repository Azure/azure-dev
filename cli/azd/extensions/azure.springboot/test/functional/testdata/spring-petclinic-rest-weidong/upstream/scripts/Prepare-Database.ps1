$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (!$env:AZURE_SUBSCRIPTION_ID) {
    throw 'Select an Azure subscription before provisioning.'
}
foreach ($tool in @('az', 'docker')) {
    if (!(Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "Install $tool before provisioning."
    }
}
& docker info --format '{{.ServerVersion}}' | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'Start Docker before provisioning; the database identity setup uses the PostgreSQL client image.'
}
$json = & az account show --subscription $env:AZURE_SUBSCRIPTION_ID --only-show-errors --output json
if ($LASTEXITCODE -ne 0) {
    throw 'Could not read the selected Azure CLI account.'
}
$account = ConvertFrom-Json -InputObject ($json -join "`n")
if ($account.user.type -ne 'user') {
    throw 'Database setup requires an Azure CLI user account, not a service principal.'
}
$token = & az account get-access-token --subscription $env:AZURE_SUBSCRIPTION_ID --resource-type arm `
    --query accessToken --output tsv --only-show-errors
if ($LASTEXITCODE -ne 0 -or !$token) {
    throw 'Could not acquire an ARM token for the selected subscription; check the Azure CLI sign-in.'
}
try {
    # Match azd's principal lookup: read the oid from the ARM token without requesting Graph access.
    $payload = (($token -join '').Trim().Split('.')[1]).Replace('-', '+').Replace('_', '/')
    $payload = $payload.PadRight($payload.Length + (4 - $payload.Length % 4) % 4, '=')
    $claims = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($payload)) | ConvertFrom-Json
    $id = [Guid]::Parse($claims.oid)
    if ($id -eq [Guid]::Empty -or [Guid]::Parse($claims.tid) -ne [Guid]::Parse($account.tenantId)) {
        throw 'Invalid principal or tenant.'
    }
    $administratorName = $null
    foreach ($name in @('upn', 'preferred_username', 'unique_name')) {
        $claim = $claims.PSObject.Properties[$name]
        if ($claim -and ![string]::IsNullOrWhiteSpace([string]$claim.Value)) {
            $administratorName = [string]$claim.Value
            break
        }
    }
    if (!$administratorName) { throw 'No user principal name in the ARM token.' }
} catch {
    throw 'Could not identify the signed-in user in the selected subscription tenant from the ARM token.'
}
foreach ($value in @(
    @('DATABASE_ADMIN_OBJECT_ID', $id.ToString()),
    @('DATABASE_ADMIN_NAME', $administratorName)
)) {
    & azd env set @value
    if ($LASTEXITCODE -ne 0) {
        throw "Could not save $($value[0]) in the azd environment."
    }
}
