$ErrorActionPreference = 'Stop'

$values = azd env get-values --output json | ConvertFrom-Json
if ([string]$values.SPRING_DATABASE_TYPE -ne 'postgresql') {
    return
}
$appId = [string]$values.AZURE_CONTAINER_APP_ID
$server = [string]$values.AZURE_POSTGRES_SERVER_NAME
$resourceGroup = [string]$values.AZURE_RESOURCE_GROUP
$subscription = [string]$values.AZURE_SUBSCRIPTION_ID
if (-not $appId -or -not $server -or -not $resourceGroup -or -not $subscription) {
    throw 'Provisioned Container App or PostgreSQL identity is missing'
}

$appName = ($appId -split '/')[-1].ToLowerInvariant()
$hash = [Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($appName))
$prefix = 'azd-springboot-' + [Convert]::ToHexString($hash[0..3]).ToLowerInvariant() + '-'
$ips = @(az containerapp show --ids $appId --query properties.outboundIpAddresses -o tsv)
if ($ips.Count -eq 0) {
    throw 'The Container App reports no outbound IP addresses'
}

$desired = @{}
foreach ($ip in $ips) {
    if (-not [Net.IPAddress]::TryParse($ip, [ref]([Net.IPAddress]$null))) {
        throw 'The Container App returned an invalid outbound IP address'
    }
    $name = $prefix + $ip.Replace('.', '-')
    $desired[$name] = $ip
    az postgres flexible-server firewall-rule create `
        --subscription $subscription --resource-group $resourceGroup --name $server `
        --rule-name $name --start-ip-address $ip --end-ip-address $ip --output none
}

$owned = @(az postgres flexible-server firewall-rule list `
    --subscription $subscription --resource-group $resourceGroup --name $server `
    --query "[?starts_with(name, '$prefix')].name" -o tsv)
foreach ($name in $owned) {
    if (-not $desired.ContainsKey($name)) {
        az postgres flexible-server firewall-rule delete `
            --subscription $subscription --resource-group $resourceGroup --name $server `
            --rule-name $name --yes --output none
    }
}
