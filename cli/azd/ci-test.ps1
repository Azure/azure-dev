param(
    [string] $Timeout = '90m',
    [string] $CoverageFileOut = 'cover.out',
    [string] $Package = './...'
)

Invoke-Expression "$(go env GOPATH)/bin/gotestsum -- -coverprofile='$CoverageFileOut' $Package"
