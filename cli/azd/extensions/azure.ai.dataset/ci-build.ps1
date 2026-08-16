param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    # Accepted because the shared CI template always passes it. This extension
    # has no record/playback mode, so there is no second binary to produce.
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # path to msys2_shell.cmd
    [string] $OutputFileName
)
$PSNativeCommandArgumentPassing = 'Legacy'

# Remove any previously built binaries.
go clean

if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

# Run `go help build` for detail on these flags.
$buildFlags = @(
    # Remove file system paths from the binary. Recorded file names become a
    # module path@version, or a plain import path for the standard library.
    "-trimpath",

    # Position Independent Executable, for memory-corruption hardening across
    # platforms. On Windows this enables ASLR and sets DYNAMICBASE and
    # HIGH-ENTROPY-VA in the PE header.
    "-buildmode=pie"
)

if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

# cfi: Control Flow Integrity, cfg: Control Flow Guard,
# osusergo: use the pure Go user lookup.
$tagsFlag = "-tags=cfi,cfg,osusergo"

# -s: omit the symbol table, -w: omit DWARF, -X: set a variable at link time.
# The path has to match this module, azureaidataset: the linker discards -X for
# a symbol that does not exist, so a stale one leaves the binary reporting dev.
$ldFlag = "-ldflags=-s -w " +
    "-X 'azureaidataset/internal/version.Version=$Version' " +
    "-X 'azureaidataset/internal/version.Commit=$SourceVersion' " +
    "-X 'azureaidataset/internal/version.BuildDate=$(Get-Date -Format o)' "

if ($IsWindows) {
    Write-Host "Building for Windows"
}
elseif ($IsLinux) {
    Write-Host "Building for linux"

    # Disable cgo for the x64 Linux build. This also links statically, which
    # widens compatibility with older Linux distributions.
    if ($env:GOARCH -ne "arm64") {
        $env:CGO_ENABLED = "0"
    }
}
elseif ($IsMacOS) {
    Write-Host "Building for macOS"
}

$outputFlag = "-o=$OutputFileName"

$buildFlags += @(
    $tagsFlag,
    $ldFlag,
    $outputFlag
)

function PrintFlags() {
    param(
        [string] $flags
    )

    # Format the flags so they can be pasted straight into pwsh.
    $i = 0
    foreach ($buildFlag in $buildFlags) {
        # Quote values so characters such as ',' survive a repaste. Not needed
        # for the direct invocation below.
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

$oldGOEXPERIMENT = $env:GOEXPERIMENT
# Opt into per-iteration loop variables, which is what most readers expect and
# what the Go team intends to make the default.
$env:GOEXPERIMENT = "loopvar"

try {
    Write-Host "Running: go build ``"
    PrintFlags -flags $buildFlags
    go build @buildFlags
    if ($LASTEXITCODE) {
        Write-Host "Error running go build"
        exit $LASTEXITCODE
    }

    Write-Host "go build succeeded"
}
finally {
    $env:GOEXPERIMENT = $oldGOEXPERIMENT
}
