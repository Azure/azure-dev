param(
    [string] $InstallFolder = "$($env:LocalAppData)\Programs\Azure Dev CLI"
)

if ($IsLinux -or $IsMacOS) {
    Write-Error "This bootstrap script is intended to run on Windows. Use the install-azd.sh instead."
    exit 1
}

if (Test-Path $InstallFolder) {
    Write-Host "Remove Install folder: $InstallFolder"
    Remove-Item $InstallFolder -Recurse -Force
} else {
    Write-Host "azd is not installed at $InstallFolder. To install run:"
    Write-Host "powershell -c `"if ((Get-ExecutionPolicy) -ne 'Unrestricted') { Set-ExecutionPolicy -ExecutionPolicy 'Unrestricted' -Scope 'Process' }; Invoke-WebRequest -Uri 'https://aka.ms/install-azd.ps1' -OutFile install-azd.ps1; ./install-azd.ps1`"`n"
}

# $env:Path, [Environment]::GetEnvironmentVariable('PATH'), and setx all expand
# variables (e.g. %JAVA_HOME%) in the value. Writing the expanded paths back
# into the environment would be destructive so instead, read the path directly
# from the registry with the DoNotExpandEnvironmentNames option and write that
# value back using the non-destructive [Environment]::SetEnvironmentVariable
# which also broadcasts environment variable changes to Windows.
try {
    $registryKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
    $originalPath = $registryKey.GetValue(`
        'PATH', `
        '', `
        [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames `
    )
    $pathParts = $originalPath -split ';'

    if ($pathParts -contains $InstallFolder) {
        Write-Host "Removing $InstallFolder from PATH"
        $newPathParts = $pathParts.Where({ $_ -ne $InstallFolder })
        $newPath = $newPathParts -join ';'

        # SetEnvironmentVariable broadcasts the "Environment" change to Windows
        # and is NOT destructive (e.g. expanding variables)
        [Environment]::SetEnvironmentVariable(
            'PATH', `
            $newPath, `
            [EnvironmentVariableTarget]::User `
        )
    } else {
        Write-Host "Could not find an entry for $InstallFolder in PATH"
    }
} finally {
    if ($registryKey) {
        $registryKey.Close()
    }
}
