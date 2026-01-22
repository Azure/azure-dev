Write-Host "Running unit tests..."
go test ./... -v -count=1 2>&1 | Tee-Object -Variable testOutput

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "Tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "All tests passed!" -ForegroundColor Green
exit 0