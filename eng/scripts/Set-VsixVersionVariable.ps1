$packageJsonContent = Get-Content $PSScriptRoot/../../ext/vscode/package.json -Raw
$packageJson = ConvertFrom-Json $packageJsonContent
$vsixVersion = $packageJson.version
Write-Host "CLI Version: $vsixVersion"
Write-Host "##vso[task.setvariable variable=VSIX_VERSION;]$vsixVersion"