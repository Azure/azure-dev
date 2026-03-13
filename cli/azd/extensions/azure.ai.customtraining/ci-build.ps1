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

$buildFlags = @(
    "-trimpath",
    "-buildmode=pie"
)

if ($CodeCoverageEnabled) {
    $buildFlags += "-cover"
}

$tagsFlag = "-tags=cfi,cfg,osusergo"

$ldFlag = "-ldflags=-s -w -X azure.ai.customtraining/internal/cmd.Version=$Version -X azure.ai.customtraining/internal/cmd.Commit=$SourceVersion -X azure.ai.customtraining/internal/cmd.BuildDate=$(Get-Date -Format o) "

if ($IsWindows) {
    Write-Host "Building for Windows"
}
elseif ($IsLinux) {
    Write-Host "Building for linux"
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

    Write-Host "go build succeeded"
}
finally {
    $env:GOEXPERIMENT = $oldGOEXPERIMENT
}
