param(
    [string] $Version = (Get-Content "$PSScriptRoot/../version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # path to msys2_shell.cmd
    [string] $GitHubCopilotClientId,
    [string] $GitHubCopilotIntegrationId
)
$PSNativeCommandArgumentPassing = 'Legacy'

# Validate GitHub Copilot parameters - both must be provided together or not at all
$GitHubCopilotEnabled = $false
if ($GitHubCopilotClientId -or $GitHubCopilotIntegrationId) {
    if ([string]::IsNullOrWhiteSpace($GitHubCopilotClientId)) {
        Write-Host "Error: GitHubCopilotClientId parameter is required when enabling GitHub Copilot integration" -ForegroundColor Red
        Write-Host "Usage: -GitHubCopilotClientId 'your-client-id' -GitHubCopilotIntegrationId 'your-integration-id'" -ForegroundColor Yellow
        exit 1
    }
    if ([string]::IsNullOrWhiteSpace($GitHubCopilotIntegrationId)) {
        Write-Host "Error: GitHubCopilotIntegrationId parameter is required when enabling GitHub Copilot integration" -ForegroundColor Red
        Write-Host "Usage: -GitHubCopilotClientId 'your-client-id' -GitHubCopilotIntegrationId 'your-integration-id'" -ForegroundColor Yellow
        exit 1
    }
    $GitHubCopilotEnabled = $true
    Write-Host "GitHub Copilot integration enabled with ClientId: $GitHubCopilotClientId and IntegrationId: $GitHubCopilotIntegrationId" -ForegroundColor Green
}

# specifying $MSYS2Shell implies building with OneAuth integration
$OneAuth = $MSYS2Shell.length -gt 0 -and $IsWindows

# Remove any previously built binaries
go clean

if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

if ($OneAuth) {
    Write-Host "Building OneAuth bridge DLL"
    # TODO: could have multiple VS installs
    $results = Get-ChildItem "$env:ProgramFiles\Microsoft Visual Studio" -Recurse -Filter 'Launch-VsDevShell.ps1'
    if (!$results) {
        Write-Host "Launch-VsDevShell.ps1 not found, can't build OneAuth bridge DLL"
        exit 1
    }
    . $results[0].FullName -SkipAutomaticLocation
    $bridgeDir = "$pwd/pkg/oneauth/bridge"
    cmake --preset=default -S"$bridgeDir" -B"$bridgeDir/_build"
    if ($LASTEXITCODE -eq 0) {
        cmake --build "$bridgeDir/_build" --config Release --verbose
    }
    if ($LASTEXITCODE) {
        Write-Host "Error running cmake"
        exit $LASTEXITCODE
    }

    # TODO: move this to a setup script that installs MSYS2
    Write-Host "Installing required MSYS2 packages"
    Invoke-Expression "$($MSYS2Shell) -mingw64 -defterm -no-start -c 'pacman -S --needed --noconfirm mingw-w64-x86_64-toolchain'"
    if ($LASTEXITCODE) {
        Write-Host "Error installing MSYS2 packages"
        exit $LASTEXITCODE
    }
}

# On Windows, use the goversioninfo tool to embed the version information into the executable.
if ($IsWindows) {
    Write-Host "Windows build, set version info and run 'go generate'"
    if (! (Get-Command "goversioninfo" -ErrorAction SilentlyContinue)) {
        Write-Host "goversioninfo not found, installing"
        go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0

        try {
            Get-Command "goversioninfo" -ErrorAction Stop
        } catch {
            Write-Host "Could not find goversioninfo after installing"
            Write-Host "Environment PATH: $env:PATH"
            Get-ChildItem -Path (Join-Path (go env GOPATH) "bin") | ForEach-Object { Write-Host $_.FullName }
        }
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
# ghCopilot: Enable GitHub Copilot integration (when parameters provided)
$tags = @("cfi", "cfg", "osusergo")
if ($GitHubCopilotEnabled) {
    $tags += "ghCopilot"
}
$tagsFlag = "-tags=$($tags -join ',')"

# ld linker flags
# -s: Omit symbol table and debug information
# -w: Omit DWARF symbol table
# -X: Set variable at link time. Used to set the version in source.
$ldFlags = @(
    "-s",
    "-w",
    "-X 'github.com/azure/azure-dev/cli/azd/internal.Version=$Version (commit $SourceVersion)'"
)

# Add GitHub Copilot linker flags if enabled
if ($GitHubCopilotEnabled) {
    $ldFlags += "-X 'github.com/azure/azure-dev/cli/azd/pkg/llm.clientID=$GitHubCopilotClientId'"
    $ldFlags += "-X 'github.com/azure/azure-dev/cli/azd/pkg/llm.copilotIntegrationID=$GitHubCopilotIntegrationId'"
}

$ldFlag = "-ldflags=$($ldFlags -join ' ')"

if ($IsWindows) {
    $msg = "Building for Windows"
    if ($OneAuth) {
        $msg += " with OneAuth integration"
        $tags += "oneauth"
        $tagsFlag = "-tags=$($tags -join ',')"
    }
    if ($GitHubCopilotEnabled) {
        $msg += " with GitHub Copilot integration"
    }
    Write-Host $msg
}
elseif ($IsLinux) {
    $msg = "Building for Linux"
    if ($GitHubCopilotEnabled) {
        $msg += " with GitHub Copilot integration"
    }
    Write-Host $msg
}
elseif ($IsMacOS) {
    $msg = "Building for macOS"
    if ($GitHubCopilotEnabled) {
        $msg += " with GitHub Copilot integration"
    }
    Write-Host $msg
}

# collect flags
$buildFlags += @(
    $tagsFlag,
    $ldFlag
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
$env:GOEXPERIMENT="loopvar"

try {
    Write-Host "Running: go build ``"
    PrintFlags -flags $buildFlags
    if ($OneAuth) {
        # write the go build command line to a script because that's simpler than trying
        # to escape the build flags, which contain commas and both kinds of quotes
        Set-Content -Path build.sh -Value "go build $($buildFlags)"
        Invoke-Expression "$($MSYS2Shell) -mingw64 -defterm -no-start -here -c 'bash ./build.sh'"
        Remove-Item -Path build.sh -ErrorAction Ignore
    }
    else {
        go build @buildFlags
    }
    if ($BuildRecordMode) {
        $recordFlagPresent = $false
        for ($i = 0; $i -lt $buildFlags.Length; $i++) {
            if ($buildFlags[$i].StartsWith("-tags=")) {
                $recordFlagPresent = $true
                $buildFlags[$i] += ",record"
            }
        }
    
        if (-not $recordFlagPresent) {
            $recordTags = $tags + @("record")
            $buildFlags += "-tags=$($recordTags -join ',')"
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
} finally {
    $env:GOEXPERIMENT = $oldGOEXPERIMENT    
}