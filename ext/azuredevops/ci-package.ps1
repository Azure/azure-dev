#!/usr/bin/env pwsh

$originalLocation = Get-Location 

try {
  Set-Location $PSScriptRoot 
  
  Write-Host "Running tsc build"
  npm --prefix $PSScriptRoot/setupAzd/ run build
  if ($LASTEXITCODE) {
    Write-Host "Build failed"  
    exit $LASTEXITCODE
  }
  Write-Host "Building Azure DevOps extension package"
  tfx extension create --manifest-globs vss-extension.json
  if ($LASTEXITCODE) {
    Write-Host "Packaging failed"  
    exit $LASTEXITCODE
  }

} finally { 
  Set-Location $originalLocation
}
