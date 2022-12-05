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

Describe 'getSemverParsedVersion' {
    BeforeEach { 
        # Import cmdlet functions into current session
        . "$PSScriptRoot/Get-MsiVersion.ps1" -CliVersion 9.9.9
    }
    
    It 'Given <version> returns <expected>' -ForEach @( 
        @{ version = '1.2.3-beta.1'; expected = '1.2.3-beta.1' },
        @{ version = '1.2.3'; expected = '1.2.3' },
        @{ version = '0.4.0-beta.2-pr.2021242'; expected = '0.4.0-beta.2'},
        @{ version = '0.4.0-beta.2-daily.2026027'; expected = '0.4.0-beta.2'}
    ) { 
        $actual = getSemverParsedVersion $version
        $actual | Should -Be $expected
    }

    It 'Throws on unexpected version of <version>' -ForEach @( 
        @{ version = '1.2.3-beta.1.2.3' },
        @{ version = '1.2.3-beta.1.pr.123' },
        @{ version = '1.2.3-beta' },
        @{ version = '1.2.3-beta.100' }
        @{ version = '1.2.3-beta.0' }
        @{ version = '1.2.3-beta.-1' }
    ) { 
        { getSemverParsedVersion $version } | Should -Throw
    }
}