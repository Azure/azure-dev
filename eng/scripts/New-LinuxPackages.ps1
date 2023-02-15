param(
    $Version = $env:CLI_VERSION
)

# Iterate over entire item
$PACKAGE_TYPES = 'deb', 'rpm'

$originalLocation = Get-Location
try { 
    Set-Location "$PSScriptRoot/../../cli/installer/fpm"
    $currentPath = (Get-Location).Path

    if (!(Test-Path "./azd-linux-amd64")) { 
        Write-Error "Cannot find azd-linux-amd64"
    }
    Copy-Item "$PSScriptRoot/../../NOTICE.txt" "NOTICE.txt"
    Copy-Item "$PSScriptRoot/../../LICENSE" "LICENSE"

    # Symlink points to potentially invalid location but will point correctly 
    # once package is installed
    ln -s /opt/microsoft/azd/azd-linux-amd64 azd
    chmod +x azd
    chmod +x azd-linux-amd64

    foreach ($type in $PACKAGE_TYPES) { 
        docker run -v "$($currentPath):/work" -t fpm `
            --force `
            --output-type $type `
            --version $Version `
            --architecture amd64 `
            azd-linux-amd64=/opt/microsoft/azd/azd-linux-amd64 `
            azd=/usr/local/bin/azd `
            NOTICE.txt=/opt/microsoft/azd/NOTICE.txt `
            LICENSE=/opt/microsoft/azd/LICENSE
        
        if ($LASTEXITCODE) { 
            Write-Host "Error building package type: $type"
            exit $LASTEXITCODE
        }
    }

} finally { 
    Set-Location $originalLocation
}
