param(
    [switch] $ShortMode,
    [string] $UnitTestCoverageDir = 'cover-unit',
    [string] $IntegrationTestTimeout = '120m',
    [string] $IntegrationTestCoverageDir = 'cover-int'
)

$ErrorActionPreference = 'Stop'

$gopath = go env GOPATH
if ($LASTEXITCODE) {
    throw "go env GOPATH failed with exit code: $LASTEXITCODE, stdout: $gopath"
}

$gotestsumBinary = "gotestsum"
if ($IsWindows) {
    $gotestsumBinary += ".exe"
}

$gotestsum = Join-Path $gopath "bin" $gotestsumBinary
if (-not (Test-Path $gotestsum)) {
    throw "gotestsum is not installed at $gotestsum"
}

function New-EmptyDirectory {
    param([string]$Path) 
    if (Test-Path $Path) {
        Remove-Item -Force -Recurse $Path | Out-Null
    }
    
    New-Item -ItemType Directory -Force -Path $Path
}
$unitCoverDir = New-EmptyDirectory -Path $UnitTestCoverageDir
Write-Host "Running unit tests..."

# --test.gocoverdir is currently a "under-the-cover" way to pass the coverage directory to a test binary
# See https://github.com/golang/go/issues/51430#issuecomment-1344711300
#
# This may be improved in go1.21 with an official 'go test' flag.
& $gotestsum -- ./... -short -v -cover -args --test.gocoverdir="$($unitCoverDir.FullName)"
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}

if ($ShortMode) {
    Write-Host "Short mode, skipping integration tests"
    exit 0
}

Write-Host "Running integration tests..."
$intCoverDir = New-EmptyDirectory -Path $IntegrationTestCoverageDir

$oldGOCOVERDIR = $env:GOCOVERDIR
$oldGOEXPERIMENT = $env:GOEXPERIMENT

# GOCOVERDIR enables any binaries (in this case, azd.exe) built with '-cover',
# to write out coverage output to the specific directory.
$env:GOCOVERDIR = $intCoverDir.FullName
# Enable the loopvar experiment, which makes the loop variaible for go loops like `range` behave as most folks would expect.
# the go team is exploring making this default in the future, and we'd like to opt into the behavior now.
$env:GOEXPERIMENT="loopvar"

try {
    & $gotestsum -- ./test/... -v -timeout $IntegrationTestTimeout
    if ($LASTEXITCODE) {
        exit $LASTEXITCODE
    }    
} finally {
    $env:GOCOVERDIR = $oldGOCOVERDIR
    $env:GOEXPERIMENT = $oldGOEXPERIMENT
}