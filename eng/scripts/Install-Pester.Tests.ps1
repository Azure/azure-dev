BeforeAll {
    $scriptPath = Join-Path $PSScriptRoot 'Install-Pester.ps1'
}

Describe 'Pinned Pester installation' {
    BeforeEach {
        Mock Start-Sleep {}
    }

    It 'installs the exact version from PSGallery without retrying success' {
        Mock Install-Module {}

        & $scriptPath

        Should -Invoke Install-Module -Times 1 -Exactly -ParameterFilter {
            $Name -eq 'Pester' -and $RequiredVersion -eq '5.7.1' -and
            $Repository -eq 'PSGallery' -and $Scope -eq 'CurrentUser' -and
            $Force -and $ErrorAction -eq 'Stop'
        }
        Should -Invoke Start-Sleep -Times 0 -Exactly
    }

    It 'retries download failures with bounded backoff' {
        $script:attempts = 0
        Mock Install-Module {
            $script:attempts++
            if ($script:attempts -lt 3) {
                Write-Error 'Package source unavailable'
            }
        }

        & $scriptPath

        Should -Invoke Install-Module -Times 3 -Exactly
        Should -Invoke Start-Sleep -Times 1 -Exactly -ParameterFilter { $Seconds -eq 5 }
        Should -Invoke Start-Sleep -Times 1 -Exactly -ParameterFilter { $Seconds -eq 10 }
    }

    It 'fails explicitly after three unsuccessful attempts' {
        Mock Install-Module { throw 'Package source unavailable' }

        { & $scriptPath } | Should -Throw '*after 3 attempts*Package source unavailable*'

        Should -Invoke Install-Module -Times 3 -Exactly
        Should -Invoke Start-Sleep -Times 2 -Exactly
    }
}
