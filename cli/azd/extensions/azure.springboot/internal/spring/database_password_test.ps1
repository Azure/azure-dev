# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$hook = Join-Path $PSScriptRoot "template/.azd-spring/prepare-database-password.ps1"
$pendingProcesses = [System.Collections.Generic.List[object]]::new()
$startedProcesses = [System.Collections.Generic.List[object]]::new()

function Assert-Condition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) {
        throw $Message
    }
}

function New-TestProcess {
    param([string]$Output, [int]$ExitCode = 0, [bool]$TimedOut = $false)
    $process = @{
        StartInfo = $null
        StandardOutput = [System.IO.StringReader]::new($Output)
        StandardError = [System.IO.StringReader]::new("sensitive-error-value")
        ExitCode = $ExitCode
        TimedOut = $TimedOut
        Killed = $false
        Disposed = $false
    }
    $process | Add-Member ScriptMethod Start { return $true }
    $process | Add-Member ScriptMethod WaitForExit {
        param([int]$Milliseconds)
        if ($Milliseconds) {
            return -not $this.TimedOut
        }
    }
    $process | Add-Member ScriptMethod Kill {
        param([bool]$EntireProcessTree)
        $this.Killed = $EntireProcessTree
    }
    $process | Add-Member ScriptMethod Dispose {
        $this.StandardOutput.Dispose()
        $this.StandardError.Dispose()
        $this.Disposed = $true
    }
    return $process
}

function Get-Command {
    [CmdletBinding()]
    param([string]$Name, [string]$CommandType)
    Assert-Condition ($Name -eq "azd" -and $CommandType -eq "Application") "Unexpected executable lookup"
    return @{ Source = "fake azd" }
}

function New-Object {
    [CmdletBinding()]
    param([string]$TypeName)
    if ($TypeName -eq "System.Diagnostics.ProcessStartInfo") {
        return [System.Diagnostics.ProcessStartInfo]::new()
    }
    Assert-Condition ($TypeName -eq "System.Diagnostics.Process") "Unexpected process type"
    Assert-Condition ($pendingProcesses.Count -gt 0) "Unexpected azd invocation"
    $process = $pendingProcesses[0]
    $pendingProcesses.RemoveAt(0)
    $startedProcesses.Add($process)
    return $process
}

function Invoke-PasswordHook {
    param(
        [string]$Values = "{}",
        [int]$ReadExitCode = 0,
        [int]$WriteExitCode = 0,
        [bool]$ReadTimeout = $false,
        [bool]$WriteTimeout = $false
    )
    $pendingProcesses.Clear()
    $startedProcesses.Clear()
    $pendingProcesses.Add((New-TestProcess $Values $ReadExitCode $ReadTimeout))
    $pendingProcesses.Add((New-TestProcess "sensitive-command-output" $WriteExitCode $WriteTimeout))
    $output = ""
    $failure = ""
    try {
        $output = & $hook 6>&1 | Out-String
    } catch [System.Management.Automation.RuntimeException] {
        $failure = Get-Error -InputObject $_ | Out-String
    } finally {
        foreach ($process in $pendingProcesses) {
            $process.Dispose()
        }
    }
    foreach ($process in $startedProcesses) {
        Assert-Condition $process.Disposed "Every started process must be disposed"
        Assert-Condition (-not $process.StartInfo.UseShellExecute) "Commands must not use a shell"
        Assert-Condition $process.StartInfo.RedirectStandardOutput "Command stdout must be captured"
        Assert-Condition $process.StartInfo.RedirectStandardError "Command stderr must be captured"
        Assert-Condition ($process.StartInfo.WorkingDirectory -eq (Join-Path $PSScriptRoot "template")) `
            "Commands must run in the generated project root"
    }
    return @{ Output = $output; Failure = $failure; Processes = @($startedProcesses.ToArray()) }
}

$result = Invoke-PasswordHook
Assert-Condition ([string]::IsNullOrEmpty($result.Failure)) "Password generation must succeed"
Assert-Condition ($result.Processes.Count -eq 2) "A missing password must be saved exactly once"
Assert-Condition (($result.Processes[0].StartInfo.ArgumentList -join " ") -eq "env get-values --output json") `
    "Environment values must be read as JSON"
$arguments = $result.Processes[1].StartInfo.ArgumentList
Assert-Condition (($arguments[0..2] -join " ") -eq "env set SPRING_DATABASE_PASSWORD") "Unexpected password binding"
Assert-Condition ($arguments.Count -eq 4) "The password must be one distinct command argument"
$password = $arguments[3]
Assert-Condition ($password.Length -ge 43 -and $password.Length -le 128) "Password length must fit Azure database limits"
foreach ($pattern in @("[a-z]", "[A-Z]", "[0-9]", "[^a-zA-Z0-9]")) {
    Assert-Condition ($password -cmatch $pattern) "Password must include every required character class"
}
Assert-Condition ($result.Output -notlike "*$password*") "Generated passwords must not be printed"
Assert-Condition ($result.Output -notlike "*sensitive-*") "Command output must not be printed"

$second = Invoke-PasswordHook -Values '{"SPRING_DATABASE_PASSWORD":""}'
Assert-Condition ([string]::IsNullOrEmpty($second.Failure)) "An empty password must be replaced"
Assert-Condition ($second.Processes.Count -eq 2) "An empty password must be saved"
Assert-Condition ($second.Processes[1].StartInfo.ArgumentList[3] -ne $password) "New passwords must use fresh randomness"

$existing = Invoke-PasswordHook -Values '{"SPRING_DATABASE_PASSWORD":"existing-synthetic-value"}'
Assert-Condition ([string]::IsNullOrEmpty($existing.Failure)) "An existing password must be accepted"
Assert-Condition ($existing.Processes.Count -eq 1) "An existing password must not be overwritten"
Assert-Condition ($existing.Output -notlike "*existing-synthetic-value*") "Existing passwords must not be printed"

foreach ($case in @(
    @{ ReadExitCode = 1 },
    @{ WriteExitCode = 1 },
    @{ ReadTimeout = $true },
    @{ WriteTimeout = $true },
    @{ Values = "sensitive-invalid-json" },
    @{ Values = "[]" }
)) {
    $result = Invoke-PasswordHook @case
    Assert-Condition (-not [string]::IsNullOrEmpty($result.Failure)) "Invalid or failed commands must fail preparation"
    Assert-Condition ($result.Failure -like "*azd env*") "Errors must identify the failed operation"
    Assert-Condition ($result.Failure -notlike "*sensitive-*") "Errors must not disclose captured output"
    Assert-Condition ($result.Output -notlike "*Password prepared*") "Failures must not report success"
    foreach ($process in $result.Processes) {
        if ($process.StartInfo.ArgumentList.Count -eq 4 -and $process.StartInfo.ArgumentList[1] -eq "set") {
            $credential = $process.StartInfo.ArgumentList[3]
            Assert-Condition ($result.Failure -notlike "*$credential*") "Errors must not disclose command arguments"
        }
        if ($process.TimedOut) {
            Assert-Condition $process.Killed "Timed-out process trees must be stopped"
            Assert-Condition ($result.Failure -like "*timed out*") "Timeout errors must be explicit"
        }
    }
}

Write-Host "Database password preparation tests passed."
