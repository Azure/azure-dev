param(
    [string] $MsiPath = "azd-windows-amd64.msi",
    [switch] $PerMachine
)

$MSIEXEC = "${env:SystemRoot}\System32\msiexec.exe"
$MsiPath = Resolve-Path $MsiPath

$installFolder = "$($env:LocalAppData)\Programs\Azure Dev CLI"
$registry = [Microsoft.Win32.Registry]::CurrentUser
$environmentSubkeyPath = 'Environment'
$additionalParameters = ""
if ($PerMachine) { 
    $installFolder = "$($env:ProgramFiles)\Azure Dev CLI"
    $registry = [Microsoft.Win32.Registry]::LocalMachine
    $environmentSubkeyPath = "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"
    $additionalParameters = "ALLUSERS=1"
}


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

$regKey = $registry.OpenSubKey($environmentSubkeyPath, $false)
$originalPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)

$process = Start-Process $MSIEXEC `
    -ArgumentList "/i", $MsiPath, "/qn", $additionalParameters `
    -PassThru `
    -Wait
assertSuccessfulExecution "Install failed. Last exit code: $($process.ExitCode)" $process

$currentPath = $regKey.GetValue( `
    'PATH', `
    '', `
    [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
)
$expectedPathEntry = $installFolder

if (!$currentPath.Contains($expectedPathEntry)) {
  Write-Error "Could not find path entry"
  Write-Error "Expected substring: $expectedPathEntry"
  Write-Error "Actual: $path"
  exit 1
}

& $installFolder/azd version
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

if ($currentPath.TrimEnd(";") -ne $originalPath.TrimEnd(";")) {
    Write-Error "Path does not match original path after uninstall"
    Write-Error "Expected: $originalPath"
    Write-Error "Actual: $currentPath"
    exit 1
}

$azdCommand = Get-Command azd -ErrorAction Ignore
if ($azdCommand) {
    $sourceFolder = Split-Path -Parent (Resolve-Path $azdCommand.Source)
    if ($sourceFolder -eq $installFolder) {
        Write-Error "Command still availble in tested install location"
        exit 1
    }
}

Write-Host "Test succeeded"
exit 0
