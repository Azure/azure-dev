#!/bin/pwsh
<#
.SYNOPSIS
Build-Templates builds all repoman templates in the current repository and places it under <repository root>/.output folder.

.DESCRIPTION
Build-Templates places each template under <repository root>/.output/<template name>/generated.
It also preserves the .azure folder with each build attempt.

.PARAMETER Name
A name filter regex. If set, only templates with names matching the name regex pattern will be built.

.PARAMETER Path
The path to discover for templates. If set, only templates under the path will be built.

.EXAMPLE
Builds all templates in the current repository.
> ./eng/scripts/Build-Templates.ps1 

Builds the templates with the template name 'todo-csharp-mongo'
> ./eng/scripts/Build-Templates.ps1 -Name todo-csharp-mongo 
> ./eng/scripts/Build-Templates.ps1 todo-csharp-mongo

Builds the templates with template name containing 'csharp'
> ./eng/scripts/Build-Templates.ps1 csharp

Builds the templates under the template path 'python-mongo'
> ./eng/scripts/Build-Templates.ps1 -Path ./templates/todo/projects/python-mongo 

#>
param (
    [string]$Name,
    [string]$Path
)

$repoRootPath = Resolve-Path "$PSScriptRoot\..\.."
if ($Path) {
    $templatesPath = $Path 
} else {
    $templatesPath = Join-Path $repoRootPath "templates"
}
$outputPath = Join-Path $repoRootPath ".output"

function Build-Repoman {
    param()

    Push-Location (Join-Path (Join-Path $repoRootPath "generators") "repo")

    try {
        Write-Host "repoman command not built. Building repoman..."
        $err = npm install 2>&1
        if($LASTEXITCODE -ne 0){
            throw "repoman npm install failed: $err"
        }

        $err = npm run build 2>&1
        if($LASTEXITCODE -ne 0){
            throw "repoman npm run build failed: $err"
        }

        $err = npm link 2>&1
        if($LASTEXITCODE -ne 0){
            throw "repoman npm link failed: $err"
        }
        
        Write-Host "Built repoman successfully. The command 'repoman' can now be used."
    } finally { 
        Pop-Location
    }
}

function Get-AzdProjectSettingsDirectory {
    param(
        [string]$ProjectPath
    )

    return (Join-Path -Path $ProjectPath -ChildPath ".azure")
}

function Backup-AzdProjectSettings {
    param(
        [string]$ProjectPath,
        [string]$BackupPath
    )
    $settings = Get-AzdProjectSettingsDirectory $ProjectPath
    $projectName = Split-Path -Path $projectPath -Leaf
    $backupProjectPath = Join-Path $BackupPath $projectName

    if (Test-Path -Path $settings -PathType Container) {
        if (-not (Test-Path -Path $backupProjectPath -PathType Container)) {
            New-Item -Path $backupProjectPath -ItemType Directory | Out-Null
        }

        Copy-Item -Recurse -Force -Path $settings -Destination $backupProjectPath | Out-Null
    }
}

function Restore-AzdProjectSettings {
    param(
        [string]$ProjectPath,
        [string]$BackupPath
    )
    $projectName = Split-Path -Path $projectPath -Leaf
    $backupProjectPath = Join-Path $BackupPath $projectName

    if ((Test-Path -Path $backupProjectPath -PathType Container)) {
        Copy-Item -Recurse -Force -Path "$backupProjectPath\*" -Destination $ProjectPath | Out-Null
    }
}

if ($null -eq (Get-Command "repoman" -ErrorAction SilentlyContinue)) { 
    Build-Repoman
}

Push-Location -Path $templatesPath
$stopWatch = [System.Diagnostics.Stopwatch]::New()
$stopWatch.Start()
try {
    Write-Host "Gathering projects..."
    $output = repoman list --format json | Out-String
    if($LASTEXITCODE -ne 0){
        throw "repoman list failed: $output"
    }

    $projects = ConvertFrom-Json $output
    if ($Name) {
        $projects = $projects | Where-Object { $_.template.metadata.name -match $Name }
    }
    Write-Host "Found $($projects.Length) project(s)."

    foreach ($project in $projects) {
        $projectPath = $project.projectPath
        $templatePath = $project.templatePath.Replace($projectPath, "")

        $projectName = $project.template.metadata.name
        $generatedPath = Join-Path $outputPath $projectName
        # repoman always creates an extra folder "generated"
        $generatedContentPath = Join-Path $generatedPath "generated"
        $backupPath = Join-Path $generatedPath "backup"

        # Save any existing project settings
        Backup-AzdProjectSettings -ProjectPath $generatedContentPath -BackupPath $backupPath

        Write-Host "Generating $projectName..."
        $output = (repoman generate `
            -s $projectPath `
            -o $generatedPath `
            -t $templatePath) 2>&1
        if ($LASTEXITCODE -ne 0){
            throw "repoman generate failed for $($projectName): $output"
        }

        Restore-AzdProjectSettings -ProjectPath $generatedContentPath -BackupPath $backupPath
    }

    $stopWatch.Stop()
    Write-Host "Successfully generated $($projects.Length) project(s) in $($stopWatch.Elapsed)."
} finally {
    Pop-Location
}
