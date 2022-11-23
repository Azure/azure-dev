param(
    $Version = "0.0.1"
)

$currentLocation = Get-Location 

try { 
    Set-Location $PSScriptRoot
    dotnet msbuild /p:ProductVersion=$Version
} finally { 
    Set-Location $currentLocation
}
