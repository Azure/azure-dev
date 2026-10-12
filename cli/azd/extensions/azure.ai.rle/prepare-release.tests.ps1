$ErrorActionPreference = "Stop"
$gitExecutable = (Get-Command git -CommandType Application).Source
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("rle-release-test-" + [Guid]::NewGuid().ToString("N"))
$previousRegistryURL = $env:RLE_REGISTRY_URL
$global:rleReleaseTestState = @{
    ghCalls = @()
    buildURLs = @()
    openPR = $false
    failPush = $false
    cloneSource = $null
    baseCommit = $null
    prBodies = @()
}

function Assert-Release {
    param([bool] $Condition, [string] $Message)
    if (-not $Condition) { throw $Message }
}

function Assert-ReleaseFailure {
    param([scriptblock] $Action, [string] $Pattern)
    $message = $null
    try { & $Action | Out-Null } catch { $message = $_.Exception.Message }
    Assert-Release ($null -ne $message -and $message -like "*$Pattern*") "Expected failure matching '$Pattern', got '$message'."
}

function git {
    $arguments = @($args)
    if ($arguments -contains "clone" -and $global:rleReleaseTestState.cloneSource) {
        $destination = $arguments[-1]
        & $gitExecutable clone --no-checkout --single-branch --branch rle-releases `
            $global:rleReleaseTestState.cloneSource $destination | Out-Null
        & $gitExecutable -C $destination remote set-url origin https://github.com/Azure/azure-dev.git
        & $gitExecutable -C $destination update-ref refs/remotes/origin/main $global:rleReleaseTestState.baseCommit
        & $gitExecutable -C $destination config user.name "Release Test"
        & $gitExecutable -C $destination config user.email "release-test@example.test"
        & $gitExecutable -C $destination config commit.gpgsign false
        $global:LASTEXITCODE = $LASTEXITCODE
        return
    }
    if ($arguments -contains "fetch") {
        $global:LASTEXITCODE = 0
        return
    }
    if ($arguments -contains "ls-remote") {
        $global:LASTEXITCODE = 0
        if ($global:rleReleaseTestState.cloneSource) { "test-commit refs/heads/rle-releases" }
        return
    }
    if ($arguments -contains "push") {
        $global:LASTEXITCODE = if ($global:rleReleaseTestState.failPush) { 1 } else { 0 }
        return
    }
    & $gitExecutable @arguments
    $global:LASTEXITCODE = $LASTEXITCODE
}

function gh {
    $global:rleReleaseTestState.ghCalls += ($args -join " ")
    $global:LASTEXITCODE = 0
    if ($args[0] -eq "pr" -and $args[1] -eq "list") {
        if ($global:rleReleaseTestState.openPR) { '[{"url":"https://github.com/Azure/azure-dev/pull/1"}]' } else { '[]' }
    }
    if ($args[0] -eq "pr" -and $args[1] -eq "create") {
        $global:rleReleaseTestState.prBodies += $args[[Array]::IndexOf($args, "--body") + 1]
        $global:rleReleaseTestState.openPR = $true
        "https://github.com/Azure/azure-dev/pull/1"
    }
}

function azd {
    $global:LASTEXITCODE = 0
    if ($args[1] -eq "build") {
        $global:rleReleaseTestState.buildURLs += $env:RLE_REGISTRY_URL
    }
    elseif ($args[1] -eq "pack") {
        $output = $args[[Array]::IndexOf($args, "--output") + 1]
        foreach ($platform in @("windows-amd64", "windows-arm64", "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")) {
            $suffix = if ($platform.StartsWith("linux")) { "tar.gz" } else { "zip" }
            Set-Content -LiteralPath (Join-Path $output "azure-ai-rle-$platform.$suffix") -Value "fixture-$platform"
        }
    }
    elseif ($args[1] -eq "publish") {
        $path = $args[[Array]::IndexOf($args, "--registry") + 1]
        $version = $args[[Array]::IndexOf($args, "--version") + 1]
        $patterns = $args[[Array]::IndexOf($args, "--artifacts") + 1] -split ","
        $registry = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
        $artifacts = [ordered]@{}
        foreach ($file in Get-ChildItem -Path $patterns -File) {
            $platform = [regex]::Match($file.Name, "azure-ai-rle-(\w+)-(\w+)\.").Groups
            $artifacts["$($platform[1].Value)/$($platform[2].Value)"] = @{
                url = $file.FullName
                checksum = @{ algorithm = "sha256"; value = (Get-FileHash $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
            }
        }
        $entry = @{ version = $version; artifacts = $artifacts }
        if (@($registry.extensions).Count -eq 0) {
            $registry.extensions = @(@{ id = "azure.ai.rle"; versions = @($entry) })
        }
        else {
            $registry.extensions[0].versions = @($registry.extensions[0].versions) + @($entry)
        }
        $registry | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $path
    }
}

try {
    $source = Join-Path $testRoot "source"
    $extension = Join-Path $source "extension"
    $public = Join-Path $testRoot "public"
    New-Item -ItemType Directory -Path $extension, $public -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "prepare-release.ps1"),
        (Join-Path $PSScriptRoot "prepare-ext-release.ps1"),
        (Join-Path $PSScriptRoot "prepare-dev-release.ps1") -Destination $extension
    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.3-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.3-preview"
    foreach ($repository in @($source, $public)) {
        git -C $repository init | Out-Null
        git -C $repository config user.name "Release Test"
        git -C $repository config user.email "release-test@example.test"
        git -C $repository -c commit.gpgsign=false commit --allow-empty -m "Base" | Out-Null
    }
    Set-Content -LiteralPath (Join-Path $public ".gitignore") -Value "artifacts/"
    git -C $public add .gitignore
    git -C $public -c commit.gpgsign=false commit -m "Ignore build artifacts" | Out-Null
    git -C $public remote add origin https://github.com/Azure/azure-dev.git
    git -C $public update-ref refs/remotes/origin/main HEAD
    git -C $public switch -c rle-releases | Out-Null
    git -C $public config commit.gpgsign false
    $wrapper = Join-Path $extension "prepare-ext-release.ps1"
    $registryPath = Join-Path $public "cli\azd\extensions\registry.rle.json"
    $env:RLE_REGISTRY_URL = "test-environment-value"

    & $wrapper -PublicRepositoryPath $public -BreakingChanges | Out-Null
    $publicBody = $global:rleReleaseTestState.prBodies[0]
    foreach ($command in @(
        'azd extension source add --name rle --type url --location "https://raw.githubusercontent.com/Azure/azure-dev/rle-releases/cli/azd/extensions/registry.rle.json"',
        "azd extension install azure.ai.rle --source rle",
        "azd ai rle version",
        "azd ai rle --help",
        "azd extension update azure.ai.rle --source rle"
    )) {
        Assert-Release ($publicBody.Contains($command)) "Public PR description is missing customer command: $command"
    }
    Assert-Release ($publicBody -notmatch "(?i)implementation|internal|follow-up|publishing commit|future external|no merge|same branch") "Public PR description contains internal release decisions."
    $first = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json
    $firstVersion = $first.extensions[0].versions[0]
    Assert-Release ($firstVersion.version -eq "1.2.4-preview" -and $firstVersion.breakingChanges) "First release metadata mismatch."
    $firstURLs = @($firstVersion.artifacts.PSObject.Properties | ForEach-Object { $_.Value.url })
    Assert-Release ($firstURLs.Count -eq 6) "Expected six artifacts."
    foreach ($url in $firstURLs) {
        Assert-Release ($url -match "^https://raw.githubusercontent.com/Azure/azure-dev/[0-9a-f]{40}/") "Artifact is not commit-pinned: $url"
    }
    Assert-Release ($env:RLE_REGISTRY_URL -eq "test-environment-value") "Registry environment was not restored."
    Assert-Release (-not (Test-Path (Join-Path $source "registry.rle.json"))) "Customer registry leaked into source checkout."
    Assert-Release (-not (Test-Path (Join-Path $source "registry.rle-ext.json"))) "Legacy external registry leaked into source checkout."
    Assert-Release (-not (git -C $public status --porcelain)) "Public checkout is not clean."
    $changed = @(git -C $public diff --name-only origin/main HEAD)
    Assert-Release ($changed.Count -eq 7) "Expected only registry and six archives in public diff."

    & $wrapper -PublicRepositoryPath $public | Out-Null
    $second = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json
    Assert-Release ($second.extensions[0].versions.Count -eq 2) "Previous release was lost."
    $preservedURLs = @($second.extensions[0].versions[0].artifacts.PSObject.Properties | ForEach-Object { $_.Value.url })
    Assert-Release (($firstURLs -join "|") -eq ($preservedURLs -join "|")) "Previous artifact URLs changed."
    Assert-Release $second.extensions[0].versions[0].breakingChanges "Previous breaking flag was lost."
    Assert-Release (-not ($second.extensions[0].versions[1].PSObject.Properties.Name -contains "breakingChanges")) "Non-breaking release has a breaking flag."
    Assert-Release (@($global:rleReleaseTestState.ghCalls | Where-Object { $_ -like "pr create *" }).Count -eq 1) "Existing PR was not reused."
    Assert-Release (@($global:rleReleaseTestState.buildURLs | Where-Object { $_ -ne "https://raw.githubusercontent.com/Azure/azure-dev/rle-releases/cli/azd/extensions/registry.rle.json" }).Count -eq 0) "External binary embedded the wrong registry URL."

    $global:rleReleaseTestState.cloneSource = $public
    $global:rleReleaseTestState.baseCommit = git -C $public rev-parse origin/main
    $freshPublic = Join-Path $testRoot "fresh-public"
    & $wrapper -PublicRepositoryPath $freshPublic | Out-Null
    Assert-Release (Test-Path (Join-Path $freshPublic "cli\azd\extensions\registry.rle.json")) "First-run clone failed."
    Assert-Release (-not (git -C $freshPublic status --porcelain)) "First-run clone is not clean."
    Assert-Release (@($global:rleReleaseTestState.ghCalls | Where-Object { $_ -like "pr create *" }).Count -eq 1) "First-run clone did not reuse the public PR."
    $global:rleReleaseTestState.cloneSource = $null

    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.3-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.3-preview"
    Assert-ReleaseFailure { & $wrapper -PublicRepositoryPath $public } "must not be overwritten"
    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.6-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.6-preview"

    Set-Content -LiteralPath (Join-Path $public "unexpected.txt") -Value "dirty"
    Assert-ReleaseFailure { & $wrapper -PublicRepositoryPath $public } "must be clean"
    Remove-Item -LiteralPath (Join-Path $public "unexpected.txt")
    Assert-ReleaseFailure { & $wrapper -PublicRepositoryPath $source } "outside the source repository"
    git -C $public remote set-url origin https://github.com/example/fork.git
    Assert-ReleaseFailure { & $wrapper -PublicRepositoryPath $public } "not a fork"
    git -C $public remote set-url origin https://github.com/Azure/azure-dev.git
    $global:rleReleaseTestState.failPush = $true
    Assert-ReleaseFailure { & $wrapper -PublicRepositoryPath $public } "push"
    Assert-Release (@($global:rleReleaseTestState.ghCalls | Where-Object { $_ -like "pr create *" }).Count -eq 1) "Failed push created a PR."

    & (Join-Path $extension "prepare-dev-release.ps1") -BreakingChanges | Out-Null
    Assert-Release ($global:rleReleaseTestState.buildURLs[-1] -eq "https://raw.githubusercontent.com/sujit-kamireddy/azure-dev/main/cli/azd/extensions/registry.rle-dev.json") "Development registry changed."
    Assert-Release (Test-Path (Join-Path $source "registry.rle-dev.json")) "Development registry was not prepared."
    Write-Host "PASS: external publishing, pinned artifacts, PR reuse, failure guards, environment restoration, and internal channel."
}
finally {
    $env:RLE_REGISTRY_URL = $previousRegistryURL
    Remove-Variable rleReleaseTestState -Scope Global
    Remove-Item -LiteralPath $testRoot -Recurse -Force
}
