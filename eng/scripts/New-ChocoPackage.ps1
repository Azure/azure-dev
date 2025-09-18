param(
    [Parameter(Mandatory = $true)]
    [string] $Version,

    [string] $Tag = "azure-dev-cli_$Version"
)

$originalLocation = Get-Location
try {
    Set-Location "$PSScriptRoot../../../cli/installer/choco"

    # Copy text files to tools directory for inclusion in the package
    Copy-Item -Path "$PSScriptRoot/../../NOTICE.txt" -Destination 'tools/NOTICE.txt'
    Copy-Item -Path "$PSSCriptRoot/../../LICENSE" -Destination 'tools/LICENSE.txt'

    choco pack .\azd.nuspec VERSION=$Version TAG=$Tag
} finally {
    Set-Location $originalLocation
}
