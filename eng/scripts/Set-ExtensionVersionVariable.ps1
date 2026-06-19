# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

<#
.SYNOPSIS
Sets the EXT_VERSION pipeline variable for an azd extension build.

.DESCRIPTION
By default EXT_VERSION is the verbatim contents of the extension's version.txt.

When nightly mode is enabled the base version is transformed into a semver-valid
nightly prerelease of the form:

    <base>-nightly.<yyyyMMdd>.<buildId>            (base has no prerelease)
    <base>.nightly.<yyyyMMdd>.<buildId>            (base already has a prerelease)

Worked examples (date 2026-06-18, build id 1234):

    version.txt   nightly EXT_VERSION
    -----------   -----------------------------------
    0.1.0         0.1.0-nightly.20260618.1234
    1.2.3         1.2.3-nightly.20260618.1234
    0.1.0-beta    0.1.0-beta.nightly.20260618.1234

Why this ordering matters (semver precedence):

  * A stable release outranks any nightly built from the same base, so the eventual
    stable supersedes the nightly:
        0.1.0-nightly.20260618.1234  <  0.1.0
  * Later nightlies outrank earlier ones because the date and then the build id are
    compared numerically:
        0.1.0-nightly.20260618.1234  <  0.1.0-nightly.20260618.5678   (same day, higher build)
        0.1.0-nightly.20260618.9999  <  0.1.0-nightly.20260619.1000   (newer day wins)

Nightly mode and its build id can be supplied explicitly via parameters or implicitly
via the standard Azure DevOps predefined variables, so the build templates that
already invoke this script need no changes — the nightly pipeline only has to set
the AZD_NIGHTLY variable.

.PARAMETER ExtensionDirectory
Path to the extension directory containing version.txt.

.PARAMETER Nightly
Forces nightly mode. When omitted the AZD_NIGHTLY environment variable
(true/1) is honored instead.

.PARAMETER BuildId
Monotonic build identifier used as the nightly build number. Falls back to
BUILD_BUILDID.
#>
param(
    [string] $ExtensionDirectory,
    [switch] $Nightly,
    [string] $BuildId = $env:BUILD_BUILDID
)

$ErrorActionPreference = 'Stop'

$baseVersion = (Get-Content "$ExtensionDirectory/version.txt" -Raw).Trim()

$nightlyEnabled = $Nightly -or ($env:AZD_NIGHTLY -in @('true', '1', 'True'))

if (-not $nightlyEnabled) {
    Write-Host "Extension Version: $baseVersion"
    Write-Host "##vso[task.setvariable variable=EXT_VERSION;]$baseVersion"
    return
}

if ([string]::IsNullOrWhiteSpace($BuildId)) {
    throw "A build id is required for nightly builds. Pass -BuildId or set BUILD_BUILDID."
}

$date = [DateTime]::UtcNow.ToString('yyyyMMdd')

# Append the nightly identifiers to an existing prerelease (e.g. 0.1.0-preview ->
# 0.1.0-preview.nightly...) or introduce a new prerelease segment otherwise.
if ($baseVersion.Contains('-')) {
    $nightlyVersion = "$baseVersion.nightly.$date.$BuildId"
}
else {
    $nightlyVersion = "$baseVersion-nightly.$date.$BuildId"
}

Write-Host "Extension Version (nightly): $nightlyVersion"
Write-Host "##vso[task.setvariable variable=EXT_VERSION;]$nightlyVersion"
