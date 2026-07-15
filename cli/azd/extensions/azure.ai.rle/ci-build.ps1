param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # path to msys2_shell.cmd
    [string] $OutputFileName
)

$PSNativeCommandArgumentPassing = 'Legacy'

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

$buildFlags += @(
    "-tags=cfi,cfg,osusergo",
    "-ldflags=-s -w -X azure.ai.rle/internal/cmd.Version=$Version -X azure.ai.rle/internal/cmd.Commit=$SourceVersion -X azure.ai.rle/internal/cmd.BuildDate=$(Get-Date -Format o) ",
    "-o=$OutputFileName"
)

function PrintFlags() {
    foreach ($buildFlag in $buildFlags) {
        Write-Host "  $buildFlag"
    }
}

$oldGOEXPERIMENT = $env:GOEXPERIMENT
$env:GOEXPERIMENT = "loopvar"

try {
    Write-Host "Running: go build"
    PrintFlags
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
        $recordOutput = "-o=$OutputFileName-record"
        if ($IsWindows) { $recordOutput += ".exe" }
        $buildFlags += $recordOutput

        Write-Host "Running: go build (record)"
        PrintFlags
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
