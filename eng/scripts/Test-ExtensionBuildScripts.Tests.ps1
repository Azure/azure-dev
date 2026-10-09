# cspell:ignore ldflags trimpath buildmode osusergo GOEXPERIMENT

BeforeDiscovery {
    $extensionRoot = Join-Path $PSScriptRoot '../../cli/azd/extensions'
    $metadata = @{
        'azure.ai.agents' = @('azureaiagent/internal/version')
        'azure.ai.connections' = @('azure.ai.connections/internal/version')
        'azure.ai.dataset' = @('azureaidataset/internal/version')
        'azure.ai.evaluations' = @('azureaieval/internal/version')
        'azure.ai.finetune' = @('azure.ai.finetune/internal/version')
        'azure.ai.inspector' = @('azureaiinspector/internal/version')
        'azure.ai.models' = @('azure.ai.models/internal/cmd')
        'azure.ai.projects' = @('azure.ai.projects/internal/version')
        'azure.ai.rle' = @('azure.ai.rle/internal/cmd')
        'azure.ai.routines' = @('azure.ai.routines/internal/cmd')
        'azure.ai.skills' = @('azureaiskills/internal/version')
        'azure.ai.toolboxes' = @('azure.ai.toolboxes/internal/cmd', 'azure.ai.toolboxes/internal/version')
        'azure.ai.training' = @('azure.ai.training/internal/cmd')
        'azure.appservice' = @('azureappservice/internal/version')
        'azure.coding-agent' = @('azurecodingagent/internal/cmd')
        'azure.logicappsstandard' = @('azurelogicappsstandard/internal/version')
        'microsoft.azd.ai.builder' = @(
            'github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/cmd'
        )
        'microsoft.azd.concurx' = @('concurx/internal/cmd')
        'microsoft.azd.demo' = @('github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.demo/internal/cmd')
        'microsoft.azd.extensions' = @(
            'github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/cmd'
        )
    }
    $scripts = @(Get-ChildItem $extensionRoot -Directory | ForEach-Object {
        $path = Join-Path $_.FullName 'ci-build.ps1'
        if ($_.Name -ne 'scripts' -and (Test-Path $path)) {
            if (-not $metadata.ContainsKey($_.Name)) {
                throw "Add linker metadata expectations for extension '$($_.Name)'."
            }
            @{
                Name = $_.Name
                ScriptPath = $path
                Packages = $metadata[$_.Name]
                VersionOnly = $_.Name -eq 'azure.appservice'
                CombinedVersion = $_.Name -eq 'azure.coding-agent'
            }
        }
    })
}

