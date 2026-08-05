Write-Host "Running unit tests..."
go test ./... -count=1

if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

exit 0
