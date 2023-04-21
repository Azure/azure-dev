param(
    [string] $Timeout = '90m',
    [string] $Package = './...',
    [switch] $ShortMode,
    [string] $UnitTestCoverageDir = 'cover-unit',
    [string] $IntegrationTestCoverageDir = 'cover-int'
)

$unitCoverDir = New-Item -ItemType Directory -Force -Path $UnitTestCoverageDir
$intCoverDir = New-Item -ItemType Directory -Force -Path $IntegrationTestCoverageDir

# GOCOVERDIR enables any binaries (in this case, azd.exe) built with '-cover',
# to write out coverage files to the specified directory.
$env:GOCOVERDIR = $intCoverDir.FullName

$goTest = "$(go env GOPATH)/bin/gotestsum -- -cover -timeout $Timeout -v $Package"

if ($ShortMode) {
    $goTest = $goTest + " -short"
}

# --test.gocoverdir is currently a "under-the-cover" way to pass the coverage directory to a test binary
# See https://github.com/golang/go/issues/51430#issuecomment-1344711300
#
# This may be improved in go1.21 with an official 'go test' flag.
$goTest += " -args --test.gocoverdir='$($unitCoverDir.FullName)'"

Invoke-Expression $goTest
