Set-StrictMode -Version 4

BeforeAll {
    $ExtPackageJsonFile = "$PSScriptRoot../../../ext/vscode/package.json"
    $ExtChangelogFile = "$PSScriptRoot../../../ext/vscode/CHANGELOG.md"

    function SetPackageJsonVersion([string] $version) {
        @{ name = "test-package"; version = $version } `
        | ConvertTo-Json -Depth 100 `
        | Set-Content $ExtPackageJsonFile
    }

    function GetPackageVersion {
        return (Get-Content $ExtPackageJsonFile | ConvertFrom-Json).version
    }

    function ResetChangeLog {
@"
# Change Log

All notable changes to the Azure Dev CLI extension will be documented in this file.

Check [Keep a Changelog](http://keepachangelog.com/) for recommendations on how to structure this file.

## 99.99.9 (2022-01-01)

### Changed
- Fictional change

### Added
- Fictional addition

### Fixed
- Fictionall fix

"@ | Set-Content $ExtChangelogFile
    }

}

AfterAll {
    git checkout $ExtPackageJsonFile
    git checkout $ExtChangelogFile
}

Describe 'Update-VscodeExtensionVersion' ` {
    Context "Increments version number" {
        It "Increments from <existing> to <expected>" -TestCases @(
            @{ existing = '0.1.0'; expected = '0.2.0-alpha' },
            @{ existing = '0.1.0-alpha'; expected = '0.2.0-alpha' },
            @{ existing = '0.1.0-beta'; expected = '0.2.0-alpha' },
            @{ existing = '1.0.0'; expected = '1.1.0-alpha' }
        ) {
            SetPackageJsonVersion $existing
            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1
            GetPackageVersion | Should -Be $expected
        }
    }

    Context "Sets specified version number" {
        BeforeEach {
            ResetChangeLog
        }
        It "Sets version number from <existing> to <newVersion>" -TestCases @(
            @{ existing = '0.1.0'; newVersion = '1.0.0' },
            @{ existing = '1.2.3'; newVersion = '1.2.4' },
            @{ existing = '0.1.0-alpha'; newVersion = '0.1.0' },
            @{ existing = '0.1.0-alpha'; newVersion = '1.1.0-alpha' }
        ) {
            SetPackageJsonVersion $existing
            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1 -NewVersion $newVersion
            GetPackageVersion | Should -Be $newVersion
        }
    }

    Context "Adds CHANGELOG entry" {
        BeforeEach {
            ResetChangeLog
        }

        It "Adds the expected version to the CHANGELOG.md file" {
            $newVersionNumber = "1.2.3"
            $ExtChangelogFile | Should -Not -FileContentMatchMultiline "## $newVersionNumber"
            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1 -NewVersion $newVersionNumber
            $ExtChangelogFile | Should -FileContentMatchMultiline "## $newVersionNumber"
        }
    }
}