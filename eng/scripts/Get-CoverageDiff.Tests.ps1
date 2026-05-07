Set-StrictMode -Version 4

# Pester tests for Get-CoverageDiff.ps1. Each test builds a small synthetic
# Go coverprofile, invokes the script in a child pwsh, and asserts the exit
# code and report contents. Subprocess invocation is required because the
# script uses `exit 2` to signal a gate breach, which would terminate the
# Pester runner if invoked in-process.

BeforeAll {
    $script:scriptPath = Join-Path $PSScriptRoot 'Get-CoverageDiff.ps1'
    $script:modPrefix  = 'github.com/azure/azure-dev/cli/azd/'

    # Each entry: @{ File='pkg/sample/foo.go'; Stmts=10; Hits=1 }. Hits>0 means covered.
    function New-Profile {
        param([string]$Path, [object[]]$Entries)
        $sb = [System.Text.StringBuilder]::new()
        [void]$sb.AppendLine('mode: set')
        $line = 1
        foreach ($e in $Entries) {
            $f = "$script:modPrefix$($e.File)"
            $next = $line + 1
            [void]$sb.AppendLine("${f}:${line}.0,${next}.0 $($e.Stmts) $($e.Hits)")
            $line = $next
        }
        Set-Content -Path $Path -Value $sb.ToString() -Encoding ASCII
    }

    function Invoke-Script {
        param(
            [string]$BaselineFile,
            [string]$CurrentFile,
            [string[]]$ChangedFiles,
            [string]$ChangedFilesFromFile,
            [switch]$FailOnGate,
            [Nullable[double]]$MaxPackageDecrease = $null,
            [Nullable[double]]$MinOverallCoverage = $null
        )
        $pwshArgs = @(
            '-NoProfile', '-NonInteractive', '-File', $script:scriptPath,
            '-BaselineFile', $BaselineFile,
            '-CurrentFile',  $CurrentFile,
            '-ModulePrefix', $script:modPrefix
        )
        if ($null -ne $MaxPackageDecrease) {
            $pwshArgs += @('-MaxPackageDecrease', $MaxPackageDecrease)
        }
        if ($null -ne $MinOverallCoverage) {
            $pwshArgs += @('-MinOverallCoverage', $MinOverallCoverage)
        }
        if ($ChangedFiles)         { $pwshArgs += @('-ChangedFiles',         ($ChangedFiles -join ',')) }
        if ($ChangedFilesFromFile) { $pwshArgs += @('-ChangedFilesFromFile', $ChangedFilesFromFile) }
        if ($FailOnGate) { $pwshArgs += '-FailOnGate' }

        $stdout = & pwsh @pwshArgs 2>&1
        return @{ ExitCode = $LASTEXITCODE; Output = ($stdout -join "`n") }
    }

    function New-TempDir {
        $dir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $dir | Out-Null
        return $dir
    }
}

