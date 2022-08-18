param(
    [string] $Timeout = '90m',
    [string] $CoverageFileOut = 'cover.out'
)

go test -timeout $Timeout -v -coverprofile $CoverageFileOut ./...
