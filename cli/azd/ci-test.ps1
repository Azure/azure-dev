param(
    [string] $Timeout = '15m'
)

go test -timeout $Timeout -v -coverprofile=cover.out ./...