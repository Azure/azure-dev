param(
    $PackageTypes = @('deb', 'rpm'),
    $DockerImagePrefix = ''
)

$originalLocation = Get-Location 
try {
    Set-Location "$PSSCriptRoot/../../cli/installer/fpm"
    $currentPath = (Get-Location).Path

    foreach ($type in $PackageTypes) { 
        docker build . -f "test-$type.Dockerfile" -t test-linux-package --build-arg prefix="$DockerImagePrefix"
        if ($LASTEXITCODE) { 
            Write-Host "Error building test container for type: $type"
            exit 1
        }

        docker run `
            -v "$($currentPath):/work" `
            -t test-linux-package

        if ($LASTEXITCODE) { 
            Write-Host "Error running test container for type: $type"
            exit 1
        }
    }
} finally { 
    Set-Location $originalLocation
}