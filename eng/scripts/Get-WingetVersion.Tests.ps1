Set-StrictMode -Version 4

Describe 'Valid verison numbers' { 
    It "Given <version> returns <expected>" -ForEach @(
        @{ version = '0.4.0-beta.2'; expected = '0.4.0.2' },
        @{ version = '0.4.1'; expected = '0.4.1.0' },
        @{ version = '0.4.2'; expected = '0.4.2.0' },
        @{ version = '1.0.0-beta.1'; expected = '1.0.0.1' },
        @{ version = '1.0.0'; expected = '1.0.0.0' },
        @{ version = '1.1.0'; expected = '1.1.0.0' },
        @{ version = '1.2.3-beta.4'; expected = '1.2.3.4' },
        @{ version = '1.2.3'; expected = '1.2.3.0' },
        @{ version = '1.2.3-beta.5'; expected = '1.2.3.5'}
    ) { 
        $actual = & $PSScriptRoot/Get-WingetVersion.ps1 -CliVersion $version
        $actual | Should -BeExactly $expected
    }
}
