param(
    [string] $Timeout = '30m',
    [string] $CoverageFileOut = 'cover.out'
)

go test -timeout $Timeout -v -coverprofile $CoverageFileOut ./...
