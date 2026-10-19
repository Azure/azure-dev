# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Invoke-Azd {
    param([string[]]$Arguments)

    $executable = Get-Command azd -CommandType Application -ErrorAction Stop
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $executable.Source
    $start.WorkingDirectory = Split-Path $PSScriptRoot -Parent
    $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    foreach ($argument in $Arguments) {
        $start.ArgumentList.Add($argument)
    }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) {
            throw "Could not start azd env; diagnose the operation locally"
        }
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(120000)) {
            $process.Kill($true)
            $process.WaitForExit()
            throw "azd env timed out; diagnose the operation locally"
        }
        $output = $stdout.GetAwaiter().GetResult()
        [void]$stderr.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) {
            throw "azd env failed; diagnose the operation locally"
        }
        return $output
    } finally {
        $process.Dispose()
    }
}

$output = Invoke-Azd -Arguments @("env", "get-values", "--output", "json")
try {
    $environment = ConvertFrom-Json -InputObject $output -AsHashtable -ErrorAction Stop
} catch [System.ArgumentException] {
    throw "azd env get-values returned invalid JSON; diagnose the operation locally"
}
if ($environment -isnot [System.Collections.IDictionary]) {
    throw "azd env get-values must return a JSON object; diagnose the operation locally"
}
if ([string]::IsNullOrEmpty([string]$environment["SPRING_DATABASE_PASSWORD"])) {
    $bytes = [byte[]]::new(32)
    $random = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
    } finally {
        $random.Dispose()
    }
    # Meet the database password character-class requirements without reducing randomness.
    $password = "Aa0!" + [Convert]::ToBase64String($bytes).TrimEnd("=").Replace("+", "-").Replace("/", "_")
    [void](Invoke-Azd -Arguments @("env", "set", "SPRING_DATABASE_PASSWORD", $password))
}
Write-Host "Password prepared in local azd state; do not commit .azure files."
