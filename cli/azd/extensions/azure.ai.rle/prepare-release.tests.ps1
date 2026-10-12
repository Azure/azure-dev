$ErrorActionPreference = "Stop"
$gitExecutable = (Get-Command git -CommandType Application).Source
$copyItemCommand = Get-Command Copy-Item -CommandType Cmdlet
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("rle-release-test-" + [Guid]::NewGuid().ToString("N"))
$previousRegistryURL = $env:RLE_REGISTRY_URL
$previousRegistrySource = $env:RLE_REGISTRY_SOURCE
$previousRepositoryPath = $env:AZURE_DEV_REPOSITORY_PATH
$global:rleReleaseTestState = @{
    remote = Join-Path $testRoot "remote.git"
    gitCalls = @()
    buildCount = 0
    packCount = 0
    publishCount = 0
    corruptCopy = $false
}

function Copy-Item {
    & $copyItemCommand @args
    if ($global:rleReleaseTestState.corruptCopy) {
        $destination = $args[[Array]::IndexOf($args, "-Destination") + 1]
        $sourceFile = $args[[Array]::IndexOf($args, "-LiteralPath") + 1]
        Set-Content -LiteralPath (Join-Path $destination (Split-Path -Leaf $sourceFile)) -Value "corrupt"
    }
}

function Assert-Release {
    param([bool] $Condition, [string] $Message)
    if (-not $Condition) { throw $Message }
}

function Assert-ReleaseFailure {
    param([scriptblock] $Action, [string] $Pattern)
    $message = $null
    try { & $Action | Out-Null } catch { $message = $_.Exception.Message }
    Assert-Release ($null -ne $message -and $message -like "*$Pattern*") "Expected '$Pattern', got '$message'."
}

function git {
    $arguments = @($args)
    $global:rleReleaseTestState.gitCalls += ($arguments -join " ")
    $fetchIndex = [Array]::IndexOf($arguments, "fetch")
    if ($fetchIndex -ge 0) {
        $arguments[$fetchIndex + 1] = $global:rleReleaseTestState.remote
    }
    & $gitExecutable @arguments
    $global:LASTEXITCODE = $LASTEXITCODE
}

