<#
.SYNOPSIS
Checks for existence of specified tag and release, outputs tag to GitHub Actions

.DESCRIPTION
Check for the existence of the tag and the related release. If either the tag or
the release already exists the script will exit with an error. Otherwise set a
property on the step's output based on the `-OutputName` parameter.

.PARAMETER Tag
Tag to check and set.

.PARAMETER OutputName
Variable name for output. Default is `github-tag`

.PARAMETER DevOpsOutputFormat
If set, formats output for DevOps. Default is false. Default output is GitHub
format.

#>

param(
    [string] $Tag,
    [string] $OutputName = 'github-tag',
    [switch] $DevOpsOutputFormat
)

$PSNativeCommandArgumentPassing = 'Legacy'

git fetch --tags
$existingTag = git tag -l $Tag

if ($existingTag) {
    Write-Error "Tag ($Tag) exists. Exiting."
    exit 1
}

gh release view $Tag
if ($LASTEXITCODE -eq 0) {
    Write-Error "Release ($Tag) exists. Exiting."
    exit 1
}

if ($DevOpsOutputFormat) {
    Write-Host "##vso[task.setvariable variable=$OutputName;]$Tag"
} else {
    Write-Host "::set-output name=$OutputName::$Tag"
}

# Exit 0 to ensure $LASTEXITCODE is 0 and CI continues
exit 0