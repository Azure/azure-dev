Set-StrictMode -Version 4

Describe 'Valid verison numbers' { 
    It "Given <version> returns <expected>" -ForEach @(
        @{ version = '0.4.0-beta.2'; expected = '0.4.2' },
        @{ version = '0.4.1'; expected = '0.4.200' },
        @{ version = '0.4.2'; expected = '0.4.300' },
        @{ version = '1.0.0-beta.1'; expected = '1.0.1' },
        @{ version = '1.0.0'; expected = '1.0.100' },
        @{ version = '1.1.0'; expected = '1.1.100' },
        @{ version = '1.2.3-beta.4'; expected = '1.2.304' },
        @{ version = '1.2.3'; expected = '1.2.400' },
        @{ version = '1.2.3-beta.5'; expected = '1.2.305'}
    ) { 
        $actual = & $PSScriptRoot/Get-MsiVersion.ps1 -CliVersion $version
        $actual | Should -BeExactly $expected
    }
}
