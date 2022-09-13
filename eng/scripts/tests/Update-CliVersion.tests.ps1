Set-StrictMode -Version 3

AfterAll {
    ."$PSScriptRoot/Update-CliVersion.constants.ps1"
    CheckoutChangedFiles
}

Describe 'Update-CliVersion with version 0.1.0-beta.1' {
    BeforeEach {
        ."$PSScriptRoot/Update-CliVersion.constants.ps1"
        Set-Content -Path $CliVersionFile -Value "0.1.0-beta.1"
    }

    It "Increments prerelease number when no parameters are applied" {
        & $PSScriptRoot/../Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^0\.1\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/../Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 0.1.0' {
    BeforeEach {
        ."$PSScriptRoot/Update-CliVersion.constants.ps1"
        Set-Content -Path $CliVersionFile -Value "0.1.0"
    }

    It "Increments minor number and sets beta.1" {
        & $PSScriptRoot/../Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^0\.2\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/../Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-beta.1' {
    BeforeEach {
        ."$PSScriptRoot/Update-CliVersion.constants.ps1"
        Set-Content -Path $CliVersionFile -Value "1.0.0-beta.1"
    }

    It "Increments prerelease number" {
        & $PSScriptRoot/../Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.0\.0-beta\.2$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/../Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0' {
    BeforeEach {
        ."$PSScriptRoot/Update-CliVersion.constants.ps1"
        Set-Content -Path $CliVersionFile -Value "1.0.0"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/../Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/../Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}

Describe 'Update-CliVersion with version 1.0.0-badPrereleaseLabel.2' {
    BeforeEach {
        ."$PSScriptRoot/Update-CliVersion.constants.ps1"
        Set-Content -Path $CliVersionFile -Value "1.0.0-badPrereleaseLabel.2"
    }

    It "Increments minor and prerelease number" {
        & $PSScriptRoot/../Update-CliVersion.ps1

        $CliVersionFile | Should -FileContentMatchExactly '^1\.1\.0-beta\.1$'
    }

    It "Sets version when given -NewVersion" {
        & $PSScriptRoot/../Update-CliVersion.ps1 -NewVersion 1.2.3

        $CliVersionFile | Should -FileContentMatchExactly '^1\.2\.3$'
    }
}