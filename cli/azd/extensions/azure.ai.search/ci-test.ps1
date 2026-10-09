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
Write-Host "All tests passed!" -ForegroundColor Green
exit 0
