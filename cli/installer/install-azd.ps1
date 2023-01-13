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
    [int] $DownloadTimeoutSeconds = 120,
    [switch] $NoTelemetry
)

function isLinuxOrMac {
    return $IsLinux -or $IsMacOS
}

# Does some very basic parsing of /etc/os-release to output the value present in
# the file. Since only lines that start with '#' are to be treated as comments
# according to `man os-release` there is no additional parsing of comments
# Options like:
# bash -c "set -o allexport; source /etc/os-release;set +o allexport; echo $VERSION_ID"
# were considered but it's possible that bash is not installed on the system and
# these commands would not be available.
function getOsReleaseValue($key) {
    $value = $null
    foreach ($line in Get-Content '/etc/os-release') {
        if ($line -like "$key=*") {
            # 'ID="value" -> @('ID', '"value"')
            $splitLine = $line.Split('=', 2)

            # Remove surrounding whitespaces and quotes
            # ` "value" ` -> `value`
            # `'value'` -> `value`
            $value = $splitLine[1].Trim().Trim(@("`"", "'"))
        }
    }
    return $value
}

function getOs {
    $os = [Environment]::OSVersion.Platform.ToString()
    try {
        if (isLinuxOrMac) {
            if ($IsLinux) {
                $os = getOsReleaseValue 'ID'
            } elseif ($IsMacOs) {
                $os = sw_vers -productName
            }
        }
    } catch {
        Write-Error "Error getting OS name $_"
        $os = "error"
    }
    return $os
}

function getOsVersion {
    $version = [Environment]::OSVersion.Version.ToString()
    try {
        if (isLinuxOrMac) {
            if ($IsLinux) {
                $version = getOsReleaseValue 'VERSION_ID'
            } elseif ($IsMacOS) {
                $version = sw_vers -productVersion
            }
        }
    } catch {
        Write-Error "Error getting OS version $_"
        $version = "error"
    }
    return $version
}

function isWsl {
    $isWsl = $false
    if ($IsLinux) {
        $kernelRelease = uname --kernel-release
        if ($kernelRelease -like '*wsl*') {
            $isWsl = $true
        }
    }
    return $isWsl
}

function getTerminal {
    return (Get-Process -Id $PID).ProcessName
}

function getExecutionEnvironment {
    $executionEnvironment = 'Desktop'
    if ($env:GITHUB_ACTIONS) {
        $executionEnvironment = 'GitHub Actions'
    } elseif ($env:SYSTEM_TEAMPROJECTID) {
        $executionEnvironment = 'Azure DevOps'
    }
    return $executionEnvironment
}

function promptForTelemetry {
    # UserInteractive may return $false if the session is not interactive
    # but this does not work in 100% of cases. For example, running:
    # "powershell -NonInteractive -c '[Environment]::UserInteractive'"
    # results in output of "True" even though the shell is not interactive.
    if (![Environment]::UserInteractive) {
        return $false
    }

    Write-Host "Answering 'yes' below will send data to Microsoft. To learn more about data collection see:"
    Write-Host "https://go.microsoft.com/fwlink/?LinkId=521839"
    Write-Host ""
    Write-Host "You can also file an issue at https://github.com/Azure/azure-dev/issues/new?assignees=&labels=&template=issue_report.md&title=%5BIssue%5D"

    try {
        $yes = New-Object System.Management.Automation.Host.ChoiceDescription `
            "&Yes", `
            "Sends failure report to Microsoft"
        $no = New-Object System.Management.Automation.Host.ChoiceDescription `
            "&No", `
            "Exits the script without sending a failure report to Microsoft (Default)"
        $options = [System.Management.Automation.Host.ChoiceDescription[]]($yes, $no)
        $decision = $Host.UI.PromptForChoice( `
            'Confirm issue report', `
            'Do you want to send diagnostic data about the failure to Microsoft?', `
            $options, `
            1 `                     # Default is 'No'
        )

        # Return $true if user consents
        return $decision -eq 0
    } catch {
        # Failure to prompt generally indicates that the environment is not
        # interactive and the default resposne can be assumed.
        return $false
    }
}

function reportTelemetryIfEnabled($eventName, $reason='', $additionalProperties = @{}) {
    if ($NoTelemetry -or $env:AZURE_DEV_COLLECT_TELEMETRY -eq 'no') {
        Write-Verbose "Telemetry disabled. No telemetry reported." -Verbose:$Verbose
        return
    }

    $IKEY = 'a9e6fa10-a9ac-4525-8388-22d39336ecc2'

    $telemetryObject = @{
        iKey = $IKEY;
        name = "Microsoft.ApplicationInsights.$($IKEY.Replace('-', '')).Event";
        time = (Get-Date).ToUniversalTime().ToString('o');
        data = @{
            baseType = 'EventData';
            baseData = @{
                ver = 2;
                name = $eventName;
                properties = @{
                    installVersion = $Version;
                    reason = $reason;
                    os = getOs;
                    osVersion = getOsVersion;
                    isWsl = isWsl;
                    terminal = getTerminal;
                    executionEnvironment = getExecutionEnvironment;
                };
            }
        }
    }

    # Add entries from $additionalProperties. These may overwrite existing
    # entries in the properties field.
    if ($additionalProperties -and $additionalProperties.Count) {
        foreach ($entry in $additionalProperties.GetEnumerator()) {
            $telemetryObject.data.baseData.properties[$entry.Name] = $entry.Value
        }
    }

    Write-Host "An error was encountered during install: $reason"
    Write-Host "Error data collected:"
    $telemetryDataTable = $telemetryObject.data.baseData.properties | Format-Table | Out-String
    Write-Host $telemetryDataTable
    if (!(promptForTelemetry)) {
        # The user responded 'no' to the telemetry prompt or is in a
        # non-interactive session. Do not send telemetry.
        return
    }

    try {
        Invoke-RestMethod `
            -Uri 'https://centralus-2.in.applicationinsights.azure.com/v2/track' `
            -ContentType 'application/json' `
            -Method Post `
            -Body (ConvertTo-Json -InputObject $telemetryObject -Depth 100 -Compress) | Out-Null
        Write-Verbose -Verbose:$Verbose "Telemetry posted"
    } catch {
        Write-Host $_
        Write-Verbose -Verbose:$Verbose "Telemetry post failed"
    }
}

try {
    if (isLinuxOrMac -and !$InstallFolder) {
        $InstallFolder = "/usr/local/bin"
    }

    $binFilename = ''
    $extension = 'msi'
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

    $tempFolder = "$([System.IO.Path]::GetTempPath())$([System.IO.Path]::GetRandomFileName())"
    Write-Verbose "Creating temporary folder for downloading package: $tempFolder"
    New-Item -ItemType Directory -Path $tempFolder | Out-Null

    Write-Verbose "Downloading build from $downloadUrl" -Verbose:$Verbose
    $releaseArtifactFilename = Join-Path $tempFolder $packageFilename
    try {
        $LASTEXITCODE = 0
        Invoke-WebRequest -Uri $downloadUrl -OutFile $releaseArtifactFilename -TimeoutSec $DownloadTimeoutSeconds
        if ($LASTEXITCODE) {
            throw "Invoke-WebRequest failed with nonzero exit code: $LASTEXITCODE"
        }
    } catch {
        Write-Error "Error downloading $downloadUrl"
        Write-Error $_
        reportTelemetryIfEnabled 'InstallFailed' 'DownloadFailed' @{ downloadUrl = $downloadUrl }
        exit 1
    }

    Write-Verbose "Decompressing artifacts" -Verbose:$Verbose
    if ($extension -eq 'zip') {
        try {
            Expand-Archive -Path $releaseArtifactFilename -DestinationPath $tempFolder/decompress
        } catch {
            Write-Error "Cannot expand $releaseArtifactFilename"
            Write-Error $_
            reportTelemetryIfEnabled 'InstallFailed' 'ArchiveDecompressionFailed'
            exit 1
        }
    } elseif ($extension -eq 'tar.gz') {
        Write-Verbose "Extracting to $tempFolder/decompress"
        New-Item -ItemType Directory -Path "$tempFolder/decompress" | Out-Null
        Write-Host "tar -zxvf $releaseArtifactFilename -C `"$tempFolder/decompress`" $binFilename"
        tar -zxvf $releaseArtifactFilename -C "$tempFolder/decompress" $binFilename

        if ($LASTEXITCODE) {
            Write-Error "Cannot expand $releaseArtifactFilename"
            reportTelemetryIfEnabled 'InstallFailed' 'ArchiveDecompressionFailed'
            exit $LASTEXITCODE
        }
    } else { 
        Write-Verbose "Decompression not required" -Verbose:$Verbose
    }

    

    try {
        if (isLinuxOrMac) {
            Write-Verbose "Installing azd in $InstallFolder" -Verbose:$Verbose
            if (!(Test-Path $InstallFolder)) {
                New-Item -ItemType Directory -Path $InstallFolder -Force | Out-Null
            }
            $outputFilename = "$InstallFolder/azd"
            test -w "$InstallFolder/"
            if ($LASTEXITCODE) {
                Write-Host "Writing to $InstallFolder/ requires elevated permission. You may be prompted to enter credentials."
                sudo cp "$tempFolder/decompress/$binFilename" $outputFilename
                if ($LASTEXITCODE) {
                    Write-Error "Could not copy $tempfolder/decompress/$binFilename to $outputFilename"
                    reportTelemetryIfEnabled 'InstallFailed' 'FileCopyFailed'
                    exit 1
                }
            } else {
                Copy-Item "$tempFolder/decompress/$binFilename" $outputFilename  -ErrorAction Stop | Out-Null
            }
        } else {
            Write-Verbose "Installing MSI" -Verbose:$Verbose
            $MSIEXEC = "${env:SystemRoot}\System32\msiexec.exe"
            $installProcess = Start-Process $MSIEXEC `
                -ArgumentList @("/i", $releaseArtifactFilename, "/qn", "INSTALLDIR=`"$InstallFolder`"") `
                -PassThru `
                -Wait

            if ($installProcess.ExitCode) {
                Write-Error "Could not install MSI at $releaseArtifactFilename"
                reportTelemetryIfEnabled 'InstallFailed' 'MsiFailure'
                exit 1
            }
        }
    } catch {
        Write-Error "Could not copy to $InstallFolder"
        Write-Error $_
        reportTelemetryIfEnabled 'InstallFailed' 'FileCopyFailed'
        exit 1
    }

    Write-Verbose "Cleaning temporary install directory: $tempFolder" -Verbose:$Verbose
    Remove-Item $tempFolder -Recurse -Force | Out-Null

    if (isLinuxOrMac) {
        Write-Host "Successfully installed to $InstallFolder"
    } else {
        Write-Host "Successfully install azd"
        # Installed on Windows
        Write-Host "Azure Developer CLI (azd) installed successfully. You may need to restart running programs for installation to take effect."
        Write-Host "- For Windows Terminal, start a new Windows Terminal instance."
        Write-Host "- For VSCode, close all instances of VSCode and then restart it."
    }
    Write-Host ""
    Write-Host "The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience."
    Write-Host "You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use."
    Write-Host ""
    Write-Host "Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection"

    exit 0
} catch {
    Write-Error "Unhandled error"
    Write-Error $_
    reportTelemetryIfEnabled 'InstallFailed' 'UnhandledError' @{ exceptionName = $_.Exception.GetType().Name; }
    exit 1
}