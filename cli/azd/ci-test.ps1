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

# Using -coverprofile flag introduced in Go 1.21 for modern coverage collection
# This replaces the older --test.gocoverdir approach and provides better integration
# with the standard Go toolchain for coverage reporting.
$unitCoverProfile = Join-Path $unitCoverDir.FullName "coverage.out"
& $gotestsum -- ./... -short -v -coverprofile="$unitCoverProfile"
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
# to write out coverage output to the specific coverage directory.
# This works in conjunction with the -coverprofile flag for comprehensive coverage reporting.
$env:GOCOVERDIR = $intCoverDir.FullName
# Set any experiment flags that are needed for the tests.
$env:GOEXPERIMENT=""

try {
    $intCoverProfile = Join-Path $intCoverDir.FullName "coverage.out"
    & $gotestsum -- ./... -v -timeout $IntegrationTestTimeout -coverprofile="$intCoverProfile"
    if ($LASTEXITCODE) {
        exit $LASTEXITCODE
    }    
} finally {
    $env:GOCOVERDIR = $oldGOCOVERDIR
    $env:GOEXPERIMENT = $oldGOEXPERIMENT
}