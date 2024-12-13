param(
    [string] $BaseUrl = 'https://azd-release-gfgac2cmf7b8cuay.b02.azurefd.net/azd/standalone/release',
    [string] $Version = 'latest',
    [string] $InstallShScriptUrl = 'https://aka.ms/install-azd.sh',
    [string] $UninstallShScriptUrl = 'https://aka.ms/uninstall-azd.sh'
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

& $PSScriptRoot/install-azd.ps1 -BaseUrl $BaseUrl -Version $Version -InstallShScriptUrl $InstallShScriptUrl
assertSuccessfulExecution "Install failed"

try {
    $azdCommand = Get-Command 'azd'
    Write-Host "File info for $($azdCommand.Source)"
    ls -lah $azdCommand.Source

    & azd version
    assertSuccessfulExecution "Could not execute azd"
} catch {
    Write-Error "Could not run 'azd version': $_"
    exit 1
}

& $PSScriptRoot/uninstall-azd.ps1 -UninstallShScriptUrl $UninstallShScriptUrl
assertSuccessfulExecution "Uninstall failed"

if (Get-Command 'azd' -ErrorAction Ignore) {
    Write-Error "azd command still accessible"
    exit 1
}

Write-Host "Test succeeded"
exit 0
