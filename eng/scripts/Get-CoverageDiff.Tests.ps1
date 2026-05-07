Set-StrictMode -Version 4

# Pester tests for Get-CoverageDiff.ps1. Each test builds a small synthetic
# Go coverprofile, invokes the script in a child pwsh, and asserts the exit
# code and report contents. Subprocess invocation is required because the
# script uses `exit 2` to signal a floor breach, which would terminate the
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
            [switch]$FailOnFloorBreach,
            [int]$MinNewFileStatements = 10,
            [double]$MinFloor = 50.0
        )
        $pwshArgs = @(
            '-NoProfile', '-NonInteractive', '-File', $script:scriptPath,
            '-BaselineFile', $BaselineFile,
            '-CurrentFile',  $CurrentFile,
            '-ModulePrefix', $script:modPrefix,
            '-MinFloor',     $MinFloor,
            '-MinNewFileStatements', $MinNewFileStatements
        )
        if ($ChangedFiles)         { $pwshArgs += @('-ChangedFiles',         ($ChangedFiles -join ',')) }
        if ($ChangedFilesFromFile) { $pwshArgs += @('-ChangedFilesFromFile', $ChangedFilesFromFile) }
        if ($FailOnFloorBreach){ $pwshArgs += '-FailOnFloorBreach' }

        $stdout = & pwsh @pwshArgs 2>&1
        return @{ ExitCode = $LASTEXITCODE; Output = ($stdout -join "`n") }
    }

    function New-TempDir {
        $dir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $dir | Out-Null
        return $dir
    }
}

Describe 'Get-CoverageDiff: file mode — pass scenarios' {
    BeforeAll {
        $script:tmp = New-TempDir

        # Baseline: improved.go 50%, unchanged.go 60%, untouched.go 30%.
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/sample/improved.go';  Stmts = 10; Hits = 1 }
            @{ File = 'pkg/sample/improved.go';  Stmts = 10; Hits = 0 }
            @{ File = 'pkg/sample/unchanged.go'; Stmts = 6;  Hits = 1 }
            @{ File = 'pkg/sample/unchanged.go'; Stmts = 4;  Hits = 0 }
            @{ File = 'pkg/sample/untouched.go';     Stmts = 3;  Hits = 1 }
            @{ File = 'pkg/sample/untouched.go';     Stmts = 7;  Hits = 0 }
        )
        # Current: improved.go 70%, unchanged.go 60%, untouched.go 30%.
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/sample/improved.go';  Stmts = 14; Hits = 1 }
            @{ File = 'pkg/sample/improved.go';  Stmts = 6;  Hits = 0 }
            @{ File = 'pkg/sample/unchanged.go'; Stmts = 6;  Hits = 1 }
            @{ File = 'pkg/sample/unchanged.go'; Stmts = 4;  Hits = 0 }
            @{ File = 'pkg/sample/untouched.go';     Stmts = 3;  Hits = 1 }
            @{ File = 'pkg/sample/untouched.go';     Stmts = 7;  Hits = 0 }
        )
    }

    It 'returns RESULT: PASS and exit 0 when all touched files >= floor' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/improved.go','cli/azd/pkg/sample/unchanged.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Match 'pkg/sample/improved\.go.*improved'
    }

    It 'reports unchanged file with Delta = 0 and ok status' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/unchanged.go'
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'pkg/sample/unchanged\.go'
        $r.Output   | Should -Match '\+0\.0%'
    }

    It 'ignores test files and non-Go files in changed list' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/improved_test.go','docs/README.md'
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched Go files: none'
    }

    It 'ignores generated *.pb.go files in changed list' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/wire_gen.pb.go'
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched Go files: none'
    }

    It 'handles -ChangedFilesFromFile pointing at a list with only non-Go entries' {
        $listPath = Join-Path $script:tmp 'docs-only.txt'
        Set-Content -Path $listPath -Value @('docs/README.md', 'eng/pipeline.yml', '') -Encoding UTF8
        $r = Invoke-Script `
            -BaselineFile         "$script:tmp/base.out" `
            -CurrentFile          "$script:tmp/curr.out" `
            -ChangedFilesFromFile $listPath `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched Go files: none'
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'handles -ChangedFilesFromFile pointing at an empty file' {
        $listPath = Join-Path $script:tmp 'empty.txt'
        Set-Content -Path $listPath -Value '' -Encoding UTF8
        $r = Invoke-Script `
            -BaselineFile         "$script:tmp/base.out" `
            -CurrentFile          "$script:tmp/curr.out" `
            -ChangedFilesFromFile $listPath `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'PR-touched Go files: none'
    }

    AfterAll {
        Remove-Item -Recurse -Force $script:tmp -ErrorAction SilentlyContinue
    }
}

