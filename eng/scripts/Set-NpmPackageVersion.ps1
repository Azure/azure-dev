param(
    $PackageJsonFile,
    $Version
)

$packageJsonContent = Get-Content $PackageJsonFile -Raw
$packageJson = ConvertFrom-Json $packageJsonContent
$packageJson.version = $Version
$packageJsonContent = ConvertTo-Json -Depth 100 $packageJson
Set-Content -Path $PackageJsonFile -Value $packageJsonContent
