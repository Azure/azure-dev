#!/usr/bin/env pwsh
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
    [string] $BaseUrl = "https://azure-dev.azureedge.net/azd/standalone/release",
    [string] $Version = "latest",
    [switch] $DryRun,
    [string] $InstallFolder,
    [switch] $NoPath,
    [int] $DownloadTimeoutSeconds = 120
)

function isLinuxOrMac {
    return $IsLinux -or $IsMacOS
}

if (!$InstallFolder) {
    $InstallFolder = "$($env:LocalAppData)\Programs\Azure Dev CLI"
    if (isLinuxOrMac) {
        $InstallFolder = "/usr/local/bin"
    }
}

$binFilename = 'azd-windows-amd64.exe'
$extension = 'zip'
$packageFilename = "azd-windows-amd64.$extension"

if (isLinuxOrMac) {
    $platform = 'linux'
    $extension = 'tar.gz'
    if ($IsMacOS) {
        $platform = 'darwin'
        $extension = 'zip'
    }

    $architecture = 'amd64'
    $rawArchitecture = uname -m
    if ($rawArchitecture -eq 'arm64' -and $IsMacOS) {
        # In the case of Apple silicon, use amd64
        $architecture = 'amd64'
    }

    if ($architecture -ne 'amd64') {
        Write-Error "Architecture not supported: $rawArchitecture on platform: $platform"
    }

    Write-Verbose "Platform: $platform"
    Write-Verbose "Architecture: $architecture"
    Write-Verbose "Extension: $extension"

    $binFilename = "azd-$platform-$architecture"
    $packageFilename = "$binFilename.$extension"
}

$downloadUrl = "$BaseUrl/$packageFilename"
if ($Version) {
    $downloadUrl = "$BaseUrl/$Version/$packageFilename"
}

if ($DryRun) {
    Write-Host $downloadUrl
    exit 0
}

if (!(Test-Path $InstallFolder)) {
    New-Item -ItemType Directory -Path $InstallFolder -Force | Out-Null
}

$tempFolder = "$([System.IO.Path]::GetTempPath())$([System.IO.Path]::GetRandomFileName())"
Write-Verbose "Creating temporary folder for downloading and extracting binary: $tempFolder"
New-Item -ItemType Directory -Path $tempFolder | Out-Null

Write-Verbose "Downloading latest build from $downloadUrl" -Verbose:$Verbose
$releaseArchiveFileName = "$tempFolder/$packageFilename"
try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $releaseArchiveFileName -TimeoutSec $DownloadTimeoutSeconds
} catch {
    Write-Error "Error downloading $downloadUrl"
    Write-Error $_
    exit 1
}

Write-Verbose "Decompressing artifacts" -Verbose:$Verbose
if ($extension -eq 'zip') {
    try {
        Expand-Archive -Path $releaseArchiveFileName -DestinationPath $tempFolder/decompress
    } catch {
        Write-Error "Cannot expand $releaseArchiveFileName"
        Write-Error $_
        exit 1
    }
} elseif ($extension -eq 'tar.gz') {
    Write-Verbose "Extracting to $tempFolder/decompress"
    New-Item -ItemType Directory -Path "$tempFolder/decompress" | Out-Null
    Write-Host "tar -zxvf $releaseArchiveFileName -C `"$tempFolder/decompress`" $binFilename"
    tar -zxvf $releaseArchiveFileName -C "$tempFolder/decompress" $binFilename

    if ($LASTEXITCODE) {
        Write-Error "Cannot expand $releaseArchiveFileName"
        exit $LASTEXITCODE
    }
}

Write-Verbose "Installing azd in $InstallFolder" -Verbose:$Verbose

$outputFilename = "$InstallFolder/azd.exe"
if (isLinuxOrMac) {
    $outputFilename = "$InstallFolder/azd"
}

try {
    if (isLinuxOrMac) {
        test -w "$InstallFolder/"
        if ($LASTEXITCODE) {
            Write-Host "Writing to $InstallFolder/ requires elevated permission. You may be prompted to enter credentials."
            sudo cp "$tempFolder/decompress/$binFilename" $outputFilename
            if ($LASTEXITCODE) {
                Write-Error "Could not copy $tempfolder/decompress/$binFilename to $outputFilename"
                exit 1
            }
        } else {
            Copy-Item "$tempFolder/decompress/$binFilename" $outputFilename  -ErrorAction Stop| Out-Null
        }
    } else {
        Copy-Item "$tempFolder/decompress/$binFilename" $outputFilename  -ErrorAction Stop| Out-Null
    }
} catch {
    Write-Error "Could not copy to $InstallFolder"
    Write-Error $_
    exit 1
}

Write-Verbose "Cleaning temporary install directory: $tempFolder" -Verbose:$Verbose
Remove-Item $tempFolder -Recurse -Force | Out-Null

# $env:Path, [Environment]::GetEnvironmentVariable('PATH'), Get-ItemProperty,
# and setx all expand variables (e.g. %JAVA_HOME%) in the value. Writing the
# expanded paths back into the environment would be destructive so instead, read
# the PATH entry directly from the registry with the DoNotExpandEnvironmentNames
# option and update the PATH entry in the registry.
if (!$NoPath -and !(isLinuxOrMac)) {
    try {
        # Wrap the Microsoft.Win32.Registry calls in a script block to prevent
        # the type intializer from attempting to initialize those objects in
        # non-Windows environments.
        . {
            $registryKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
            $originalPath = $registryKey.GetValue(`
                'PATH', `
                '', `
                [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
            )
            $originalValueKind = $registryKey.GetValueKind('PATH')
        }
        $pathParts = $originalPath -split ';'

        if (!($pathParts -contains $InstallFolder)) {
            Write-Host "Adding $InstallFolder to PATH"

            $registryKey.SetValue( `
                'PATH', `
                "$originalPath;$InstallFolder", `
                $originalValueKind `
            )

            # Calling this method ensures that a WM_SETTINGCHANGE message is
            # sent to top level windows without having to pinvoke from
            # PowerShell. Setting to $null deletes the variable if it exists.
            [Environment]::SetEnvironmentVariable( `
                'AZD_INSTALLER_NOOP', `
                $null, `
                [EnvironmentVariableTarget]::User `
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

if (isLinuxOrMac) {
    Write-Host "Successfully installed to $InstallFolder"
} else {
    # Installed on Windows
    Write-Host "Azure Developer CLI (azd) installed successfully. You may need to restart running programs for installation to take effect."
    Write-Host "- For Windows Terminal, start a new Windows Terminal instance."
    Write-Host "- For VSCode, close all instances of VSCode and then restart it."
}

exit 0
