param(
    [string] $BaseUrl = 'https://azd-release-gfgac2cmf7b8cuay.b02.azurefd.net/azd/standalone/release',
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

    if (!((Get-Content "$InstallFolder/.installed-by.txt") -eq 'install-azd.ps1')) {
        Write-Error ".installed-by.txt does not contain expected value"
        exit 1
    }

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