Describe 'Get-CoverageDiff: per-package report scoping' {
    BeforeAll {
        $script:tmp = New-TempDir

        # Baseline: pkg/a 60%, pkg/b 50%, pkg/c 80%
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 60; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 40; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 50; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 50; Hits = 0 }
            @{ File = 'pkg/c/z.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/c/z.go'; Stmts = 20; Hits = 0 }
        )
        # Current: pkg/a 70% (improved), pkg/b 50% (unchanged), pkg/c 80% (unchanged)
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 70; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 30; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 50; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 50; Hits = 0 }
            @{ File = 'pkg/c/z.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/c/z.go'; Stmts = 20; Hits = 0 }
        )
    }

    It 'shows only touched packages when -ChangedFiles is supplied' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/a/x.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages \(1 package\):'
        $r.Output   | Should -Match 'pkg/a'
        $r.Output   | Should -Not -Match '\bpkg/b\b'
        $r.Output   | Should -Not -Match '\bpkg/c\b'
    }

    It 'reports "PR-touched packages: none" when changed files match no coverage entries' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/nope/missing.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages: none with coverage data\.'
    }

    It 'includes a touched file with no coverage entry when its package has coverage (G4)' {
        # pkg/a/constants.go has no coverage entry (constants-only file or
        # build-tagged out), but pkg/a is otherwise tracked via x.go. The
        # package must still surface in the per-package report so the gate
        # can evaluate it.
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/a/constants.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages \(1 package\):'
        $r.Output   | Should -Match 'pkg/a'
    }

    It 'falls back to top-N changed packages when no changed files supplied' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out"

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'Top \d+ changed packages'
        $r.Output   | Should -Match 'pkg/a'
    }

    It 'shows "1 file touched" annotation for single-file packages' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/a/x.go'

        $r.Output | Should -Match '1 file touched'
    }

    It 'aggregates multiple files in same package as "N files touched"' {
        $script:tmp2 = New-TempDir
        New-Profile -Path "$script:tmp2/base.out" -Entries @(
            @{ File = 'pkg/multi/a.go'; Stmts = 10; Hits = 1 }
            @{ File = 'pkg/multi/b.go'; Stmts = 10; Hits = 1 }
            @{ File = 'pkg/multi/c.go'; Stmts = 10; Hits = 1 }
        )
        New-Profile -Path "$script:tmp2/curr.out" -Entries @(
            @{ File = 'pkg/multi/a.go'; Stmts = 10; Hits = 1 }
            @{ File = 'pkg/multi/b.go'; Stmts = 10; Hits = 1 }
            @{ File = 'pkg/multi/c.go'; Stmts = 10; Hits = 1 }
        )

        $r = Invoke-Script `
            -BaselineFile "$script:tmp2/base.out" `
            -CurrentFile  "$script:tmp2/curr.out" `
            -ChangedFiles 'cli/azd/pkg/multi/a.go,cli/azd/pkg/multi/b.go,cli/azd/pkg/multi/c.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match '3 files touched'
    }
}

Describe 'Get-CoverageDiff: changed-files input handling' {
    BeforeAll {
        $script:tmp = New-TempDir

        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 0 }
        )
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 0 }
        )
    }

    It 'ignores non-Go, _test.go, and .pb.go from changed-file input' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'README.md','cli/azd/pkg/a/x_test.go','cli/azd/pkg/a/generated.pb.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages: none with coverage data\.'
    }

    It 'normalizes repo-relative cli/azd/ prefix to module-relative' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/a/x.go'

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages \(1 package\)'
    }

    It 'accepts both -ChangedFiles and -ChangedFilesFromFile and dedupes them' {
        $listFile = "$script:tmp/changed.txt"
        Set-Content -Path $listFile -Value @(
            'cli/azd/pkg/a/x.go'
            'cli/azd/pkg/a/x.go'  # duplicate
        )

        $r = Invoke-Script `
            -BaselineFile         "$script:tmp/base.out" `
            -CurrentFile          "$script:tmp/curr.out" `
            -ChangedFiles         'cli/azd/pkg/a/x.go' `
            -ChangedFilesFromFile $listFile

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages \(1 package\)'
    }

    It 'reports none when -ChangedFilesFromFile is empty' {
        $listFile = "$script:tmp/empty.txt"
        Set-Content -Path $listFile -Value ''

        $r = Invoke-Script `
            -BaselineFile         "$script:tmp/base.out" `
            -CurrentFile          "$script:tmp/curr.out" `
            -ChangedFilesFromFile $listFile

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched packages: none with coverage data\.'
    }
}

