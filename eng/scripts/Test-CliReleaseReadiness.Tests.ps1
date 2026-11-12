Set-StrictMode -Version 4

Describe 'Test-CliReleaseReadiness' {
    BeforeEach {
        $testDirectory = Join-Path $TestDrive 'release-metadata'
        New-Item -ItemType Directory -Path $testDirectory -Force | Out-Null

        $cliVersionPath = Join-Path $testDirectory 'version.txt'
        $changeLogPath = Join-Path $testDirectory 'CHANGELOG.md'
        $azdExtVersionPath = Join-Path $testDirectory 'version.go'

        Set-Content -Path $cliVersionPath -Value '1.2.3'
        Set-Content -Path $changeLogPath -Value @'
# Release History

## 1.2.3 (2026-09-21)

### Features Added

- Added release readiness validation.
'@
        Set-Content -Path $azdExtVersionPath -Value 'const Version = "1.2.3"'

        $scriptArguments = @{
            CliVersionPath    = $cliVersionPath
            ChangeLogPath     = $changeLogPath
            AzdExtVersionPath = $azdExtVersionPath
        }
    }

    It 'accepts a dated changelog version matching version.txt and version.go' {
        { & $PSScriptRoot/Test-CliReleaseReadiness.ps1 @scriptArguments } | Should -Not -Throw
    }

    It 'rejects a changelog version without a release date' {
        (Get-Content -Path $changeLogPath -Raw).Replace('(2026-09-21)', '(Unreleased)') |
            Set-Content -Path $changeLogPath

        { & $PSScriptRoot/Test-CliReleaseReadiness.ps1 @scriptArguments } |
            Should -Throw '*is not release-ready*'
    }

    It 'rejects a version.txt version that does not match the changelog version' {
        Set-Content -Path $cliVersionPath -Value '1.2.2'

        { & $PSScriptRoot/Test-CliReleaseReadiness.ps1 @scriptArguments } |
            Should -Throw "*CLI version '1.2.2' does not match the latest changelog version '1.2.3'*"
    }

    It 'rejects a version.go version that does not match the changelog version' {
        Set-Content -Path $azdExtVersionPath -Value 'const Version = "1.2.2"'

        { & $PSScriptRoot/Test-CliReleaseReadiness.ps1 @scriptArguments } |
            Should -Throw "*does not match azdext SDK version '1.2.2'*"
    }
}
