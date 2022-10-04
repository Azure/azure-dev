param(
    [string] $BaseUrl = 'https://azure-dev.azureedge.net/azd/standalone/release',
    [string] $Version = 'latest',
    [string] $InstallFolder = "$($env:USERPROFILE)\azd-install-test"
)

function assertSuccessfulExecution($errorMessage) {
    if ($LASTEXITCODE -or !$?) {
        Write-Error $errorMessage
        if ($LASTEXITCODE) {
            exit $LASTEXITCODE
        }
        exit 1
    }
}

$regKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
$originalPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$originalPathType = $regKey.GetValueKind('PATH')

& $PSScriptRoot/install-azd.ps1 `
    -BaseUrl $BaseUrl `
    -Version $Version `
    -InstallFolder $InstallFolder `
    -Verbose
assertSuccessfulExecution "Install failed. Last exit code: $LASTEXITCODE"

$currentPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$expectedPathEntry = $InstallFolder

if (!$currentPath.Contains($expectedPathEntry)) {
  Write-Error "Could not find path entry"
  Write-Error "Expected substring: $expectedPathEntry"
  Write-Error "Actual: $path"
  exit 1
}

$afterInstallPathType = $regKey.GetValueKind('PATH')
if ($originalPathType -ne $afterInstallPathType) {
    Write-Error "Path registry key type does not match"
    Write-Error "Expected: $originalPathType"
    Write-Error "Actual: $afterInstallPathType"
    exit 1
}

& $InstallFolder/azd version
assertSuccessfulExecution "Could not execute 'azd version'"

& $PSScriptRoot/uninstall-azd.ps1 -InstallFolder $InstallFolder -Verbose
assertSuccessfulExecution "Uninstall failed"

$currentPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$afterUninstallPathType = $regKey.GetValueKind('PATH')

if ($currentPath -ne $originalPath) {
    Write-Error "Path does not match original path after uninstall"
    Write-Error "Expected: $originalPath"
    Write-Error "Actual: $currentPath"
    exit 1
}

if ($originalPathType -ne $afterUninstallPathType) {
    Write-Error "Path registry key type does not match"
    Write-Error "Expected: $originalPathType"
    Write-Error "Actual: $afterUninstallPathType"
    exit 1
}

$azdCommand = Get-Command azd -ErrorAction Ignore
if ($azdCommand) {
    $sourceFolder = Split-Path -Parent (Resolve-Path $azdCommand.Source)
    if ($sourceFolder -eq $InstallFolder) {
        Write-Error "Command still availble in tested install location"
        exit 1
    }
}

Write-Host "Test succeeded"
exit 0
