param(
    [string] $Version = (Get-Content "$PSScriptRoot/../version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode
)

# Remove any previously built binaries
go clean

if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

# On Windows, use the goversioninfo tool to embed the version information into the executable.
if ($IsWindows) {
    Write-Host "Windows build, set version info and run 'go generate'"
    if (! (Get-Command "goversioninfo" -ErrorAction SilentlyContinue)) {
        Write-Host "goversioninfo not found, installing"
        go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0
        Get-Command "goversioninfo" -ErrorAction Stop
    }

    $VERSION_INFO_PATH = "$PSScriptRoot/versioninfo.json"

    $exeFileVersion = ."$PSScriptRoot/../../eng/scripts/Get-MsiVersion.ps1" -CliVersion $Version
    $splitExeFileVersion = $exeFileVersion -split '\.'
    $versionInfo = Get-Content $VERSION_INFO_PATH | ConvertFrom-Json

    $versionInfo.FixedFileInfo.FileVersion.Major = [int]$splitExeFileVersion[0]
    $versionInfo.FixedFileInfo.FileVersion.Minor = [int]$splitExeFileVersion[1]
    $versionInfo.FixedFileInfo.FileVersion.Patch = [int]$splitExeFileVersion[2]
    $versionInfo.FixedFileInfo.FileVersion.Build = 0

    # Product verison is the same as the file version
    $versioninfo.FixedFileInfo.ProductVersion = $versionInfo.FixedFileInfo.FileVersion

    $versionInfo.StringFileInfo.ProductVersion = $Version

    $versionInfoJson = ConvertTo-Json $versionInfo -Depth 10
    Set-Content $VERSION_INFO_PATH $versionInfoJson
    Write-Host "go generate"
    go generate
    if ($LASTEXITCODE) {
        Write-Host "Error running go generate"
        exit $LASTEXITCODE
    }
    Write-Host "go generate succeeded"
}

# Run `go help build` to obtain detailed information about `go build` flags.
$buildFlags = @(
    # Remove file system paths from the compiled binary
    "-gcflags=-trimpath",
    # Remove file system paths from the assembled code
    "-asmflags=-trimpath"
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
$ldFlag = "-ldflags=`"-s -w -X 'github.com/azure/azure-dev/cli/azd/internal.Version=$Version (commit $SourceVersion)' "

if ($IsWindows) {
    Write-Host "Building for windows"
    $buildFlags += @(
        "-buildmode=exe",
        # remove all file system paths from the resulting executable.
        # Instead of absolute file system paths, the recorded file names
        # will begin either a module path@version (when using modules),
        # or a plain import path (when using the standard library, or GOPATH).
        "-trimpath",
        $tagsFlag,
        # -extldflags=-Wl,--high-entropy-va: Pass the high-entropy VA flag to the linker to enable high entropy virtual addresses
        ($ldFlag + "-linkmode=auto -extldflags=-Wl,--high-entropy-va`"")
    )
}
elseif ($IsLinux) {
    Write-Host "Building for linux"
    $buildFlags += @(
        "-buildmode=pie",
        ($tagsFlag + ",cfgo"),
        # -extldflags=-Wl,--high-entropy-va: Pass the high-entropy VA flag to the linker to enable high entropy virtual addresses
        ($ldFlag + "-extldflags=-Wl,--high-entropy-va`"")
    )
}
elseif ($IsMacOS) {
    Write-Host "Building for macOS"
    $buildFlags += @(
        "-buildmode=pie",
        ($tagsFlag + ",cfgo"),
        # -linkmode=auto: Link Go object files and C object files together
        ($ldFlag + "-linkmode=auto`"")
    )
}

function PrintFlags() {
    param(
        [string] $flags
    )

    # Attempt to format flags so that they are easily copy-pastable to be ran inside pwsh
    $i = 0
    foreach ($buildFlag in $buildFlags) {
        # If the flag has a value, wrap it in quotes. This is not required when invoking directly below,
        # but when repasted into a shell for execution, the quotes can help escape special characters such as ','.
        $argWithValue = $buildFlag -split "="
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

Write-Host "Running: go build ``"
PrintFlags -flags $buildFlags
go build @buildFlags

if ($BuildRecordMode) {
    $recordFlagPresent = $false
    for ($i = 0; $i -lt $buildFlags.Length; $i++) {
        if ($buildFlags[$i].StartsWith("-tags=")) {
            $recordFlagPresent = $true
            $buildFlags[$i] += ",record"
        }
    }

    if (-not $recordFlagPresent) {
        $buildFlags[$i] += "-tags=record"
    }

    $outputFlag = "-o=azd-record"
    if ($IsWindows) {
        $outputFlag += ".exe"
    }
    $buildFlags += $outputFlag

    Write-Host "Running: go build (record) ``"
    PrintFlags -flags $buildFlags
    go build @buildFlags
}

if ($LASTEXITCODE) {
    Write-Host "Error running go build"
    exit $LASTEXITCODE
}
Write-Host "go build succeeded"

if ($IsWindows) {
    Write-Host "Windows exe file version info"
    $azdExe = Get-Item azd.exe
    Write-Host "File Version: $($azdExe.VersionInfo.FileVersionRaw)"
    Write-Host "Product Version: $($azdExe.VersionInfo.ProductVersionRaw)"
}