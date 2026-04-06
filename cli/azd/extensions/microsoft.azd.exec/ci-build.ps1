param(
    [string] $Version = (Get-Content "$PSScriptRoot/../version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [string] $OutputFileName
)
$PSNativeCommandArgumentPassing = 'Legacy'

# Remove any previously built binaries
go clean

if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

# Run `go help build` to obtain detailed information about `go build` flags.
$buildFlags = @(
    # remove all file system paths from the resulting executable.
    "-trimpath",

    # Use buildmode=pie (Position Independent Executable) for enhanced security.
    "-buildmode=pie"
)

if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

# Build constraint tags
$tagsFlag = "-tags=cfi,cfg,osusergo"

# ld linker flags
$ldFlag = "-ldflags=-s -w -X 'microsoft.azd.exec/internal/cmd.Version=$Version'"

if ($IsWindows) {
    Write-Host "Building for Windows"
}
elseif ($IsLinux) {
    Write-Host "Building for linux"
}
elseif ($IsMacOS) {
    Write-Host "Building for macOS"
}

# Add output file flag based on specified output file name
$outputFlag = "-o=$OutputFileName"

# collect flags
$buildFlags += @(
    $tagsFlag,
    $ldFlag,
    $outputFlag
)

function PrintFlags() {
    param(
        [string] $flags
    )

    $i = 0
    foreach ($buildFlag in $buildFlags) {
        $argWithValue = $buildFlag.Split('=', 2)
        if ($argWithValue.Length -eq 2 -and !$argWithValue[1].StartsWith("`"")) {
            $buildFlag = "$($argWithValue[0])=`"$($argWithValue[1])`""
        }

        if ($i -eq $buildFlags.Length - 1) {
            Write-Host "  $buildFlag"
        }
        else {
            Write-Host "  $buildFlag ``"
        }
        $i++
    }
}

Write-Host "Running: go build ``"
PrintFlags -flags $buildFlags
go build @buildFlags
if ($LASTEXITCODE) {
    Write-Host "Error running go build"
    exit $LASTEXITCODE
}

Write-Host "go build succeeded"
