$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

if ([string]::IsNullOrWhiteSpace($env:AZD_CONFIG_DIR)) {
    throw "Package acceptance requires an isolated AZD_CONFIG_DIR."
}

azd version
if ($LASTEXITCODE -ne 0) {
    throw "Failed to read the installed azd version."
}

$extensionIds = @("azure.ai.evaluations", "azure.ai.dataset")
foreach ($extensionId in $extensionIds) {
    azd extension install $extensionId --source azd --no-prompt
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to install $extensionId from the canonical azd registry."
    }
}

$installedJson = azd extension list --installed --source azd --output json
if ($LASTEXITCODE -ne 0) {
    throw "Failed to list installed extensions."
}
$installed = @(($installedJson -join [Environment]::NewLine) | ConvertFrom-Json)
foreach ($extensionId in $extensionIds) {
    $matches = @($installed | Where-Object { $_.id -ceq $extensionId })
    if ($matches.Count -ne 1) {
        throw "Expected exactly one installed $extensionId package."
    }
    $package = $matches[0]
    if ($package.source -cne "azd" -or
        [string]::IsNullOrWhiteSpace($package.installedVersion) -or
        $package.installedVersion -cne $package.version) {
        throw "The installed $extensionId package is not the latest canonical version."
    }
    Write-Host "$extensionId $($package.installedVersion) (source: azd)"
}

azd ai eval version --output json | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "The installed evaluation extension did not start."
}
azd ai dataset version --output json | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "The installed dataset extension did not start."
}
