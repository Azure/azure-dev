param(
    [string] $ExtensionDirectory,
    # Optional pre-release suffix (without the leading '-'). When provided it is
    # appended to the base version, e.g. -Suffix 'alpha.202606150300' produces
    # '<base>-alpha.202606150300'. Used by the nightly extension release pipeline.
    [string] $Suffix
)

$extVersion = (Get-Content "$ExtensionDirectory/version.txt" -Raw).Trim()

if ($Suffix) {
    # If the base version already has a pre-release segment (contains '-'),
    # extend it with dot-separated identifiers to keep valid semver ordering
    # (e.g. '0.1.39-preview' + 'alpha.202606150300' => '0.1.39-preview.alpha.202606150300').
    # Otherwise start the pre-release segment with a hyphen
    # (e.g. '0.5.0' + 'alpha.202606150300' => '0.5.0-alpha.202606150300').
    if ($extVersion -match '-') {
        $extVersion = "$extVersion.$Suffix"
    } else {
        $extVersion = "$extVersion-$Suffix"
    }
}

Write-Host "Extension Version: $extVersion"
Write-Host "##vso[task.setvariable variable=EXT_VERSION;]$extVersion"
