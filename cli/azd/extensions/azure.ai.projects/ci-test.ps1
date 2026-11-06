$gopath = go env GOPATH
$gotestsumBinary = "gotestsum"
if ($IsWindows) {
    $gotestsumBinary += ".exe"
}
$gotestsum = Join-Path $gopath "bin" $gotestsumBinary

Write-Host "Running unit tests..."

if (Test-Path $gotestsum) {
    # Use gotestsum for better output formatting and summary
    & $gotestsum --format testname -- ./... -count=1
} else {
    # Fallback to go test if gotestsum is not installed
    Write-Host "gotestsum not found, using go test..." -ForegroundColor Yellow
    go test ./... -v -count=1
}

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "Tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
    exit $LASTEXITCODE
}

# synthesis is a nested Go module, so ./... above does not include it.
Write-Host "Running synthesis module tests..."
Push-Location (Join-Path $PSScriptRoot "synthesis")
try {
    go test ./... -count=1
    $synthesisExit = $LASTEXITCODE
} finally {
    Pop-Location
}

if ($synthesisExit -ne 0) {
    Write-Host ""
    Write-Host "Synthesis tests failed with exit code: $synthesisExit" -ForegroundColor Red
    exit $synthesisExit
}

Write-Host ""
Write-Host "All tests passed!" -ForegroundColor Green
exit 0
