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

$originalErrorActionPreference = $ErrorActionPreference

try {
    $ErrorActionPreference = "Continue"
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

    & $InstallFolder/azd version
    assertSuccessfulExecution "Could not execute 'azd version'"

    Write-Host "Test succeeded"
    exit 0
} finally { 
    $ErrorActionPreference = $originalErrorActionPreference
}