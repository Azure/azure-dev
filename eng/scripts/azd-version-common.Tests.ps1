Describe 'getSemverParsedVersion' {
    BeforeEach { 
        # Import cmdlet functions into current session
        . "$PSScriptRoot/azd-version-common.ps1"
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
