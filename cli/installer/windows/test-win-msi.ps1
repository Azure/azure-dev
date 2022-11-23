param(
    [string] $MsiPath = "azd-windows-amd64.msi",
    [string] $InstallFolder = "$($env:USERPROFILE)\azd-install-test"
)

$MSIEXEC = "${env:SystemRoot}\System32\msiexec.exe"
$MsiPath = Resolve-Path $MsiPath


function assertSuccessfulExecution($errorMessage, $process) {
    $exitCode = $LASTEXITCODE
    if ($process) { 
        $exitCode = $process.ExitCode
    }

    if ($exitCode -or !$?) {
        Write-Error $errorMessage
        if ($exitCode) {
            exit $exitCode
        }
        exit 1
    }
}

$regKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
$originalPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$originalPathType = $regKey.GetValueKind('PATH')

$process = Start-Process $MSIEXEC `
    -ArgumentList "/i", $MsiPath, "/qn", "InstallDir=$InstallFolder" `
    -PassThru `
    -Wait
assertSuccessfulExecution "Install failed. Last exit code: $($process.ExitCode)" $process

$currentPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$expectedPathEntry = $InstallFolder

if (!$currentPath.Contains($expectedPathEntry)) {
  Write-Error "Could not find path entry"
  Write-Error "Expected substring: $expectedPathEntry"
  Write-Error "Actual: $path"
  exit 1
}

$afterInstallPathType = $regKey.GetValueKind('PATH')
if ($originalPathType -ne $afterInstallPathType) {
    Write-Error "Path registry key type does not match"
    Write-Error "Expected: $originalPathType"
    Write-Error "Actual: $afterInstallPathType"
    exit 1
}

& $InstallFolder/azd version
assertSuccessfulExecution "Could not execute 'azd version'"

$process = Start-Process $MSIEXEC `
    -ArgumentList "/x", $MsiPath, "/qn" `
    -PassThru `
    -Wait
assertSuccessfulExecution "Uninstall failed. Exit code: $($process.ExitCode)" $process

$currentPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$afterUninstallPathType = $regKey.GetValueKind('PATH')

if ($currentPath -ne $originalPath) {
    Write-Error "Path does not match original path after uninstall"
    Write-Error "Expected: $originalPath"
    Write-Error "Actual: $currentPath"
    exit 1
}

if ($originalPathType -ne $afterUninstallPathType) {
    Write-Error "Path registry key type does not match"
    Write-Error "Expected: $originalPathType"
    Write-Error "Actual: $afterUninstallPathType"
    exit 1
}

$azdCommand = Get-Command azd -ErrorAction Ignore
if ($azdCommand) {
    $sourceFolder = Split-Path -Parent (Resolve-Path $azdCommand.Source)
    if ($sourceFolder -eq $InstallFolder) {
        Write-Error "Command still availble in tested install location"
        exit 1
    }
}

Write-Host "Test succeeded"
exit 0
