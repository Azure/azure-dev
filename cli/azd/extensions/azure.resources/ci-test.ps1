$ErrorActionPreference = 'Stop'

go test ./... -count=1
exit $LASTEXITCODE