BeforeAll {
    $sharedScriptPath = Join-Path $PSScriptRoot '../../cli/azd/extensions/scripts/ci-build.ps1'
    $hostOS, $hostArch = go env GOHOSTOS GOHOSTARCH
    if ($LASTEXITCODE) {
        throw "Resolving the Go host target failed with exit code $LASTEXITCODE."
    }
    $fixtureDirectory = Join-Path $TestDrive 'native-go'
    New-Item -ItemType Directory -Path $fixtureDirectory | Out-Null
    $goSource = Join-Path $fixtureDirectory 'main.go'
    Set-Content $goSource @'
package main

import (
    "encoding/json"
    "fmt"
    "os"
    "runtime"
    "strings"
)

func main() {
    cgo, cgoSet := os.LookupEnv("CGO_ENABLED")
    log, err := os.OpenFile(os.Getenv("TEST_GO_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil {
        panic(err)
    }
    defer log.Close()
    err = json.NewEncoder(log).Encode(struct {
        Args []string
        Cgo string
        CgoSet bool
        Experiment string
    }{os.Args[1:], cgo, cgoSet, os.Getenv("GOEXPERIMENT")})
    if err != nil {
        panic(err)
    }
    phase := os.Args[1]
    if phase == "build" {
        for _, arg := range os.Args[2:] {
            if strings.HasPrefix(arg, "-tags=") && strings.Contains(arg, ",record") {
                phase = "record"
            }
        }
    }
    if phase == os.Getenv("TEST_GO_FAILURE") {
        fmt.Fprintln(os.Stderr, "requested failure:", phase)
        os.Exit(17)
    }
    if phase == "env" {
        goOS := os.Getenv("GOOS")
        if goOS == "" {
            goOS = runtime.GOOS
        }
        goArch := os.Getenv("GOARCH")
        if goArch == "" {
            goArch = runtime.GOARCH
        }
        fmt.Println(goOS)
        fmt.Println(goArch)
    }
}
'@
    $goExecutable = Join-Path $fixtureDirectory $(if ($IsWindows) { 'go.exe' } else { 'go' })
    # A native executable catches argument splitting that a PowerShell function mock cannot.
    go build -o $goExecutable $goSource
    if ($LASTEXITCODE) {
        throw "Building the native Go test fixture failed with exit code $LASTEXITCODE."
    }
    $driver = Join-Path $fixtureDirectory 'invoke.ps1'
    Set-Content $driver @'
param([string] $SettingsPath)
$ErrorActionPreference = 'Stop'
$settings = Get-Content $SettingsPath -Raw | ConvertFrom-Json -AsHashtable
$PSNativeCommandArgumentPassing = $settings.ArgumentMode
$arguments = @{
    SourceVersion = 'source revision'
    OutputFileName = $settings.Output
    CodeCoverageEnabled = $settings.Coverage
    BuildRecordMode = $settings.Record
}
if (-not $settings.DefaultVersion) {
    $arguments.Version = '1.2.3-test'
}
if ($settings.Shared) {
    $arguments.VersionPackages = @('example/internal/version')
}
try {
    & $settings.ScriptPath @arguments
    $exitCode = $LASTEXITCODE
}
finally {
    @{
        Cgo = $env:CGO_ENABLED
        CgoSet = Test-Path Env:CGO_ENABLED
        Experiment = $env:GOEXPERIMENT
    } | ConvertTo-Json | Set-Content $settings.StatePath
}
exit $exitCode
'@

    function Invoke-BuildScript {
        param(
            [string] $ScriptPath,
            [string] $TargetOS = 'linux',
            [string] $TargetArch = 'amd64',
            [AllowNull()][AllowEmptyString()][string] $Cgo = '1',
            [switch] $UnsetCgo,
            [switch] $Record,
            [switch] $Coverage,
            [switch] $DefaultVersion,
            [string] $Failure = '',
            [string] $ArgumentMode = 'Standard',
            [string] $Output = 'output folder/extension'
        )

        $runDirectory = Join-Path $TestDrive ([guid]::NewGuid().ToString())
        New-Item -ItemType Directory -Path $runDirectory | Out-Null
        $settingsPath = Join-Path $runDirectory 'settings.json'
        $statePath = Join-Path $runDirectory 'state.json'
        $logPath = Join-Path $runDirectory 'calls.jsonl'
        @{
            ScriptPath = $ScriptPath
            StatePath = $statePath
            Output = $Output
            Record = [bool]$Record
            Coverage = [bool]$Coverage
            DefaultVersion = [bool]$DefaultVersion
            ArgumentMode = $ArgumentMode
            Shared = $ScriptPath -eq $sharedScriptPath
        } | ConvertTo-Json | Set-Content $settingsPath

        $start = [System.Diagnostics.ProcessStartInfo]::new((Get-Process -Id $PID).Path)
        $start.UseShellExecute = $false
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        foreach ($argument in @('-NoProfile', '-File', $driver, '-SettingsPath', $settingsPath)) {
            $start.ArgumentList.Add($argument)
        }
        $start.Environment['PATH'] = $fixtureDirectory + [IO.Path]::PathSeparator + $env:PATH
        $start.Environment['TEST_GO_LOG'] = $logPath
        $start.Environment['TEST_GO_FAILURE'] = $Failure
        $start.Environment['GOEXPERIMENT'] = 'rangefunc'
        foreach ($entry in @{ GOOS = $TargetOS; GOARCH = $TargetArch }.GetEnumerator()) {
            if ($entry.Value) {
                $start.Environment[$entry.Key] = $entry.Value
            }
            else {
                $start.Environment.Remove($entry.Key) | Out-Null
            }
        }
        if ($UnsetCgo) {
            $start.Environment.Remove('CGO_ENABLED') | Out-Null
        }
        else {
            $start.Environment['CGO_ENABLED'] = $Cgo
        }
        $process = [System.Diagnostics.Process]::Start($start)
        try {
            $stdout = $process.StandardOutput.ReadToEndAsync()
            $stderr = $process.StandardError.ReadToEndAsync()
            if (-not $process.WaitForExit(30000)) {
                $process.Kill($true)
                $process.WaitForExit()
                throw "Build script '$ScriptPath' did not finish within 30 seconds."
            }
            @{
                ExitCode = $process.ExitCode
                Stdout = $stdout.GetAwaiter().GetResult()
                Stderr = $stderr.GetAwaiter().GetResult()
                Calls = @(Get-Content $logPath | ForEach-Object { $_ | ConvertFrom-Json })
                State = Get-Content $statePath -Raw | ConvertFrom-Json
            }
        }
        finally {
            $process.Dispose()
        }
    }
}

Describe '<Name> CI build wrapper' -ForEach $scripts {
    It 'triggers its release pipeline when the shared build changes' {
        $pipelineName = "release-ext-$($Name.Replace('.', '-')).yml"
        $pipelinePath = Join-Path $PSScriptRoot "../pipelines/$pipelineName"
        $pipeline = Get-Content $pipelinePath -Raw
        foreach ($trigger in @('trigger', 'pr')) {
            $section = [regex]::Match($pipeline, "(?ms)^${trigger}:.*?(?=^\S|\z)").Value
            $section | Should -Match ([regex]::Escape('cli/azd/extensions/scripts/ci-build.ps1'))
        }
    }

    It 'preserves flags and argument boundaries with <ArgumentMode> callers' -ForEach @(
        @{ ArgumentMode = 'Standard' }
        @{ ArgumentMode = 'Legacy' }
    ) {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -ArgumentMode $ArgumentMode
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $build = @($result.Calls | Where-Object { $_.Args[0] -eq 'build' })
        $build | Should -HaveCount 1
        $build[0].Args | Should -Contain '-trimpath'
        $build[0].Args | Should -Contain '-buildmode=pie'
        $build[0].Args | Should -Contain '-tags=cfi,cfg,osusergo'
        $build[0].Args | Should -Contain '-o=output folder/extension'
        $build[0].Args | Should -Not -Contain '-cover'
        $linker = @($build[0].Args | Where-Object { $_.StartsWith('-ldflags=') })
        $linker | Should -HaveCount 1
        $version = if ($CombinedVersion) { '1.2.3-test (commit source revision)' } else { '1.2.3-test' }
        foreach ($package in $Packages) {
            $linker[0] | Should -Match ([regex]::Escape("-X '$package.Version=$version'"))
            if (-not $VersionOnly) {
                $linker[0] | Should -Match ([regex]::Escape("-X '$package.Commit=source revision'"))
                $linker[0] | Should -Match (
                    "$([regex]::Escape("-X '$package.BuildDate="))" +
                    "\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{7}[+-]\d{2}:\d{2}'"
                )
            }
        }
        if ($VersionOnly) {
            $linker[0] | Should -Not -Match '\.(Commit|BuildDate)='
        }
    }

    It 'reads its own version.txt when no version override is supplied' {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -DefaultVersion
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $version = Get-Content (Join-Path (Split-Path $ScriptPath) 'version.txt')
        if ($CombinedVersion) {
            $version += ' (commit source revision)'
        }
        $build = $result.Calls | Where-Object { $_.Args[0] -eq 'build' }
        $linker = $build.Args | Where-Object { $_.StartsWith('-ldflags=') }
        $linker | Should -Match ([regex]::Escape("$($Packages[0]).Version=$version"))
    }

    It 'forwards coverage and record switches to the shared build' {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -Record -Coverage
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $builds = @($result.Calls | Where-Object { $_.Args[0] -eq 'build' })
        $builds | Should -HaveCount 2
        foreach ($build in $builds) {
            $build.Args | Should -Contain '-cover'
        }
        $builds[1].Args | Should -Contain '-tags=cfi,cfg,osusergo,record'
        $builds[1].Args | Should -Contain '-o=output folder/extension-record'
    }

    It 'propagates shared build failures without losing caller state' {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -Failure record -Record
        $result.ExitCode | Should -Be 17
        $result.Stdout | Should -Not -Match 'go build succeeded'
        $result.State.Cgo | Should -Be '1'
        $result.State.Experiment | Should -Be 'rangefunc'
    }
}

Describe 'Shared extension CI build' {
    BeforeAll {
        $ScriptPath = $sharedScriptPath
    }

    It 'supports independent <Mode> builds' -ForEach @(
        @{ Mode = 'coverage'; Coverage = $true; Record = $false; BuildCount = 1 }
        @{ Mode = 'record'; Coverage = $false; Record = $true; BuildCount = 2 }
    ) {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -Record:$Record -Coverage:$Coverage
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $builds = @($result.Calls | Where-Object { $_.Args[0] -eq 'build' })
        $builds | Should -HaveCount $BuildCount
        foreach ($build in $builds) {
            if ($Coverage) {
                $build.Args | Should -Contain '-cover'
            }
            else {
                $build.Args | Should -Not -Contain '-cover'
            }
        }
        if ($Record) {
            $builds[1].Args | Should -Contain '-tags=cfi,cfg,osusergo,record'
            $builds[1].Args | Should -Contain '-o=output folder/extension-record'
        }
    }

    It 'uses target-specific cgo policy and record names for <TargetOS>/<TargetArch>' -ForEach @(
        @{ TargetOS = 'linux'; TargetArch = 'amd64'; ExpectedCgo = '0'; Suffix = '-record' }
        @{ TargetOS = 'linux'; TargetArch = 'arm64'; ExpectedCgo = '1'; Suffix = '-record' }
        @{ TargetOS = 'darwin'; TargetArch = 'amd64'; ExpectedCgo = '1'; Suffix = '-record' }
        @{ TargetOS = 'darwin'; TargetArch = 'arm64'; ExpectedCgo = '1'; Suffix = '-record' }
        @{ TargetOS = 'windows'; TargetArch = 'amd64'; ExpectedCgo = '1'; Suffix = '-record.exe' }
        @{ TargetOS = 'windows'; TargetArch = 'arm64'; ExpectedCgo = '1'; Suffix = '-record.exe' }
    ) {
        $output = if ($TargetOS -eq 'windows') { 'output folder/extension.exe' } else { 'output folder/extension' }
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -TargetOS $TargetOS -TargetArch $TargetArch `
            -Output $output -Record -Coverage
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $builds = @($result.Calls | Where-Object { $_.Args[0] -eq 'build' })
        $builds | Should -HaveCount 2
        $builds[0].Args | Should -Contain "-o=$output"
        $builds[1].Args | Should -Contain "-o=output folder/extension$Suffix"
        $builds[0].Args | Should -Contain '-tags=cfi,cfg,osusergo'
        $builds[1].Args | Should -Contain '-tags=cfi,cfg,osusergo,record'
        foreach ($build in $builds) {
            @($build.Args | Where-Object { $_.StartsWith('-o=') }) | Should -HaveCount 1
            @($build.Args | Where-Object { $_.StartsWith('-tags=') }) | Should -HaveCount 1
            $build.Args | Should -Contain '-cover'
            $build.Cgo | Should -Be $ExpectedCgo
            $build.Experiment | Should -Be 'rangefunc'
        }
        ($builds[0].Args | Where-Object { $_.StartsWith('-ldflags=') }) |
            Should -Be ($builds[1].Args | Where-Object { $_.StartsWith('-ldflags=') })
        $result.State.Cgo | Should -Be '1'
        $result.State.Experiment | Should -Be 'rangefunc'
    }

    It 'uses Go defaults when target environment variables are unset' {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -TargetOS '' -TargetArch ''
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $result.Calls[1].Args | Should -Be @('env', 'GOOS', 'GOARCH')
        $build = $result.Calls | Where-Object { $_.Args[0] -eq 'build' }
        $build.Cgo | Should -Be $(if ($hostOS -eq 'linux' -and $hostArch -eq 'amd64') { '0' } else { '1' })
    }

    It 'restores unset and empty cgo environment values' -ForEach @(
        @{ Unset = $true }
        @{ Unset = $false }
    ) {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -Cgo '' -UnsetCgo:$Unset
        $result.ExitCode | Should -Be 0 -Because $result.Stderr
        $result.State.CgoSet | Should -Be (-not $Unset)
        $result.State.Cgo | Should -Be $(if ($Unset) { $null } else { '' })
    }

    It 'stops after a <Failure> failure and restores caller state' -ForEach @(
        @{ Failure = 'clean'; ExpectedCalls = 1 }
        @{ Failure = 'env'; ExpectedCalls = 2 }
        @{ Failure = 'build'; ExpectedCalls = 3 }
        @{ Failure = 'record'; ExpectedCalls = 4 }
    ) {
        $result = Invoke-BuildScript -ScriptPath $ScriptPath -Failure $Failure -Record
        $result.ExitCode | Should -Be 17
        $result.Calls | Should -HaveCount $ExpectedCalls
        $result.Stdout | Should -Not -Match 'go build succeeded'
        $result.State.Cgo | Should -Be '1'
        $result.State.Experiment | Should -Be 'rangefunc'
    }
}
