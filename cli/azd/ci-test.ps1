param(
    [string] $Timeout = '90m',
    [string] $CoverageFileOut = 'cover.out',
    [string] $Package = './...',
    [switch] $ShortMode
)

$goTest = "$(go env GOPATH)/bin/gotestsum -- -timeout $Timeout -v -coverprofile='$CoverageFileOut' $Package"

if ($ShortMode) {
    $goTest = $goTest + " -short"
}

Invoke-Expression $goTest
