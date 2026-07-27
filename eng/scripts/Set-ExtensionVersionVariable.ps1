param(
    [string] $ExtensionDirectory,
    # Defaults to the pipeline-provided values so the script is unit-testable.
    [string] $BuildReason = $env:BUILD_REASON,
    [string] $BuildId = $env:BUILD_BUILDID,
    [string] $PublishToRegistry = 'stable'
)

$extVersion = (Get-Content "$ExtensionDirectory/version.txt").Trim()

# On nightly (scheduled or manually selected) runs, append a semver-valid prerelease suffix so each
# nightly sorts above the previous one (numeric build id) while still sorting
# below the matching stable release for non-prerelease base versions. The build
# id keeps all matrix jobs in a single run on the same version even if the run
# crosses midnight, and guarantees a re-run produces a distinct version.
if ($BuildReason -eq 'Schedule' -or $PublishToRegistry -eq 'nightly') {
    if ([string]::IsNullOrWhiteSpace($BuildId)) {
        throw "BuildId is required for nightly versioning but was empty (expected Build.BuildId)."
    }

    if ($extVersion.Contains('-')) {
        # Base already has a prerelease label (e.g. 1.2.3-preview): extend it.
        $extVersion = "$extVersion.nightly.$BuildId"
    }
    else {
        # Stable base (e.g. 1.2.3): add the prerelease label.
        $extVersion = "$extVersion-nightly.$BuildId"
    }
}

Write-Host "Extension Version: $extVersion"
Write-Host "##vso[task.setvariable variable=EXT_VERSION;]$extVersion"
