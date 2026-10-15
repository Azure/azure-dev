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
Write-Host "Running database password preparation tests..."
& (Join-Path $PSScriptRoot "internal/spring/database_password_test.ps1")
if (-not $?) {
    exit 1
}

Write-Host ""
Write-Host "Running database bootstrap tests..."
python (Join-Path $PSScriptRoot "internal/spring/database_bootstrap_test.py")
if ($LASTEXITCODE -ne 0) {
    Write-Host "Database bootstrap tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "All tests passed!" -ForegroundColor Green
exit 0
