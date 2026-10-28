# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'cleanup.ps1')

Add-Type -TypeDefinition @'
using System.IO;
using System.Threading.Tasks;
public static class PreviewCleanupLock {
    public static async Task Release(FileStream file) {
        await Task.Delay(1500);
        file.Dispose();
    }
}
'@

$directory = Join-Path ([System.IO.Path]::GetTempPath()) ("azd-preview-cleanup-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $directory | Out-Null
$binary = Join-Path $directory 'preview-tests.exe'
Set-Content -LiteralPath $binary -Value 'test artifact'
$lock = [System.IO.File]::Open($binary, 'Open', 'Read', 'None')
$release = [PreviewCleanupLock]::Release($lock)
try {
    Complete-PreviewCleanup -Directory $directory
    if (Test-Path -LiteralPath $directory) { throw 'transient file lock was not retried successfully' }
} finally {
    $null = $release.GetAwaiter().GetResult()
    Complete-PreviewCleanup -Directory $directory
}

foreach ($primaryFailure in @($true, $false)) {
    $directory = Join-Path ([System.IO.Path]::GetTempPath()) ("azd-preview-cleanup-test-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $directory | Out-Null
    $binary = Join-Path $directory 'preview-tests.exe'
    Set-Content -LiteralPath $binary -Value 'test artifact'
    Set-Content -LiteralPath (Join-Path $directory 'overlay.json') -Value '{}'
    $lock = [System.IO.File]::Open($binary, 'Open', 'Read', 'None')
    $primaryError = $null
    $observed = $null
    $warnings = @()
    try {
        try {
            try {
                if ($primaryFailure) { throw 'initiating build failure' }
            } catch {
                $primaryError = $_
                throw
            } finally {
                Complete-PreviewCleanup -Directory $directory -PrimaryError $primaryError -WarningVariable warnings
            }
        } catch {
            $observed = $_
        }
        $expected = if ($primaryFailure) { 'initiating build failure' } else { 'preview cleanup failed' }
        if (-not $observed -or $observed.Exception.Message -notlike "*$expected*") {
            throw "did not preserve expected failure: $expected"
        }
        if ($primaryFailure -and ($warnings -join '; ') -notlike '*preview cleanup failed*') {
            throw 'cleanup failure was not explicitly reported'
        }
        if (Test-Path -LiteralPath (Join-Path $directory 'overlay.json')) {
            throw 'cleanup stopped before removing the other known artifacts'
        }
        if (-not (Test-Path -LiteralPath $binary)) { throw 'locked test artifact unexpectedly removed' }
    } finally {
        $lock.Dispose()
        Complete-PreviewCleanup -Directory $directory
    }
    if (Test-Path -LiteralPath $directory) { throw 'unlocked artifacts were not removed' }
}

$directory = Join-Path ([System.IO.Path]::GetTempPath()) ("azd-preview-cleanup-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $directory | Out-Null
$unexpected = Join-Path $directory 'retain.txt'
Set-Content -LiteralPath $unexpected -Value 'not a runner artifact'
try {
    try {
        Complete-PreviewCleanup -Directory $directory
        throw 'nonempty directory was unexpectedly deleted'
    } catch {
        if ($_.Exception.Message -notlike '*preview cleanup failed*') { throw }
    }
    if (-not (Test-Path -LiteralPath $unexpected)) { throw 'unrecognized file was deleted' }
} finally {
    Remove-Item -LiteralPath $unexpected
    Complete-PreviewCleanup -Directory $directory
}
Write-Host 'Preview cleanup failure and recovery checks passed'
