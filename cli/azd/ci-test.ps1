param(
    [string] $Timeout = '15m',
    [string] $CoverageFileOut = 'cover.out'
)

go test -timeout $Timeout -v -coverprofile $CoverageFileOut ./...