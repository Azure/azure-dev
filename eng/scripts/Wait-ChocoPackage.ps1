param(
    $PackageName,
    $PackageVersion,
    $TimeoutInSeconds = 300
)
$PSNativeCommandArgumentPassing = 'Legacy'

$startTime = Get-Date

while (((Get-Date) - $startTime).TotalSeconds -lt $TimeoutInSeconds) {
    if (choco info $PackageName --version=$PackageVersion --limit-output) { 
        Write-Host "Package $PackageName $PackageVersion is available"
        exit 0
    }

    Write-Host "$PackageName $PackageVersion is not available yet. Waiting..."
    Start-Sleep -Seconds 10
}

Write-Host "Package not found before expiration."
exit 1
