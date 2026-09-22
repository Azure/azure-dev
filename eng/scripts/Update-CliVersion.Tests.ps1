Set-StrictMode -Version 4

BeforeAll {
    $ScriptArguments = @{
        VersionTxtPath      = Join-Path $TestDrive 'version.txt'
        ChangelogMdPath     = Join-Path $TestDrive 'CHANGELOG.md'
        AzdExtVersionGoPath = Join-Path $TestDrive 'version.go'
    }

    function InitTestFiles {
        Set-Content -Path $ScriptArguments.ChangelogMdPath -Value @'
# Release History

## 0.0.1 (Unreleased)

### Features Added

- Existing change.
'@
        Set-Content -Path $ScriptArguments.AzdExtVersionGoPath -Value 'const Version = "0.0.1"'
    }
}

# TODO: Formulate as TestCases
# It "does <a> <thing> " -TestCases @( a = 1; thing = 2) { [execute thing]; $a | should -be $thing }
# https://pester.dev/docs/usage/test-file-structure

Describe 'Update-CliVersion with version 0.1.0-beta.1' {
    BeforeEach {
        InitTestFiles
        Set-Content -Path $ScriptArguments.VersionTxtPath -Value "0.1.0-beta.1"
    }

    It "Increments prerelease number when no parameters are applied" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^0\.1\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments -NewVersion 1.2.3

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 0.1.0' {
    BeforeEach {
        InitTestFiles
        Set-Content -Path $ScriptArguments.VersionTxtPath -Value "0.1.0"
    }

    It "Increments minor number and sets beta.1" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^0\.2\.0-beta\.1$'
    }


    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments -NewVersion 1.2.3

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-beta.1' {
    BeforeEach {
        InitTestFiles
        Set-Content -Path $ScriptArguments.VersionTxtPath -Value "1.0.0-beta.1"
    }

    It "Increments prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.0\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments -NewVersion 1.2.3

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0' {
    BeforeEach {
        InitTestFiles
        Set-Content -Path $ScriptArguments.VersionTxtPath -Value "1.0.0"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments -NewVersion 1.2.3

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-badPrereleaseLabel.2' {
    BeforeEach {
        InitTestFiles
        Set-Content -Path $ScriptArguments.VersionTxtPath -Value "1.0.0-badPrereleaseLabel.2"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 @ScriptArguments -NewVersion 1.2.3

        $ScriptArguments.VersionTxtPath | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}
