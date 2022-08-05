#!/usr/bin/env pwsh
param(
    [string] $InstallFolder = ""
)


# Windows specific:
# This functions sends a WM_SETTINGCHANGE message which causes new processes to
# pick up the updates to environment variables. Not calling this funciton after
# updating environment variables means that new processes will use an older view
# of the environment variables.
function broadcastSettingChange {
    $SEND_MESSAGE_TIMEOUT_DEFINITION = @"
    [DllImport("user32.dll")]public static extern IntPtr SendMessageTimeout(
        IntPtr hWnd,
        uint Msg,
        UIntPtr wParam,
        string lParam,
        uint fuFlags,
        uint uTimeout,
        out UIntPtr lpdwResult
    );
"@

    # Broadcast environment variable change to Windows. Processes launched after
    # this broadcast will use the new PATH environment variable.
    # Use "Environment" as the lParam
    # https://docs.microsoft.com/en-us/windows/win32/winmsg/wm-settingchange
    Write-Verbose "Broadcasting environment variable change to Windows"
    $sendMessageTimeout = Add-Type `
        -MemberDefinition $SEND_MESSAGE_TIMEOUT_DEFINITION `
        -Name 'Win322SendMessageTimeout' `
        -Namespace Win32Functions `
        -PassThru
    $messageResult = [UIntPtr]::Zero
    $result = $sendMessageTimeout::SendMessageTimeout(
        [IntPtr] 0xffff,        # HWND_BROADCAST
        0x001A,                 # WM_SETTINGCHANGE
        [UIntPtr]::Zero,
        "Environment",
        0x0002,                 # SMTO_ABORTIFHUNG
        1000,                   # Wait 1000ms per window
        [ref] $messageResult
    )

    if ($result -eq 0) {
        Write-Error "Windows runtime environment variable update did not succeed. To use azd, log out of Windows and log back in."
    }
}

function isLinuxOrMac {
    return $IsLinux -or $IsMacOS
}

if (!$InstallFolder) {
    $InstallFolder = "$($env:LocalAppData)\Programs\Azure Dev CLI"
    if (isLinuxOrMac) {
        $InstallFolder = "/usr/local/bin"
    }
}

if (isLinuxOrMac) {
    $installLocation = "$InstallFolder/azd"

    if (!(Test-Path $installLocation)) {
        Write-Host "azd is not installed at $installLocation. To install run:"
        Write-Host "pwsh -c `"Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression`""
        exit 0
    }

    Write-Host "Removing install: $installLocation"
    test -w $installLocation
    if ($LASTEXITCODE) {
        Write-Host "Writing to $InstallFolder/ requires elevated permission. You may be promtped to enter credentials."
        sudo rm $installLocation
        if ($LASTEXITCODE) {
            Write-Error "Could not remove azd from $installLocation"
            exit 1
        }
    } else {
        Remove-Item $installLocation
    }
} else {
    if (Test-Path $InstallFolder) {
        Write-Host "Remove Install folder: $InstallFolder"
        Remove-Item $InstallFolder -Recurse -Force
    } else {
        Write-Host "azd is not installed at $InstallFolder. To install run:"
        Write-Host "powershell -ex AllSigned -c `"Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression`"`n"
    }

    # $env:Path, [Environment]::GetEnvironmentVariable('PATH'), Get-ItemProperty,
    # and setx all expand variables (e.g. %JAVA_HOME%) in the value. Writing the
    # expanded paths back into the environment would be destructive so instead, read
    # the PATH entry directly from the registry with the DoNotExpandEnvironmentNames
    # option and update the PATH entry using Set-ItemProperty
    try {
        . {
            # Wrap the Microsoft.Win32.Registry calls in a script block to
            # prevent the type intializer from attempting to initialize those
            # objects in non-Windows environments.
            $registryKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
            $originalPath = $registryKey.GetValue(`
                'PATH', `
                '', `
                [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
            )
            $originalValueKind = $registryKey.GetValueKind('PATH')
        }
        $pathParts = $originalPath -split ';'

        if ($pathParts -contains $InstallFolder) {
            Write-Host "Removing $InstallFolder from PATH"
            $newPathParts = $pathParts.Where({ $_ -ne $InstallFolder })
            $newPath = $newPathParts -join ';'

            $registryKey.SetValue( `
                'PATH', `
                $newPath, `
                $originalValueKind `
            )
            broadcastSettingChange
        } else {
            Write-Host "Could not find an entry for $InstallFolder in PATH"
        }
    } finally {
        if ($registryKey) {
            $registryKey.Close()
        }
    }
}