Describe 'Get-CoverageDiff: file mode — fail scenarios' {
    BeforeAll {
        $script:tmp = New-TempDir

        # Baseline: regressed.go 80%, low_coverage.go 20%.
        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/sample/regressed.go';     Stmts = 8; Hits = 1 }
            @{ File = 'pkg/sample/regressed.go';     Stmts = 2; Hits = 0 }
            @{ File = 'pkg/sample/low_coverage.go';  Stmts = 2; Hits = 1 }
            @{ File = 'pkg/sample/low_coverage.go';  Stmts = 8; Hits = 0 }
        )
        # Current: regressed.go drops to 30% (FAIL); low_coverage.go still 20% (FAIL — strict floor).
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/sample/regressed.go';     Stmts = 3; Hits = 1 }
            @{ File = 'pkg/sample/regressed.go';     Stmts = 7; Hits = 0 }
            @{ File = 'pkg/sample/low_coverage.go';  Stmts = 2; Hits = 1 }
            @{ File = 'pkg/sample/low_coverage.go';  Stmts = 8; Hits = 0 }
        )
    }

    It 'emits ##vso[task.logissue type=error] on FAIL with -FailOnFloorBreach' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/regressed.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match '##vso\[task\.logissue type=error\].*Coverage floor breach.*regressed\.go'
    }

    It 'marks regression below floor as FAIL and returns exit 2 when -FailOnFloorBreach' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/regressed.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'FAIL'
        $r.Output   | Should -Match 'How to fix'
        $r.Output   | Should -Match 'regressed\.go'
    }

    It 'returns exit 0 on FAIL when -FailOnFloorBreach is NOT set (advisory mode)' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/regressed.go'
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: FAIL'
    }

    It 'fails any touched file below floor regardless of baseline (strict floor)' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/low_coverage.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'FAIL'
        $r.Output   | Should -Match 'low_coverage\.go'
        $r.Output   | Should -Match 'below floor'
        $r.Output   | Should -Match 'RESULT: FAIL'
    }

    AfterAll {
        Remove-Item -Recurse -Force $script:tmp -ErrorAction SilentlyContinue
    }
}

Describe 'Get-CoverageDiff: new files' {
    BeforeAll {
        $script:tmp = New-TempDir

        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/sample/baseline.go'; Stmts = 10; Hits = 1 }
        )
        # Current adds:
        #   added_covered.go     20 stmts, 80% covered  -> "new", PASS
        #   added_uncovered.go   20 stmts, 30% covered  -> FAIL
        #   added_small.go        3 stmts, 0%  covered  -> "new" (small, exempt)
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/sample/baseline.go';        Stmts = 10; Hits = 1 }
            @{ File = 'pkg/sample/added_covered.go';   Stmts = 16; Hits = 1 }
            @{ File = 'pkg/sample/added_covered.go';   Stmts = 4;  Hits = 0 }
            @{ File = 'pkg/sample/added_uncovered.go'; Stmts = 6;  Hits = 1 }
            @{ File = 'pkg/sample/added_uncovered.go'; Stmts = 14; Hits = 0 }
            @{ File = 'pkg/sample/added_small.go';     Stmts = 3;  Hits = 0 }
        )
    }

    It 'flags new file at/above floor as new without failing' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/added_covered.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'pkg/sample/added_covered\.go.*\bnew\b'
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    It 'fails on new file below floor with sufficient statements' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/added_uncovered.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 2
        $r.Output   | Should -Match 'RESULT: FAIL'
        $r.Output   | Should -Match 'new file below floor'
    }

    It 'exempts small new files (< MinNewFileStatements) from floor failure' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/added_small.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'small file'
        $r.Output   | Should -Match 'RESULT: PASS'
    }

    AfterAll {
        Remove-Item -Recurse -Force $script:tmp -ErrorAction SilentlyContinue
    }
}

