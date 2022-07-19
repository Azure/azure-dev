<#
.SYNOPSIS
Download and install azd on the local machine.

.DESCRIPTION
Downloads and installs azd on the local machine. Includes ability to configure
download and install locations.

.PARAMETER BaseUrl
Specifies the base URL to use when downloading. Default is
https://azure-dev.azureedge.net/azd/standalone

.PARAMETER Version
Specifies the version to use. Default is `latest`. Valid values include a
SemVer version number (e.g. 1.0.0 or 1.1.0-beta.1), `latest`, `daily`

.PARAMETER DryRun
Print the download URL and quit. Does not download or install.

.PARAMETER InstallFolder
Location to install azd.

.PARAMETER NoPath
Do not update the PATH environment variable with the location of azd.

.PARAMETER DownloadTimeoutSeconds
Download timeout in seconds. Default is 120 (2 minutes).

.EXAMPLE
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"

Install the azd CLI from a Windows shell

The use of `-ex AllSigned` is intended to handle the scenario where a machine's
default execution policy is restricted such that modules used by
`install-azd.ps1` cannot be loaded. Because this syntax is piping output from
`Invoke-RestMethod` to `Invoke-Expression` there is no direct valication of the
`install-azd.ps1` script's signature. Validation of the script can be
accomplished by downloading the script to a file and executing the script file.

.EXAMPLE
Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'
PS > ./install-azd.ps1

Download the installer and execute from PowerShell

.EXAMPLE
Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'
PS > ./install-azd.ps1 -Version daily

Download the installer and install the "daily" build
#>

param(
    [string] $BaseUrl = "https://azuresdkreleasepreview.blob.core.windows.net/azd/standalone/release",
    [string] $Version = "daily",
    [switch] $DryRun,
    [string] $InstallFolder = "$($env:LocalAppData)\Programs\Azure Dev CLI",
    [switch] $NoPath,
    [int] $DownloadTimeoutSeconds = 120
)

if ($IsLinux -or $IsMacOS) {
    Write-Error "This bootstrap script is intended to run on Windows. Use the install-azd.sh instead."
    exit 1
}

$packageFilename = 'azd-windows-amd64.zip'
$downloadUrl = "$BaseUrl/$packageFilename"
if ($Version) {
    $downloadUrl = "$BaseUrl/$Version/$packageFilename"
}

if ($DryRun) {
    Write-Host $downloadUrl
    exit 0
}

New-Item -ItemType Directory -Path $InstallFolder -Force | Out-Null

$tempFolder = "$([System.IO.Path]::GetTempPath())$([System.IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $tempFolder | Out-Null

Write-Verbose "Downloading latest build from $downloadUrl" -Verbose:$Verbose
$zipFileName = "$tempFolder\azd-windows-amd64.zip"
try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $zipFileName -TimeoutSec $DownloadTimeoutSeconds
} catch {
    Write-Error "Error downloading $downloadUrl"
    Write-Error $_
    exit 1
}

Write-Verbose "Unzipping artifacts" -Verbose:$Verbose
try {
    Expand-Archive -Path $zipFileName -DestinationPath $tempFolder/unzip
} catch {
    Write-Error "Cannot expand $zipFileName"
    Write-Error $_
    exit 1
}

Write-Verbose "Installing azd in $InstallFolder" -Verbose:$Verbose
try {
    Copy-Item "$tempFolder/unzip/azd-windows-amd64.exe" "$InstallFolder/azd.exe" | Out-Null
} catch {
    Write-Error "Could not copy to $InstallFolder"
    Write-Error $_
    exit 1
}

Write-Verbose "Cleaning temporary install directory: $tempFolder" -Verbose:$Verbose
Remove-Item $tempFolder -Recurse -Force | Out-Null

# $env:Path, [Environment]::GetEnvironmentVariable('PATH'), and setx all expand
# variables (e.g. %JAVA_HOME%) in the value. Writing the expanded paths back
# into the environment would be destructive so instead, read the path directly
# from the registry with the DoNotExpandEnvironmentNames option and write that
# value back using the non-destructive [Environment]::SetEnvironmentVariable
# which also broadcasts environment variable changes to Windows.
if (!$NoPath) {
    try {
        $registryKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
        $originalPath = $registryKey.GetValue(`
            'PATH', `
            '', `
            [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
        )
        $pathParts = $originalPath -split ';'

        if (!($pathParts -contains $InstallFolder)) {
            Write-Host "Adding $InstallFolder to PATH"

            # SetEnvironmentVariable broadcasts the "Environment" change to
            # Windows and is NOT destructive (e.g. expanding variables)
            [Environment]::SetEnvironmentVariable(
                'PATH', `
                "$originalPath;$InstallFolder", `
                [EnvironmentVariableTarget]::User`
            )

            # Also add the path to the current session
            $env:PATH += ";$InstallFolder"
        } else {
            Write-Host "An entry for $InstallFolder is already in PATH"
        }
    } finally {
        if ($registryKey) {
            $registryKey.Close()
        }
    }
}

Write-Host "Azure Developer CLI (azd) installed successfully. You may need to restart running programs for installation to take effect."
Write-Host "- For Windows Terminal, start a new Windows Terminal instance."
Write-Host "- For VSCode, close all instances of VSCode and then restart it."
