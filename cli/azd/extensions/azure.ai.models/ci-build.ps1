param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # path to msys2_shell.cmd
    [string] $OutputFileName
)

# Remove any previously built binaries
go clean

if ($LASTEXITCODE) {
    Write-Host "Error running go clean"
    exit $LASTEXITCODE
}

# Run `go help build` to obtain detailed information about `go build` flags.
$buildFlags = @(
    "-trimpath",
    "-buildmode=pie"
)

if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

$tagsFlag = "-tags=cfi,cfg,osusergo"

$ldFlag = "-ldflags=-s -w -X 'azure.ai.models/internal/cmd.Version=$Version' -X 'azure.ai.models/internal/cmd.Commit=$SourceVersion' -X 'azure.ai.models/internal/cmd.BuildDate=$(Get-Date -Format o)' "

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

$oldGOEXPERIMENT = $env:GOEXPERIMENT
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
