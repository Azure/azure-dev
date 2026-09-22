param(
    $PackageName,
    $PackageVersion,
    $TimeoutInSeconds = 14400,
    $PollingIntervalInSeconds = 60
)

if (!(Test-Path wingetcreate.exe)) {
    Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile wingetcreate.exe
}

$startTime = Get-Date
$attempt = 0

while (((Get-Date) - $startTime).TotalSeconds -lt $TimeoutInSeconds) {
    $attempt++
    if ((.\wingetcreate.exe show $PackageName --version-manifest) -contains "PackageVersion: $PackageVersion") {
        Write-Host "Package $PackageName $PackageVersion is available"
        exit 0
    }

    $elapsed = [int]((Get-Date) - $startTime).TotalSeconds
    Write-Host "$PackageName $PackageVersion is not available after $elapsed seconds (attempt $attempt). Waiting..."
    Start-Sleep -Seconds $PollingIntervalInSeconds
}

Write-Error "Package $PackageName $PackageVersion was not available within $TimeoutInSeconds seconds. Check the ESRP Release UI and WinGet PR status before retrying."
exit 1
