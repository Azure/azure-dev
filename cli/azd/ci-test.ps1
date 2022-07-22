param(
    [string] $Timeout = '20m'
)

go test -timeout $Timeout -v ./...