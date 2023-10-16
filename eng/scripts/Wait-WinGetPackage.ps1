param(
    $PackageName,
    $PackageVersion,
    $TimeoutInSeconds = 300
)

$startTime = Get-Date

while (((Get-Date) - $startTime).TotalSeconds -lt $TimeoutInSeconds) {
    if ((winget show $PackageName --versions) -contains $PackageVersion) {
        Write-Host "Package $PackageName $PackageVersion is available"
        exit 0
    }

    Write-Host "$PackageName $PackageVersion is not available yet. Waiting..."
    Start-Sleep -Seconds 10
}

Write-Host "Package not found before expiration."
exit 1
