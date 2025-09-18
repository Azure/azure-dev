Set-StrictMode -Version 4

BeforeAll {
    $CliVersionFile = "$PSScriptRoot../../../cli/version.txt"
    $CliChangelogFile = "$PSScriptRoot../../../cli/azd/CHANGELOG.md"
}

AfterAll {
    git checkout $CliVersionFile
    git checkout $CliChangelogFile
}

# TODO: Formulate as TestCases
# It "does <a> <thing> " -TestCases @( a = 1; thing = 2) { [execute thing]; $a | should -be $thing }
# https://pester.dev/docs/usage/test-file-structure

Describe 'Update-CliVersion with version 0.1.0-beta.1' {
    BeforeEach {
        Set-Content -Path $CliVersionFile -Value "0.1.0-beta.1"
    }

    It "Increments prerelease number when no parameters are applied" {
        & $PSScriptRoot/Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^0\.1\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 0.1.0' {
    BeforeEach {
        Set-Content -Path $CliVersionFile -Value "0.1.0"
    }

    It "Increments minor number and sets beta.1" {
        & $PSScriptRoot/Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^0\.2\.0-beta\.1$'
    }


    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-beta.1' {
    BeforeEach {
        Set-Content -Path $CliVersionFile -Value "1.0.0-beta.1"
    }

    It "Increments prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.0\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0' {
    BeforeEach {
        Set-Content -Path $CliVersionFile -Value "1.0.0"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-badPrereleaseLabel.2' {
    BeforeEach {
        Set-Content -Path $CliVersionFile -Value "1.0.0-badPrereleaseLabel.2"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}