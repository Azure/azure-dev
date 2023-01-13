# Azure Developer CLI Installer Scripts

## Install Azure Developer CLI

### Windows

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```

#### MSI installation

Windows installations of the Azure Developer CLI now use MSI. The PowerShell script downloads the specified MSI file and installs it using `msiexec.exe`. 

See [MSI configuration](#msi-configuration) for advanced install scenarios.


### Linux/MacOS

```
curl -fsSL https://aka.ms/install-azd.sh | bash
```

## Uninstall Azure Developer CLI

### Windows

#### Uninstalling 0.5.0-beta.1 and later

The Azure Developer CLI uses MSI to install on Windows. Use the "Add or remove programs" dialog in Windows to remove the "Azure Developer CLI" application. 

#### Uninstalling version 0.4.0-beta.1 and earlier

Use this PowerShell script to uninstall Azure Developer CLI 0.4.0-beta.1 and earlier.

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/uninstall-azd.ps1' | Invoke-Expression"
```

### Linux/MacOS

```
curl -fsSL https://aka.ms/uninstall-azd.sh | bash
```

## Advanced installation scenarios

Both the PowerShell and Linux/MacOS scripts can be downloaded and executed locally with parameters that provide additional functionality like setting the version to download and specifying where to install files.

These scripts can be used, for example, to ensure a particular version of azd is installed in a CI/CD environment.

### MSI configuration

For more adavanced installs, the MSI can be downloaded from the release in [GitHub Releases](https://github.com/Azure/azure-dev/releases). 

When installing using the MSI directly (instead of the install script) the MSI behavior can be modified by providing the following parameters to `msiexec.exe`: 

| Parameters | Value |
| -------- | ----- |
| `ALLUSERS` | `2`: Default. Install for current user (no privilege elevation required). <br/> `1`: Install for _all_ users (may require privilege elevation). |
| `INSTALLDIR` | Installation path. <br/> `"%LOCALAPPDATA%\Programs\Azure Dev CLI"`: Default. <br/> `"%PROGRAMFILES%\Azure Dev CLI"`: Default all users. |

### Download from daily builds 

The `daily` feed is periodically updated with builds from the latest source code in the `main` branch. Use the `version` parameter to download the latest daily release.

#### Windows


```pwsh
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'; ./install-azd.ps1 -Version 'daily'"
```

#### Linux/MacOS

```bash
curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --version daily
```

### Download Windows installer (PowerShell)

To download the script, use the same URL and send the output to a file.

```powershell
Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'
```

To learn more about the install script parameters

```powershell
Get-Help ./install-azd.ps1 -Detailed
```

To download and install the "daily" version of azd (most recent build)

```powershell
./install-azd.ps1 -Version daily
```

#### Verifying the install script in PowerShell

The PowerShell install script is Authenticode signed and the signature will be automatically verified when running the script from a file on disk.

The script signature is not validated when piping output from `Invoke-RestMethod` to `Invoke-Expression`. The use of `-ex AllSigned` in the simple install scenario handles situations where the default execution policy for PowerShell is too restrictive to run cmdlets that `install-azd.ps1` requires to perform the installation.

### Download Linux/MacOS installer

```bash
curl -fsSL 'https://aka.ms/install-azd.sh' > install-azd.sh
chmod +x install-azd.sh
```

To learn more about the install script parameters

```bash
./install-azd.sh --help
```

To download and install the "daily" version of azd (most recent build)

```bash
./install-azd.sh --version daily
```
