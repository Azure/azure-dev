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
            @{ existing = '0.1.0'; expected = '0.2.0-alpha.1' },
            @{ existing = '1.0.0'; expected = '1.1.0-alpha.1' }
        ) {
            SetPackageJsonVersion $existing
            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1
            GetPackageVersion | Should -BeExactly $expected
        }
    }

    Context "Sets specified version number" {
        BeforeEach {
            ResetChangeLog
        }
        It "Sets version number from <existing> to <newVersion>" -TestCases @(
            @{ existing = '0.1.0'; newVersion = '1.0.0' },
            @{ existing = '1.2.3'; newVersion = '1.2.4' },
            @{ existing = '0.1.0-alpha.1'; newVersion = '1.1.0-alpha.1' }
        ) {
            SetPackageJsonVersion $existing
            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1 -NewVersion $newVersion
            GetPackageVersion | Should -BeExactly $newVersion
        }
    }

    Context "Adds CHANGELOG entry" {
        BeforeEach {
            ResetChangeLog
        }

        It "Updates CHANGELOG entry when version is directly specified" {
            $newVersionNumber = "1.2.3"
            $newVersionNumberRegex = "1\.2\.3"
            # Ensure file doesn't contain expected version before running test
            $ExtChangelogFile | Should -Not -FileContentMatch "^## $newVersionNumberRegex"

            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1 -NewVersion $newVersionNumber
            $ExtChangelogFile | Should -FileContentMatch "^## $newVersionNumberRegex "
        }

        It "Adds new CHANGELOG entry when version is not specified" {
            # Set existing package version
            SetPackageJsonVersion "0.1.0-alpha.1"

            $newVersionNumber = "0.2.0-alpha.1"
            $newVersionNumberRegex = "0\.2\.0-alpha\.1"
            # Ensure file doesn't contain expected version before running test
            $ExtChangelogFile | Should -Not -FileContentMatch "^## $newVersionNumberRegex"

            & $PSScriptRoot/Update-VscodeExtensionVersion.ps1 -NewVersion $newVersionNumber

            $ExtChangelogFile | Should -FileContentMatch "^## $newVersionNumberRegex "
        }
    }
}