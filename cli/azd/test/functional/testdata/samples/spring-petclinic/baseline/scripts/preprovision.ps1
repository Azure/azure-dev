$ErrorActionPreference = 'Stop'

$values = azd env get-values --output json | ConvertFrom-Json
$modePersisted = $values.PSObject.Properties.Name -contains 'SPRING_DATABASE_TYPE'
$modeProvided = Test-Path Env:SPRING_DATABASE_TYPE
$mode = if ($modePersisted) {
    [string]$values.SPRING_DATABASE_TYPE
} elseif ($modeProvided) {
    $env:SPRING_DATABASE_TYPE
} else {
    'h2'
}

if ([string]::IsNullOrEmpty($mode) -or $mode -notin @('h2', 'postgresql')) {
    throw "SPRING_DATABASE_TYPE must be h2 or postgresql"
}

$persistedPresent = $values.PSObject.Properties.Name -contains 'SPRING_DATABASE_PASSWORD'
$providedPresent = Test-Path Env:SPRING_DATABASE_PASSWORD
$persisted = [string]$values.SPRING_DATABASE_PASSWORD
$provided = [string]$env:SPRING_DATABASE_PASSWORD
if ($mode -eq 'h2') {
    if ($persistedPresent -or $providedPresent) {
        throw 'SPRING_DATABASE_PASSWORD cannot be set in h2 mode'
    }
    azd env set SPRING_DATABASE_TYPE $mode | Out-Null
    return
}

if (($persistedPresent -and [string]::IsNullOrEmpty($persisted)) -or
    ($providedPresent -and [string]::IsNullOrEmpty($provided))) {
    throw 'SPRING_DATABASE_PASSWORD cannot be explicitly empty in PostgreSQL mode'
}
if ($persistedPresent -and $providedPresent -and $persisted -ne $provided) {
    throw 'The supplied PostgreSQL password conflicts with the persisted value'
}
if ($persistedPresent) {
    $password = $persisted
} else {
    $password = if ($providedPresent) {
        $provided
    } else {
        $subscription = [string]$values.AZURE_SUBSCRIPTION_ID
        $environment = [string]$values.AZURE_ENV_NAME
        if ([string]::IsNullOrEmpty($subscription) -or [string]::IsNullOrEmpty($environment)) {
            throw 'Cannot establish the selected Azure subscription and environment before generating a password'
        }
        $deployments = @(
            az deployment sub list `
                --subscription $subscription `
                --query "[?tags.'azd-env-name' == '$environment']" `
                --output json | ConvertFrom-Json
        )
        if ($LASTEXITCODE -ne 0) {
            throw 'Cannot verify existing azd deployments; password generation is blocked'
        }
        $servers = @(
            az resource list `
                --subscription $subscription `
                --tag "azd-env-name=$environment" `
                --resource-type 'Microsoft.DBforPostgreSQL/flexibleServers' `
                --output json | ConvertFrom-Json
        )
        if ($LASTEXITCODE -ne 0) {
            throw 'Cannot verify existing PostgreSQL resources; password generation is blocked'
        }
        if ($deployments.Count -ne 0 -or $servers.Count -ne 0) {
            throw 'Existing infrastructure has no local password; restore the original SPRING_DATABASE_PASSWORD'
        }
        $bytes = [byte[]]::new(24)
        [Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
        'AzD-' + [Convert]::ToHexString($bytes).Substring(0, 24) + '-p9!'
    }
}

if ($password.Length -lt 8 -or $password.Length -gt 128 -or
    $password -cnotmatch '[A-Z]' -or $password -cnotmatch '[a-z]' -or
    $password -notmatch '[0-9]' -or $password -notmatch '[^A-Za-z0-9]' -or
    $password -match "['`"\\]") {
    throw 'SPRING_DATABASE_PASSWORD does not meet the PostgreSQL password requirements'
}

azd env set SPRING_DATABASE_TYPE $mode | Out-Null
if (-not $persistedPresent) {
    azd env set SPRING_DATABASE_PASSWORD $password | Out-Null
}
