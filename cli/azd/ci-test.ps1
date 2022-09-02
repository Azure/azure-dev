param(
    [string] $Timeout = '90m',
    [string] $CoverageFileOut = 'cover.out',
    [string] $Package = './...'
)

Invoke-Expression "$(go env GOPATH)/bin/gotestsum -- -timeout $Timeout -v -coverprofile='$CoverageFileOut' $Package"
