param(
    [string] $Timeout = '60m',
    [string] $CoverageFileOut = 'cover.out'
)

go test -timeout $Timeout -v -coverprofile $CoverageFileOut ./...
