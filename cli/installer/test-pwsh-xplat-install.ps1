param(
    [string] $BaseUrl = 'https://azure-dev.azureedge.net/azd/standalone/release',
    [string] $Version = 'latest'
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

& $PSScriptRoot/install-azd.ps1 -BaseUrl $BaseUrl -Version $Version
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

& $PSScriptRoot/uninstall-azd.ps1
assertSuccessfulExecution "Uninstall failed"

if (Get-Command 'azd' -ErrorAction Ignore) {
    Write-Error "azd command still accessible"
    exit 1
}

Write-Host "Test succeeded"
exit 0
