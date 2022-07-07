param(
    [string] $Timeout = '15m'
)

go test -timeout $Timeout -v ./...