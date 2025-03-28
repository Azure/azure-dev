
Set-StrictMode -Version 4

Describe 'Releases when it should' { 
    It "Given <version> and <allowPrerelease> returns <expected>" -ForEach @(
        # Zero major version is a pre-release version
        @{ version = '0.4.0-beta.2'; allowPrerelease = $true; expected = $true },
        @{ version = '0.4.0'; allowPrerelease = $true; expected = $true },
        @{ version = '0.4.0-beta.2'; allowPrerelease = $false; expected = $false },
        @{ version = '0.4.0'; allowPrerelease = $false; expected = $false },

        # Use a non-zero major version
        @{ version = '1.4.0-beta.2'; allowPrerelease = $true; expected = $true },
        @{ version = '1.4.0'; allowPrerelease = $true; expected = $true },
        @{ version = '1.4.0-beta.2'; allowPrerelease = $false; expected = $false },
        @{ version = '1.4.0'; allowPrerelease = $false; expected = $true }
    ) { 
        $actual = & $PSScriptRoot/Test-ShouldReleasePackageVersion.ps1 `
            -CliVersion $version `
            -AllowPrerelease:$allowPrerelease
        $actual | Should -BeExactly $expected
    }
}