function azd {
    $global:LASTEXITCODE = 0
    if ($args[1] -eq "build") {
        $global:rleReleaseTestState.buildCount++
        Set-Content -LiteralPath (Join-Path $PWD "bin\fixture-binary") -Value "channel-neutral"
    }
    elseif ($args[1] -eq "pack") {
        $global:rleReleaseTestState.packCount++
        $output = $args[[Array]::IndexOf($args, "--output") + 1]
        foreach ($platform in @("windows-amd64", "windows-arm64", "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")) {
            $suffix = if ($platform.StartsWith("linux")) { "tar.gz" } else { "zip" }
            Set-Content -LiteralPath (Join-Path $output "azure-ai-rle-$platform.$suffix") -Value "fixture-$platform"
        }
    }
    elseif ($args[1] -eq "publish") {
        $global:rleReleaseTestState.publishCount++
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

function Publish-TestCheckout {
    & $gitExecutable -C $public add --force -- "cli/azd/extensions/azure.ai.rle/artifacts"
    & $gitExecutable -C $public add -- "cli/azd/extensions/registry.rle.json"
    & $gitExecutable -C $public commit -m "Fixture release" | Out-Null
    & $gitExecutable -C $public push $global:rleReleaseTestState.remote rle-releases | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Fixture publication failed." }
}

function Invoke-TestRelease {
    param([string] $Path, [switch] $BreakingChanges)
    & $wrapper -AzureDevRepositoryPath $Path -BreakingChanges:$BreakingChanges
}

try {
    $source = Join-Path $testRoot "source"
    $extension = Join-Path $source "extension"
    $public = Join-Path $testRoot "public"
    New-Item -ItemType Directory -Path $extension, $public -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "prepare-release.ps1"),
        (Join-Path $PSScriptRoot "prepare-ext-release.ps1"),
        (Join-Path $PSScriptRoot "prepare-dev-release.ps1"),
        (Join-Path $PSScriptRoot "get-development-repository.ps1") -Destination $extension
    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.3-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.3-preview"
    foreach ($repository in @($source, $public)) {
        & $gitExecutable -C $repository init | Out-Null
        & $gitExecutable -C $repository config user.name "Release Test"
        & $gitExecutable -C $repository config user.email "release-test@example.test"
        & $gitExecutable -C $repository config commit.gpgsign false
        & $gitExecutable -C $repository commit --allow-empty -m "Base" | Out-Null
    }
    & $gitExecutable -C $source remote add origin https://github.com/test-owner/rle-implementation.git
    $repositoryResolver = Join-Path $extension "get-development-repository.ps1"
    foreach ($origin in @(
        "https://github.com/test-owner/rle-implementation.git",
        "https://github.com/test-owner/rle-implementation",
        "git@github.com:test-owner/rle-implementation.git",
        "ssh://git@github.com/test-owner/rle-implementation.git"
    )) {
        & $gitExecutable -C $source remote set-url origin $origin
        Assert-Release ((& $repositoryResolver) -eq "test-owner/rle-implementation") "Wrong inferred development repository."
    }
    & $gitExecutable -C $source remote set-url origin https://example.test/test-owner/rle-implementation.git
    Assert-ReleaseFailure { & (Join-Path $extension "prepare-dev-release.ps1") } "GitHub HTTPS or SSH"
    & $gitExecutable -C $source remote set-url origin https://token@example.test/test-owner/rle-implementation.git
    Assert-ReleaseFailure { & $repositoryResolver } "without embedded credentials"
    & $gitExecutable -C $source remote remove origin
    Assert-ReleaseFailure { & (Join-Path $extension "prepare-dev-release.ps1") } "Could not read"
    Assert-Release ((Get-Content (Join-Path $extension "version.txt")).Trim() -eq "1.2.3-preview") "Invalid dev origin bumped version."
    & $gitExecutable -C $source remote add origin https://github.com/test-owner/rle-implementation.git
    Set-Content -LiteralPath (Join-Path $public ".gitignore") -Value "artifacts/"
    & $gitExecutable -C $public add .gitignore
    & $gitExecutable -C $public commit -m "Ignore artifacts" | Out-Null
    & $gitExecutable -C $public branch -M main
    & $gitExecutable init --bare $global:rleReleaseTestState.remote | Out-Null
    & $gitExecutable -C $public push $global:rleReleaseTestState.remote main | Out-Null
    & $gitExecutable -C $public switch -c rle-releases | Out-Null
    & $gitExecutable -C $public push $global:rleReleaseTestState.remote rle-releases | Out-Null
    & $gitExecutable -C $public remote add origin https://github.com/Azure/azure-dev.git
    $wrapper = Join-Path $extension "prepare-ext-release.ps1"
    $registryPath = Join-Path $public "cli\azd\extensions\registry.rle.json"
    $env:RLE_REGISTRY_URL = "test-url"
    $env:RLE_REGISTRY_SOURCE = "test-source"
    $head = & $gitExecutable -C $public rev-parse HEAD

    $env:AZURE_DEV_REPOSITORY_PATH = $public
    $missingParameter = & pwsh -NoProfile -NonInteractive -File $wrapper 2>&1 | Out-String
    Assert-Release ($LASTEXITCODE -ne 0 -and $missingParameter.Contains("AzureDevRepositoryPath")) "Missing path parameter did not fail; the environment variable must not provide a fallback."
    Assert-ReleaseFailure { Invoke-TestRelease "" } "empty"
    Assert-ReleaseFailure { Invoke-TestRelease "   " } "must not be blank"
    $env:AZURE_DEV_REPOSITORY_PATH = Join-Path $testRoot "ignored-environment-path"
    Assert-ReleaseFailure { Invoke-TestRelease (Join-Path $testRoot "missing") } "existing"
    Assert-Release (-not (Test-Path (Join-Path $testRoot "missing"))) "Script created a checkout."
    Assert-ReleaseFailure { Invoke-TestRelease $source } "outside"
    $subdirectory = Join-Path $public "subdirectory"
    New-Item -ItemType Directory -Path $subdirectory | Out-Null
    Assert-ReleaseFailure { Invoke-TestRelease $subdirectory } "must be the root"
    & $gitExecutable -C $public switch main | Out-Null
    Assert-ReleaseFailure { Invoke-TestRelease $public } "must be on"
    & $gitExecutable -C $public switch rle-releases | Out-Null
    & $gitExecutable -C $public remote set-url origin https://github.com/example/fork.git
    Assert-ReleaseFailure { Invoke-TestRelease $public } "not a fork"
    & $gitExecutable -C $public remote set-url origin https://github.com/Azure/azure-dev.git
    Set-Content -LiteralPath (Join-Path $public "dirty.txt") -Value "dirty"
    Assert-ReleaseFailure { Invoke-TestRelease $public } "must be clean"
    & $gitExecutable -C $public add dirty.txt
    Assert-ReleaseFailure { Invoke-TestRelease $public } "must be clean"
    & $gitExecutable -C $public restore --staged dirty.txt
    Remove-Item -LiteralPath (Join-Path $public "dirty.txt")
    & $gitExecutable -C $public commit --allow-empty -m "Unpublished" | Out-Null
    Assert-ReleaseFailure { Invoke-TestRelease $public } "unpublished or diverged"
    & $gitExecutable -C $public reset --soft $head
    Set-Content -LiteralPath (Join-Path $public ".git\MERGE_HEAD") -Value $head
    Assert-ReleaseFailure { Invoke-TestRelease $public } "current Git operation"
    Remove-Item -LiteralPath (Join-Path $public ".git\MERGE_HEAD")
    Assert-Release ((Get-Content (Join-Path $extension "version.txt")).Trim() -eq "1.2.3-preview") "Rejected checkout bumped source version."

    $global:rleReleaseTestState.gitCalls = @()
    Invoke-TestRelease $public -BreakingChanges | Out-Null
    Assert-Release ((& $gitExecutable -C $public rev-parse HEAD) -eq $head) "Script committed changes."
    Assert-Release (-not (& $gitExecutable -C $public diff --cached --name-only)) "Script staged changes."
    Assert-Release (@($global:rleReleaseTestState.gitCalls | Where-Object { $_ -match " (clone|add|commit|push) " }).Count -eq 0) "Script published or staged changes."
    $first = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json
    $firstVersion = $first.extensions[0].versions[0]
    Assert-Release ($firstVersion.version -eq "1.2.4-preview" -and $firstVersion.breakingChanges) "Breaking metadata mismatch."
    $firstJSON = $firstVersion | ConvertTo-Json -Depth 100 -Compress
    Assert-Release ($firstVersion.artifacts.PSObject.Properties.Name.Count -eq 6) "Expected six platforms."
    $firstDev = Get-Content (Join-Path $source "registry.rle-dev.json") -Raw | ConvertFrom-Json
    $firstDevVersion = $firstDev.extensions[0].versions[0]
    Assert-Release ($firstDevVersion.version -eq $firstVersion.version -and $firstDevVersion.breakingChanges) "External preparation did not also prepare the same breaking dev version."
    Assert-Release ($global:rleReleaseTestState.buildCount -eq 1 -and $global:rleReleaseTestState.packCount -eq 1) "External preparation must build and pack once."
    Assert-Release ($global:rleReleaseTestState.publishCount -eq 2) "External preparation must update both registries."
    Assert-Release (-not (Test-Path (Join-Path $extension "artifacts\rle-ext\1.2.4-preview"))) "External staging was not cleaned."
    Assert-Release (-not (Test-Path (Join-Path $extension "bin"))) "External binaries were not cleaned."
    Assert-Release (Test-Path (Join-Path $extension "artifacts\rle-dev\1.2.4-preview")) "Dev packages were removed."
    Assert-Release ((Get-Content (Join-Path $extension "version.txt")).Trim() -eq "1.2.4-preview") "External preparation bumped version more than once."
    foreach ($artifact in $firstVersion.artifacts.PSObject.Properties) {
        Assert-Release ($artifact.Value.url.StartsWith("https://raw.githubusercontent.com/Azure/azure-dev/rle-releases/")) "Wrong branch artifact URL."
        $file = Join-Path $public ("cli\azd\extensions\azure.ai.rle\artifacts\1.2.4-preview\" + (Split-Path -Leaf $artifact.Value.url))
        Assert-Release ((Get-FileHash $file -Algorithm SHA256).Hash -eq $artifact.Value.checksum.value) "Checksum mismatch."
        Assert-Release ($artifact.Value.checksum.value -eq $firstDevVersion.artifacts.($artifact.Name).checksum.value) "Customer and dev package checksums differ."
    }
    foreach ($artifact in $firstDevVersion.artifacts.PSObject.Properties) {
        Assert-Release ($artifact.Value.url.StartsWith("https://raw.githubusercontent.com/test-owner/rle-implementation/main/cli/azd/extensions/azure.ai.rle/artifacts/rle-dev/1.2.4-preview/")) "External preparation used the wrong dev artifact URL."
        $file = Join-Path $extension ("artifacts\rle-dev\1.2.4-preview\" + (Split-Path -Leaf $artifact.Value.url))
        Assert-Release ((Get-FileHash $file -Algorithm SHA256).Hash -eq $artifact.Value.checksum.value) "External preparation dev checksum mismatch."
    }
    Assert-Release ($env:RLE_REGISTRY_URL -eq "test-url" -and $env:RLE_REGISTRY_SOURCE -eq "test-source") "Build environment was not restored."
    Assert-ReleaseFailure { Invoke-TestRelease $public } "must be clean"
    Publish-TestCheckout
    Set-Content -LiteralPath (Join-Path $public "source.txt") -Value "not a release"
    & $gitExecutable -C $public add source.txt
    & $gitExecutable -C $public commit -m "Invalid distribution change" | Out-Null
    $beforeValidationBuilds = $global:rleReleaseTestState.buildCount
    & $gitExecutable -C $public push $global:rleReleaseTestState.remote rle-releases | Out-Null
    Assert-ReleaseFailure { Invoke-TestRelease $public } "non-release change"
    Assert-Release ($global:rleReleaseTestState.buildCount -eq $beforeValidationBuilds) "Invalid branch triggered a build."
    & $gitExecutable -C $public rm source.txt | Out-Null
    & $gitExecutable -C $public commit -m "Remove invalid change" | Out-Null
    & $gitExecutable -C $public push $global:rleReleaseTestState.remote rle-releases | Out-Null

    # Simulate another publisher advancing the remote; this checkout must fast-forward.
    $publisher = Join-Path $testRoot "publisher"
    & $gitExecutable clone --branch rle-releases $global:rleReleaseTestState.remote $publisher | Out-Null
    & $gitExecutable -C $publisher config user.name "Release Test"
    & $gitExecutable -C $publisher config user.email "release-test@example.test"
    & $gitExecutable -C $publisher -c commit.gpgsign=false commit --allow-empty -m "Remote advance" | Out-Null
    & $gitExecutable -C $publisher push origin rle-releases | Out-Null
    $remoteHead = & $gitExecutable -C $publisher rev-parse HEAD
    $beforeDivergence = & $gitExecutable -C $public rev-parse HEAD
    & $gitExecutable -C $public commit --allow-empty -m "Diverged local change" | Out-Null
    Assert-ReleaseFailure { Invoke-TestRelease $public } "unpublished or diverged"
    & $gitExecutable -C $public reset --soft $beforeDivergence
    Invoke-TestRelease $public | Out-Null
    Assert-Release ((& $gitExecutable -C $public rev-parse HEAD) -eq $remoteHead) "Checkout did not fast-forward."
    $second = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json
    Assert-Release ($second.extensions[0].versions.Count -eq 2) "Previous version lost."
    Assert-Release (($second.extensions[0].versions[0] | ConvertTo-Json -Depth 100 -Compress) -eq $firstJSON) "Previous metadata changed."
    Assert-Release (-not ($second.extensions[0].versions[1].PSObject.Properties.Name -contains "breakingChanges")) "Non-breaking flag mismatch."
    $secondDev = Get-Content (Join-Path $source "registry.rle-dev.json") -Raw | ConvertFrom-Json
    Assert-Release ($secondDev.extensions[0].versions.Count -eq 2) "Dev version history lost."
    Assert-Release ($secondDev.extensions[0].versions[0].breakingChanges) "Previous dev breaking flag lost."
    Assert-Release (-not ($secondDev.extensions[0].versions[1].PSObject.Properties.Name -contains "breakingChanges")) "Wrong new dev breaking flag."
    Assert-Release ($secondDev.extensions[0].versions[1].version -eq $second.extensions[0].versions[1].version) "Channel versions differ."
    Publish-TestCheckout
    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.3-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.3-preview"
    Assert-ReleaseFailure { Invoke-TestRelease $public } "must not be overwritten"
    Set-Content -LiteralPath (Join-Path $extension "version.txt") -Value "1.2.5-preview"
    Set-Content -LiteralPath (Join-Path $extension "extension.yaml") -Value "version: 1.2.5-preview"
    $devHead = & $gitExecutable -C $source rev-parse HEAD
    $publicJSON = Get-Content -LiteralPath $registryPath -Raw
    $beforeDevBuilds = $global:rleReleaseTestState.buildCount
    $beforeDevPublishes = $global:rleReleaseTestState.publishCount
    $global:rleReleaseTestState.gitCalls = @()
    & (Join-Path $extension "prepare-dev-release.ps1") | Out-Null
    Assert-Release ($global:rleReleaseTestState.buildCount -eq $beforeDevBuilds + 1) "Dev preparation must build once."
    Assert-Release ($global:rleReleaseTestState.publishCount -eq $beforeDevPublishes + 1) "Dev preparation must update only one registry."
    Assert-Release ((Get-Content -LiteralPath $registryPath -Raw) -eq $publicJSON) "Dev preparation modified the customer registry."
    Assert-Release ((& $gitExecutable -C $source rev-parse HEAD) -eq $devHead) "Development script committed changes."
    Assert-Release (-not (& $gitExecutable -C $source diff --cached --name-only)) "Development script staged changes."
    Assert-Release (@($global:rleReleaseTestState.gitCalls | Where-Object { $_ -match " (clone|add|commit|push) " }).Count -eq 0) "Development script published or staged changes."
    $devRegistry = Get-Content (Join-Path $source "registry.rle-dev.json") -Raw | ConvertFrom-Json
    foreach ($artifact in $devRegistry.extensions[0].versions[-1].artifacts.PSObject.Properties) {
        $expectedURL = "https://raw.githubusercontent.com/test-owner/rle-implementation/main/cli/azd/extensions/azure.ai.rle/artifacts/rle-dev/1.2.6-preview/"
        Assert-Release ($artifact.Value.url.StartsWith($expectedURL)) "Wrong development artifact URL."
        $file = Join-Path $extension ("artifacts\rle-dev\1.2.6-preview\" + (Split-Path -Leaf $artifact.Value.url))
        Assert-Release ((Get-FileHash $file -Algorithm SHA256).Hash -eq $artifact.Value.checksum.value) "Development checksum mismatch."
    }
    $global:rleReleaseTestState.corruptCopy = $true
    Assert-ReleaseFailure { Invoke-TestRelease $public } "failed checksum verification"
    Assert-Release (Test-Path (Join-Path $extension "bin")) "Failed copy removed recovery binaries."
    Assert-Release (Test-Path (Join-Path $extension "artifacts\rle-dev\1.2.7-preview")) "Failed copy removed dev packages."
    $global:rleReleaseTestState.corruptCopy = $false
    Write-Host "PASS: required path parameter with no environment fallback, origin inference, checkout validation, dev-only and dual registries, one build/pack/version bump, identical checksums, history, flags, local-only state, cleanup and failed-copy retention."
}
finally {
    $env:RLE_REGISTRY_URL = $previousRegistryURL
    $env:RLE_REGISTRY_SOURCE = $previousRegistrySource
    $env:AZURE_DEV_REPOSITORY_PATH = $previousRepositoryPath
    Remove-Variable rleReleaseTestState -Scope Global
    Remove-Item -LiteralPath $testRoot -Recurse -Force
}
