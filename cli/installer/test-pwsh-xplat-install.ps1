parm(
    [string] $BaseUrl,
    [string] $Version = ''
)

& $PSScriptRoot/install-azd.ps1 -BaseUrl $BaseUrl -Version $Version

if ($LASTEXITCODE) {
    Write-Error "Install failed"
    exit $LASTEXITCODE
}

azd version

if ($LASTEXITCODE) {
    Write-Error "Could not execute azd"
    exit $LASTEXITCODE
}

& $PSScriptRoot/uninstall-azd.ps1

if  ($LASTEXITCODE) {
    Write-Error "Uninstall failed"
    exit $LASTEXITCODE
}

if (Get-Command 'azd' -ErrorAction Ignore) {
    Write-Error "azd command still accessible"
    exit 1
}
