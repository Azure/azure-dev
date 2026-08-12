Describe 'Set-ExtensionVersionVariable' {
    BeforeAll {
        $scriptPath = Join-Path $PSScriptRoot 'Set-ExtensionVersionVariable.ps1'

        function Set-TestVersion {
            param([string] $Version)

            Set-Content -Path (Join-Path $extensionDirectory 'version.txt') -Value $Version
        }

        function Invoke-VersionScript {
            param(
                [string] $BuildReason = 'Manual',
                [string] $BuildReasonOverride = '',
                [string] $BuildId = '1234',
                [string] $PullRequestNumber = '',
                [string] $PullRequestNumberOverride = '',
                [string] $PublishToRegistry = 'stable'
            )

            & $scriptPath `
                -ExtensionDirectory $extensionDirectory `
                -BuildReason $BuildReason `
                -BuildReasonOverride $BuildReasonOverride `
                -BuildId $BuildId `
                -PullRequestNumber $PullRequestNumber `
                -PullRequestNumberOverride $PullRequestNumberOverride `
                -PublishToRegistry $PublishToRegistry 6>&1 |
                ForEach-Object { $_.ToString() }
        }
    }

    BeforeEach {
        $extensionDirectory = Join-Path $TestDrive 'extension'
        New-Item -ItemType Directory -Path $extensionDirectory -Force | Out-Null
    }

    It 'adds PR identity to a stable version' {
        Set-TestVersion '1.2.3'

        $output = Invoke-VersionScript -BuildReason PullRequest -PullRequestNumber 9409

        $output | Should -Contain 'Extension Version: 1.2.3-pr.9409.1234'
    }

    It 'extends an existing prerelease version with PR identity' {
        Set-TestVersion '1.2.3-preview'

        $output = Invoke-VersionScript -BuildReason PullRequest -PullRequestNumber 9409

        $output | Should -Contain 'Extension Version: 1.2.3-preview.pr.9409.1234'
    }

    It 'uses the build reason and PR number overrides' {
        Set-TestVersion '1.2.3'

        $output = Invoke-VersionScript `
            -BuildReason Manual `
            -BuildReasonOverride PullRequest `
            -PullRequestNumber 100 `
            -PullRequestNumberOverride 9409

        $output | Should -Contain 'Extension Version: 1.2.3-pr.9409.1234'
    }

    It 'rejects PR versioning without a PR number' {
        Set-TestVersion '1.2.3'

        { Invoke-VersionScript -BuildReason PullRequest } |
            Should -Throw '*PullRequestNumber is required for PR versioning*'
    }

    It 'rejects PR versioning without a build ID' {
        Set-TestVersion '1.2.3'

        { Invoke-VersionScript -BuildReason PullRequest -PullRequestNumber 9409 -BuildId '' } |
            Should -Throw '*BuildId is required for PR versioning*'
    }

    It 'leaves a non-PR release version unchanged' {
        Set-TestVersion '1.2.3'

        $output = Invoke-VersionScript

        $output | Should -Contain 'Extension Version: 1.2.3'
    }

    It 'preserves scheduled nightly versioning' {
        Set-TestVersion '1.2.3-preview'

        $output = Invoke-VersionScript `
            -BuildReason Schedule `
            -BuildReasonOverride PullRequest `
            -PullRequestNumber 9409

        $output | Should -Contain 'Extension Version: 1.2.3-preview.nightly.1234'
    }

    It 'preserves manually selected nightly versioning' {
        Set-TestVersion '1.2.3'

        $output = Invoke-VersionScript `
            -BuildReason Manual `
            -BuildReasonOverride PullRequest `
            -PullRequestNumber 9409 `
            -PublishToRegistry nightly

        $output | Should -Contain 'Extension Version: 1.2.3-nightly.1234'
    }
}
