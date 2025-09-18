param(
    $Version = $env:CLI_VERSION,
    $PackageTypes = @('deb', 'rpm'),
    $Architecture = 'amd64'
)

$originalLocation = Get-Location
try { 
    Set-Location "$PSScriptRoot/../../cli/installer/fpm"
    $currentPath = (Get-Location).Path

    if (!(Test-Path "./azd-linux-$Architecture")) { 
        Write-Error "Cannot find azd-linux-$Architecture"
        exit 1
    }
    Copy-Item "$PSScriptRoot/../../NOTICE.txt" "NOTICE.txt"
    Copy-Item "$PSScriptRoot/../../LICENSE" "LICENSE"

    # Symlink points to potentially invalid location but will point correctly 
    # once package is installed
    ln -s /opt/microsoft/azd/azd-linux-$Architecture azd
    chmod +x azd-linux-$Architecture

    foreach ($type in $PackageTypes) { 
        docker run -v "$($currentPath):/work" -t fpm `
            --force `
            --output-type $type `
            --version $Version `
            --architecture $Architecture `
            --after-install install-notice.sh `
            --after-remove uninstall.sh `
            azd-linux-$Architecture=/opt/microsoft/azd/azd-linux-$Architecture `
            azd=/usr/local/bin/azd `
            NOTICE.txt=/opt/microsoft/azd/NOTICE.txt `
            LICENSE=/opt/microsoft/azd/LICENSE `
            installed-by-$type.txt=/opt/microsoft/azd/.installed-by.txt
        
        if ($LASTEXITCODE) { 
            Write-Host "Error building package type: $type"
            exit $LASTEXITCODE
        }
    }

} finally { 
    Set-Location $originalLocation
}
