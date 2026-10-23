$ErrorActionPreference = 'Stop'

$gopath = go env GOPATH
$gotestsumBinary = "gotestsum"
if ($IsWindows) {
    $gotestsumBinary += ".exe"
}
$gotestsum = Join-Path $gopath "bin" $gotestsumBinary

Write-Host "Running unit tests..."

if (Test-Path $gotestsum) {
    & $gotestsum --format testname -- ./... -count=1
} else {
    Write-Host "gotestsum not found, using go test..." -ForegroundColor Yellow
    go test ./... -v -count=1
}

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "Tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "Running local Content Understanding client unit tests..."
Push-Location (Join-Path $PSScriptRoot "cu-rest-client")
try {
    if (Test-Path $gotestsum) {
        & $gotestsum --format testname -- ./... -count=1
    } else {
        go test ./... -v -count=1
    }
    if ($LASTEXITCODE -ne 0) {
        Write-Host ""
        Write-Host "Client tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}

if ($env:AZURE_DEV_CI_OS -in @('win', 'mac')) {
    if ([string]::IsNullOrWhiteSpace($env:BUILDTARGET) -or [string]::IsNullOrWhiteSpace($env:EXT_VERSION)) {
        throw "Installed-host smoke requires the pipeline's BUILDTARGET and EXT_VERSION."
    }
    Write-Host ""
    Write-Host "Running offline installed-host smoke..."
    & "$PSScriptRoot/ci-smoke.ps1" -BinaryPath (Join-Path $PSScriptRoot $env:BUILDTARGET) -Version $env:EXT_VERSION
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}

Write-Host ""
Write-Host "All tests passed!" -ForegroundColor Green
exit 0
