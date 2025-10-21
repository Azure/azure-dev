param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # path to msys2_shell.cmd
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
    # Instead of absolute file system paths, the recorded file names
    # will begin either a module path@version (when using modules),
    # or a plain import path (when using the standard library, or GOPATH).
    "-trimpath",

    # Use buildmode=pie (Position Independent Executable) for enhanced security across platforms
    # against memory corruption exploits across all major platforms.
    #
    # On Windows, the -buildmode=pie flag enables Address Space Layout 
    # Randomization (ASLR) and automatically sets DYNAMICBASE and HIGH-ENTROPY-VA flags in the PE header.
    "-buildmode=pie"
)

if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

# Build constraint tags
# cfi: Enable Control Flow Integrity (CFI),
# cfg: Enable Control Flow Guard (CFG),
# osusergo: Optimize for OS user accounts
$tagsFlag = "-tags=cfi,cfg,osusergo"

# ld linker flags
# -s: Omit symbol table and debug information
# -w: Omit DWARF symbol table
# -X: Set variable at link time. Used to set the version in source.

# TODO: set version properly
$ldFlag = "-ldflags=-s -w -X 'azurecodingagent/internal/cmd.Version=$Version (commit $SourceVersion)' "

if ($IsWindows) {
    $msg = "Building for Windows"
    Write-Host $msg
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

    # Attempt to format flags so that they are easily copy-pastable to be ran inside pwsh
    $i = 0
    foreach ($buildFlag in $buildFlags) {
        # If the flag has a value, wrap it in quotes. This is not required when invoking directly below,
        # but when repasted into a shell for execution, the quotes can help escape special characters such as ','.
        $argWithValue = $buildFlag.Split('=', 2)
        if ($argWithValue.Length -eq 2 -and !$argWithValue[1].StartsWith("`"")) {
            $buildFlag = "$($argWithValue[0])=`"$($argWithValue[1])`""
        }

        # Write each flag on a newline with '`' acting as the multiline separator
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
# Enable the loopvar experiment, which makes the loop variaible for go loops like `range` behave as most folks would expect.
# the go team is exploring making this default in the future, and we'd like to opt into the behavior now.
$env:GOEXPERIMENT = "loopvar"

try {
    Write-Host "Running: go build ``"
    PrintFlags -flags $buildFlags
    go build @buildFlags
    if ($LASTEXITCODE) {
        Write-Host "Error running go build"
        exit $LASTEXITCODE
    }

    if ($BuildRecordMode) {
        # Modify build tags to include record
        $recordTagPatched = $false
        for ($i = 0; $i -lt $buildFlags.Length; $i++) {
            if ($buildFlags[$i].StartsWith("-tags=")) {
                $buildFlags[$i] += ",record"
                $recordTagPatched = $true
            }
        }
        if (-not $recordTagPatched) {
            $buildFlags += "-tags=record"
        }
        # Add output file flag for record mode
        $recordOutput = "-o=$OutputFileName-record"
        if ($IsWindows) { $recordOutput += ".exe" }
        $buildFlags += $recordOutput

        Write-Host "Running: go build (record) ``"
        PrintFlags -flags $buildFlags
        go build @buildFlags
        if ($LASTEXITCODE) {
            Write-Host "Error running go build (record)"
            exit $LASTEXITCODE
        }
    }

    Write-Host "go build succeeded"
}
finally {
    $env:GOEXPERIMENT = $oldGOEXPERIMENT
}
