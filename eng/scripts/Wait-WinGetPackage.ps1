param(
    $PackageName,
    $PackageVersion,
    $TimeoutInSeconds = 300
)
$PSNativeCommandArgumentPassing = 'Legacy'

if (!(Test-Path wingetcreate.exe)) {
    Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile wingetcreate.exe
}

$startTime = Get-Date

while (((Get-Date) - $startTime).TotalSeconds -lt $TimeoutInSeconds) {
    if ((.\wingetcreate.exe show $PackageName --version-manifest) -contains "PackageVersion: $PackageVersion") {
        Write-Host "Package $PackageName $PackageVersion is available"
        exit 0
    }

    Write-Host "$PackageName $PackageVersion is not available yet. Waiting..."
    Start-Sleep -Seconds 10
}

Write-Host "Package not found before expiration."
exit 1