Describe 'Get-CoverageDiff: profile parsing edge cases' {
    BeforeAll {
        $script:tmp = New-TempDir
    }

    It 'throws when current file is empty' {
        Set-Content -Path "$script:tmp/empty.out" -Value ''
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 10; Hits = 1 }
        )

        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/empty.out" `
            -FailOnGate

        $r.ExitCode | Should -Not -Be 0
        $r.ExitCode | Should -Not -Be 2
        $r.Output   | Should -Match 'does not start with a mode line'
    }

    It 'throws when profile has mode line but only malformed entries' {
        $f = "$script:tmp/malformed.out"
        Set-Content -Path $f -Value @('mode: set', 'this-is-not-a-valid-coverline', 'another-bad-line')
        New-Profile -Path "$script:tmp/base2.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 10; Hits = 1 }
        )

        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base2.out" `
            -CurrentFile  $f

        $r.ExitCode | Should -Not -Be 0
        $r.Output   | Should -Match 'valid coverage entries'
    }

    It 'tolerates valid mix of well-formed and malformed lines (warns, does not throw)' {
        $f = "$script:tmp/mixed.out"
        Set-Content -Path $f -Value @(
            'mode: set'
            'github.com/azure/azure-dev/cli/azd/pkg/a/x.go:1.0,2.0 50 1'
            'github.com/azure/azure-dev/cli/azd/pkg/a/x.go:3.0,4.0 50 0'
            'completely-bogus-line'
        )
        New-Profile -Path "$script:tmp/base3.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 50; Hits = 0 }
        )

        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base3.out" `
            -CurrentFile  $f `
            -MinOverallCoverage 0 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }
}

Describe 'Get-CoverageDiff: absolute floor gate (-MinOverallCoverage)' {
    BeforeAll {
        $script:tmp = New-TempDir
        # Baseline 80% (fits comfortably above any reasonable floor)
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 20; Hits = 0 }
        )
        # Current 60% (below default 65 floor; only -20pp from baseline)
        New-Profile -Path "$script:tmp/below-floor.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 60; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 40; Hits = 0 }
        )
            # Add an at-floor profile (exactly 65%) to lock in the boundary contract:
            # currTotal == MinOverallCoverage MUST pass (gate uses strict less-than).
            New-Profile -Path "$script:tmp/at-floor.out" -Entries @(
                @{ File = 'pkg/a/x.go'; Stmts = 65; Hits = 1 }
                @{ File = 'pkg/a/x.go'; Stmts = 35; Hits = 0 }
            )
        # Current 70% (above default 65 floor)
        New-Profile -Path "$script:tmp/above-floor.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 70; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 30; Hits = 0 }
        )
    }

    It 'FAILs when overall coverage drops below the -MinOverallCoverage floor' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/below-floor.out" `
            -MinOverallCoverage  65 `
            -MaxPackageDecrease  100 `
            -FailOnGate

        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'RESULT: FAIL'
        $r.Output   | Should -Match 'Overall coverage 60\.0% is below floor of 65\.0%'
        $r.Output   | Should -Match '##vso\[task\.logissue type=error\].*below floor'
    }

    It 'PASSes when overall coverage stays above the floor' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/above-floor.out" `
            -MinOverallCoverage  65 `
            -MaxPackageDecrease  100 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'PASSes when overall coverage exactly equals the -MinOverallCoverage floor' {
        # Boundary contract: currTotal >= MinOverallCoverage passes. Use raw
        # comparison (strict less-than) so 65.0% at a 65 floor does NOT fail.
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/at-floor.out" `
            -MinOverallCoverage  65 `
            -MaxPackageDecrease  100 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'shows the floor in the report header' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/above-floor.out" `
            -MinOverallCoverage  65 `
            -MaxPackageDecrease  100

        $r.Output | Should -Match 'Floor: overall coverage must stay >= 65\.0%'
    }

    It 'is advisory (exit 0) without -FailOnGate even when below floor' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/below-floor.out" `
            -MinOverallCoverage  65 `
            -MaxPackageDecrease  100

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: FAIL'
        $r.Output   | Should -Match 'below floor of 65\.0%'
    }
}

Describe 'Get-CoverageDiff: per-package decrease gate (-MaxPackageDecrease)' {
    BeforeAll {
        $script:tmp = New-TempDir
        # Baseline: pkg/a 80%, pkg/b 80% (overall 80%)
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 20; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 20; Hits = 0 }
        )
        # Current: pkg/a 78% (-2pp), pkg/b 80% (overall 79%, -1pp)
        New-Profile -Path "$script:tmp/pkg-regress.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 78; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 22; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 20; Hits = 0 }
        )
        # Current: pkg/a 79.5% (-0.5pp, exactly at boundary), pkg/b 80%
        New-Profile -Path "$script:tmp/pkg-tiny.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 159; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 41;  Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 80;  Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 20;  Hits = 0 }
        )
    }

    It 'FAILs when any package drops more than -MaxPackageDecrease' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/pkg-regress.out" `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'RESULT: FAIL'
        $r.Output   | Should -Match '1 package\(s\) dropped more than 0\.5 pp'
        $r.Output   | Should -Match 'pkg/a: 80\.0% -> 78\.0% \(-2\.0 pp\)'
        $r.Output   | Should -Match '##vso\[task\.logissue type=error\].*Package pkg/a dropped 2\.0 pp'
    }

    It 'PASSes when no package drops beyond tolerance' {
        # Baseline → baseline = no change at all.
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/base.out" `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'PASSes when a package decrease is exactly at the tolerance boundary' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/pkg-tiny.out" `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'with custom -MaxPackageDecrease 5.0 tolerates a 2.0pp package drop' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/pkg-regress.out" `
            -MaxPackageDecrease  5.0 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Match 'Tolerance: -5\.0 pp per package'
    }

    It 'in changed-file mode, only checks PR-touched packages' {
        # pkg/a regresses but is NOT in changed files → should NOT trigger gate.
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/pkg-regress.out" `
            -ChangedFiles        'cli/azd/pkg/b/y.go' `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'in changed-file mode, FAILs when a touched package regresses' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/pkg-regress.out" `
            -ChangedFiles        'cli/azd/pkg/a/x.go' `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  0 `
            -FailOnGate

        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'Package pkg/a dropped 2\.0 pp'
    }
}

Describe 'Get-CoverageDiff: combined multi-gate behavior' {
    BeforeAll {
        $script:tmp = New-TempDir
        # Baseline 80%
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 20; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 80; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 20; Hits = 0 }
        )
        # Current trips ALL THREE gates: overall 50% (below 65 floor + drop 30pp),
        # pkg/a -50pp, pkg/b -10pp.
        New-Profile -Path "$script:tmp/all-bad.out" -Entries @(
            @{ File = 'pkg/a/x.go'; Stmts = 30; Hits = 1 }
            @{ File = 'pkg/a/x.go'; Stmts = 70; Hits = 0 }
            @{ File = 'pkg/b/y.go'; Stmts = 70; Hits = 1 }
            @{ File = 'pkg/b/y.go'; Stmts = 30; Hits = 0 }
        )
    }

    It 'reports both breached gates in a single FAIL block' {
        $r = Invoke-Script `
            -BaselineFile        "$script:tmp/base.out" `
            -CurrentFile         "$script:tmp/all-bad.out" `
            -MaxPackageDecrease  0.5 `
            -MinOverallCoverage  65 `
            -FailOnGate

        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'RESULT: FAIL'
        $r.Output   | Should -Match 'Overall coverage 50\.0% is below floor of 65\.0%'
        $r.Output   | Should -Match '2 package\(s\) dropped more than 0\.5 pp'
    }
}
