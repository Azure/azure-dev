param(
    [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string[]] $VersionPackages,
    [string] $Version,
    [string] $SourceVersion,
    [switch] $VersionOnly,
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $OutputFileName
)

$ErrorActionPreference = 'Stop'

go clean
if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

$goOS, $goArch = go env GOOS GOARCH
if ($LASTEXITCODE) {
    Write-Host "Error running go env GOOS GOARCH"
    exit $LASTEXITCODE
}

# The linker silently ignores -X for missing symbols. Use the packages the binary actually reads.
$buildDate = Get-Date -Format o
$ldFlags = @("-s", "-w") # Strip the symbol table and DWARF debug information.
foreach ($versionPath in $VersionPackages) {
    $ldFlags += "-X '$versionPath.Version=$Version'"
    if (-not $VersionOnly) {
        $ldFlags += @(
            "-X '$versionPath.Commit=$SourceVersion'",
            "-X '$versionPath.BuildDate=$buildDate'"
        )
    }
}

$buildFlags = @(
    "-trimpath",
    # PIE preserves ASLR, including Windows DYNAMICBASE and HIGH-ENTROPY-VA.
    "-buildmode=pie",
    "-ldflags=$($ldFlags -join ' ')"
)
if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

function PrintFlags {
    param([string[]] $Flags)

    for ($i = 0; $i -lt $Flags.Count; $i++) {
        $flag = $Flags[$i]
        $argWithValue = $flag.Split('=', 2)
        if ($argWithValue.Length -eq 2) {
            $flag = "$($argWithValue[0])=`"$($argWithValue[1])`""
        }
        if ($i -eq $Flags.Count - 1) {
            Write-Host "  $flag"
        }
        else {
            Write-Host "  $flag ``"
        }
    }
}

$oldCGOEnabled = $env:CGO_ENABLED
try {
    # Avoid the build agent's GLIBC dependency on Linux amd64. Other targets keep
    # the caller's settings, including macOS ARM64 builds that require cgo.
    if ($goOS -eq "linux" -and $goArch -eq "amd64") {
        $env:CGO_ENABLED = "0"
    }

    Write-Host "Building for $goOS/$goArch"
    # Keep the release tags; osusergo selects the pure-Go user lookup.
    $productionFlags = $buildFlags + @("-tags=cfi,cfg,osusergo", "-o=$OutputFileName")
    Write-Host "Running: go build ``"
    PrintFlags -Flags $productionFlags
    go build @productionFlags
    if ($LASTEXITCODE) {
        Write-Host "Error running go build"
        exit $LASTEXITCODE
    }

    if ($BuildRecordMode) {
        $recordOutput = $OutputFileName
        if ($goOS -eq "windows") {
            # Keep .exe last rather than producing <name>.exe-record.exe.
            if ($recordOutput.EndsWith(".exe", [StringComparison]::OrdinalIgnoreCase)) {
                $recordOutput = $recordOutput.Substring(0, $recordOutput.Length - 4)
            }
            $recordOutput += "-record.exe"
        }
        else {
            $recordOutput += "-record"
        }
        $recordFlags = $buildFlags + @("-tags=cfi,cfg,osusergo,record", "-o=$recordOutput")
        Write-Host "Running: go build (record) ``"
        PrintFlags -Flags $recordFlags
        go build @recordFlags
        if ($LASTEXITCODE) {
            Write-Host "Error running go build (record)"
            exit $LASTEXITCODE
        }
    }

    Write-Host "go build succeeded"
}
finally {
    $env:CGO_ENABLED = $oldCGOEnabled
}