Describe 'Get-CoverageDiff: input handling' {
    BeforeAll {
        $script:tmp = New-TempDir

        New-Profile -Path "$script:tmp/base.out" -Entries @(
            @{ File = 'pkg/sample/single.go'; Stmts = 10; Hits = 1 }
        )
        New-Profile -Path "$script:tmp/curr.out" -Entries @(
            @{ File = 'pkg/sample/single.go'; Stmts = 10; Hits = 1 }
        )
    }

    It 'falls back to package mode when no changed files supplied' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out"
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Not -Match 'PR-touched files'
    }

    It 'accepts module-relative paths as well as repo-relative paths' {
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFiles 'pkg/sample/single.go'
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'pkg/sample/single\.go'
    }

    It 'reads -ChangedFilesFromFile newline-delimited' {
        $listPath = "$script:tmp/changed.txt"
        Set-Content -Path $listPath -Value @(
            'cli/azd/pkg/sample/single.go'
            ''
            'docs/notes.md'
            'cli/azd/pkg/sample/single_test.go'
        ) -Encoding ASCII
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/base.out" `
            -CurrentFile  "$script:tmp/curr.out" `
            -ChangedFilesFromFile $listPath
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'pkg/sample/single\.go'
    }

    AfterAll {
        Remove-Item -Recurse -Force $script:tmp -ErrorAction SilentlyContinue
    }
}

Describe 'Get-CoverageDiff: edge cases' {
    BeforeAll {
        $script:tmp = New-TempDir
    }

    It 'treats exactly-at-floor (50%) as PASS, not below floor' {
        New-Profile -Path "$script:tmp/half-base.out" -Entries @(
            @{ File = 'pkg/sample/exactly_half.go'; Stmts = 5; Hits = 1 }
            @{ File = 'pkg/sample/exactly_half.go'; Stmts = 5; Hits = 0 }
        )
        New-Profile -Path "$script:tmp/half-curr.out" -Entries @(
            @{ File = 'pkg/sample/exactly_half.go'; Stmts = 5; Hits = 1 }
            @{ File = 'pkg/sample/exactly_half.go'; Stmts = 5; Hits = 0 }
        )
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/half-base.out" `
            -CurrentFile  "$script:tmp/half-curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/exactly_half.go' `
            -MinFloor 50.0 `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Not -Match 'below floor'
    }

    It 'with -MinFloor 0 never fails even on 0% files' {
        New-Profile -Path "$script:tmp/zero-base.out" -Entries @(
            @{ File = 'pkg/sample/zero_coverage.go'; Stmts = 20; Hits = 0 }
        )
        New-Profile -Path "$script:tmp/zero-curr.out" -Entries @(
            @{ File = 'pkg/sample/zero_coverage.go'; Stmts = 20; Hits = 0 }
        )
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/zero-base.out" `
            -CurrentFile  "$script:tmp/zero-curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/zero_coverage.go' `
            -MinFloor 0.0 `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Not -Match 'below floor'
        $r.Output   | Should -Not -Match 'FAIL'
    }

    It 'silently skips deleted files (in baseline only) without crashing' {
        New-Profile -Path "$script:tmp/del-base.out" -Entries @(
            @{ File = 'pkg/sample/deleted.go'; Stmts = 10; Hits = 0 }
            @{ File = 'pkg/sample/kept.go';    Stmts = 10; Hits = 1 }
        )
        New-Profile -Path "$script:tmp/del-curr.out" -Entries @(
            @{ File = 'pkg/sample/kept.go';    Stmts = 10; Hits = 1 }
        )
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/del-base.out" `
            -CurrentFile  "$script:tmp/del-curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/deleted.go','cli/azd/pkg/sample/kept.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'RESULT: PASS'
        $r.Output   | Should -Match 'pkg/sample/kept\.go'
        $r.Output   | Should -Not -Match 'pkg/sample/deleted\.go\s'
    }

    It 'errors on empty profile file' {
        $emptyPath = "$script:tmp/empty.out"
        Set-Content -Path $emptyPath -Value '' -Encoding ASCII
        New-Profile -Path "$script:tmp/empty-curr.out" -Entries @(
            @{ File = 'pkg/sample/parsed.go'; Stmts = 4; Hits = 1 }
        )
        $r = Invoke-Script -BaselineFile $emptyPath -CurrentFile "$script:tmp/empty-curr.out"
        $r.ExitCode | Should -Not -Be 0
        $r.Output   | Should -Match '(?i)empty'
    }

    It 'errors when profile is missing the leading mode line' {
        $bad = "$script:tmp/no-mode.out"
        Set-Content -Path $bad -Value @(
            'github.com/azure/azure-dev/cli/azd/pkg/sample/parsed.go:1.0,2.0 4 4'
        ) -Encoding ASCII
        New-Profile -Path "$script:tmp/no-mode-curr.out" -Entries @(
            @{ File = 'pkg/sample/parsed.go'; Stmts = 4; Hits = 1 }
        )
        $r = Invoke-Script -BaselineFile $bad -CurrentFile "$script:tmp/no-mode-curr.out"
        $r.ExitCode | Should -Not -Be 0
        $r.Output   | Should -Match '(?i)mode'
    }

    It 'tolerates malformed lines mixed with valid lines (warns, continues)' {
        $mixed = "$script:tmp/mixed.out"
        Set-Content -Path $mixed -Value @(
            'mode: set'
            'github.com/azure/azure-dev/cli/azd/pkg/sample/parsed.go:1.0,2.0 4 4'
            'this line is garbage and should be skipped'
            'github.com/azure/azure-dev/cli/azd/pkg/sample/parsed.go:2.0,3.0 6 6'
        ) -Encoding ASCII
        New-Profile -Path "$script:tmp/mixed-curr.out" -Entries @(
            @{ File = 'pkg/sample/parsed.go'; Stmts = 4; Hits = 1 }
            @{ File = 'pkg/sample/parsed.go'; Stmts = 6; Hits = 1 }
        )
        $r = Invoke-Script `
            -BaselineFile $mixed `
            -CurrentFile  "$script:tmp/mixed-curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/parsed.go' `
            -FailOnFloorBreach
        $r.ExitCode | Should -Be 0
        $r.Output   | Should -Match 'pkg/sample/parsed\.go'
        $r.Output   | Should -Match '(?i)skipped\s+1\s+line'
    }

    It 'errors when every coverage line is malformed (no silent pass on corrupt profile)' {
        $allBad = "$script:tmp/all-bad.out"
        Set-Content -Path $allBad -Value @(
            'mode: set'
            'this line is garbage'
            'so is this one'
        ) -Encoding ASCII
        New-Profile -Path "$script:tmp/all-bad-curr.out" -Entries @(
            @{ File = 'pkg/sample/parsed.go'; Stmts = 4; Hits = 1 }
        )
        $r = Invoke-Script -BaselineFile $allBad -CurrentFile "$script:tmp/all-bad-curr.out"
        $r.ExitCode | Should -Not -Be 0
        $r.Output   | Should -Match '(?i)valid coverage entries'
    }

    It 'dedupes duplicate paths across -ChangedFiles and -ChangedFilesFromFile' {
        New-Profile -Path "$script:tmp/dup-base.out" -Entries @(
            @{ File = 'pkg/sample/duplicate.go'; Stmts = 8; Hits = 1 }
            @{ File = 'pkg/sample/duplicate.go'; Stmts = 2; Hits = 0 }
        )
        New-Profile -Path "$script:tmp/dup-curr.out" -Entries @(
            @{ File = 'pkg/sample/duplicate.go'; Stmts = 8; Hits = 1 }
            @{ File = 'pkg/sample/duplicate.go'; Stmts = 2; Hits = 0 }
        )
        $listPath = "$script:tmp/dup-list.txt"
        Set-Content -Path $listPath -Value @(
            'cli/azd/pkg/sample/duplicate.go'
            'cli/azd/pkg/sample/duplicate.go'
        ) -Encoding ASCII
        $r = Invoke-Script `
            -BaselineFile "$script:tmp/dup-base.out" `
            -CurrentFile  "$script:tmp/dup-curr.out" `
            -ChangedFiles 'cli/azd/pkg/sample/duplicate.go','cli/azd/pkg/sample/duplicate.go' `
            -ChangedFilesFromFile $listPath
        $r.ExitCode | Should -Be 0
        $hits = [regex]::Matches($r.Output, 'pkg/sample/duplicate\.go')
        $hits.Count | Should -Be 1
    }

    AfterAll {
        Remove-Item -Recurse -Force $script:tmp -ErrorAction SilentlyContinue
    }
}
