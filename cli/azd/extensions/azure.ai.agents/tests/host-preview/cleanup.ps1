# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

function Complete-PreviewCleanup {
    param(
        [Parameter(Mandatory)][string] $Directory,
        [System.Management.Automation.ErrorRecord] $PrimaryError
    )

    $failures = @()
    # Native go commands and the host fixture wait for their child processes.
    # Retry at the filesystem boundary for transient Windows executable locks.
    foreach ($name in @('preview-tests.exe', 'project-preview-tests.exe', 'project-preview-result.json', 'overlay.json', '')) {
        $path = if ($name) { Join-Path $Directory $name } else { $Directory }
        for ($attempt = 0; $attempt -le 10; $attempt++) {
            try {
                if (Test-Path -LiteralPath $path -ErrorAction Stop) {
                    if ($name) {
                        Remove-Item -LiteralPath $path -Force -ErrorAction Stop
                    } else {
                        [System.IO.Directory]::Delete($path)
                    }
                }
                break
            } catch {
                $exception = $_.Exception
                while ($exception.InnerException) { $exception = $exception.InnerException }
                $transient = $exception -is [System.IO.IOException] -or
                    $exception -is [System.UnauthorizedAccessException]
                if ($transient -and $attempt -lt 10) {
                    Start-Sleep -Seconds 1
                    continue
                }
                $failures += "${path}: $($_.Exception.Message)"
                break
            }
        }
    }
    if ($failures.Count -gt 0) {
        $message = "preview cleanup failed; retained artifacts at ${Directory}: " + ($failures -join '; ')
        if ($PrimaryError) {
            Write-Warning -Message $message -WarningAction Continue
        } else {
            throw $message
        }
    }
}
