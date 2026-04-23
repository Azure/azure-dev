param(
    [Parameter(Mandatory = $true)]
    [string] $NewVersion
)

Set-StrictMode -Version 4
$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path "$PSScriptRoot/../../"

# Canonical source of truth
$coreGoMod = Join-Path $repoRoot 'cli/azd/go.mod'

# All go.mod files that should track the same Go version (including testdata samples).
$goModFiles = Get-ChildItem -Path (Join-Path $repoRoot 'cli/azd') -Recurse -Filter 'go.mod'

# ADO pipeline template that pins the Go toolchain version
$adoSetupGo = Join-Path $repoRoot 'eng/pipelines/templates/steps/setup-go.yml'

$updated = @()
$skipped = @()

# --- Update go.mod files ---
foreach ($file in $goModFiles) {
    $content = Get-Content $file.FullName -Raw
    if ($content -match '(?m)^go\s+\S+') {
        $newContent = $content -replace '(?m)^go\s+\S+', "go $NewVersion"
        if ($newContent -ne $content) {
            Set-Content -Path $file.FullName -Value $newContent -NoNewline -Encoding utf8NoBOM
            $updated += $file.FullName.Substring($repoRoot.Path.Length)
        } else {
            $skipped += $file.FullName.Substring($repoRoot.Path.Length)
        }
    }
}

# --- Update ADO pipeline template ---
if (Test-Path $adoSetupGo) {
    $content = Get-Content $adoSetupGo -Raw
    $newContent = $content -replace '(?m)^(\s+GoVersion:\s+)\S+', "`${1}$NewVersion"
    if ($newContent -ne $content) {
        Set-Content -Path $adoSetupGo -Value $newContent -NoNewline -Encoding utf8NoBOM
        $updated += $adoSetupGo.Substring($repoRoot.Path.Length)
    } else {
        $skipped += $adoSetupGo.Substring($repoRoot.Path.Length)
    }
}

# --- Update Dockerfiles referencing golang:<version> base images ---
$dockerfiles = Get-ChildItem -Path (Join-Path $repoRoot 'cli/azd') -Recurse -Filter 'Dockerfile'
foreach ($file in $dockerfiles) {
    $content = Get-Content $file.FullName -Raw
    if ($content -match 'golang:\d+\.\d+') {
        $newContent = $content -replace 'golang:\d+[\d.]*', "golang:$NewVersion"
        if ($newContent -ne $content) {
            Set-Content -Path $file.FullName -Value $newContent -NoNewline -Encoding utf8NoBOM
            $updated += $file.FullName.Substring($repoRoot.Path.Length)
        } else {
            $skipped += $file.FullName.Substring($repoRoot.Path.Length)
        }
    }
}

# --- Update devcontainer.json Go feature version ---
$devcontainer = Join-Path $repoRoot '.devcontainer/devcontainer.json'
if (Test-Path $devcontainer) {
    $content = Get-Content $devcontainer -Raw
    $newContent = $content -replace '("ghcr\.io/devcontainers/features/go:\d+":\s*\{\s*"version":\s*")[\d.]+(")', "`${1}$NewVersion`${2}"
    if ($newContent -ne $content) {
        Set-Content -Path $devcontainer -Value $newContent -NoNewline -Encoding utf8NoBOM
        $updated += $devcontainer.Substring($repoRoot.Path.Length)
    } else {
        $skipped += $devcontainer.Substring($repoRoot.Path.Length)
    }
}

# --- Report ---
Write-Host ""
if ($updated.Count -gt 0) {
    Write-Host "Updated $($updated.Count) file(s) to Go $NewVersion`:" -ForegroundColor Green
    $updated | ForEach-Object { Write-Host "  $_" }
} else {
    Write-Host "No files needed updating." -ForegroundColor Yellow
}

if ($skipped.Count -gt 0) {
    Write-Host ""
    Write-Host "Already at Go $NewVersion ($($skipped.Count) file(s)):" -ForegroundColor Cyan
    $skipped | ForEach-Object { Write-Host "  $_" }
}

Write-Host ""
Write-Host "Done. GitHub Actions workflows read the version from cli/azd/go.mod automatically." -ForegroundColor Green
Write-Host "Run 'git diff' to review changes before committing." -ForegroundColor Gray
