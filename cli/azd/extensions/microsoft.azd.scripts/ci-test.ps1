Write-Host "Running tests..."

go test ./... -short -v
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}

Write-Host "Tests passed."
exit 0
