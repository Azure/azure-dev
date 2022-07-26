param(
    [string] $Timeout = '20m',
    [string] $CoverageFileOut = 'cover.out'
)

go test -timeout $Timeout -v -coverprofile $CoverageFileOut ./...
