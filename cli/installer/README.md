# Azure Developer CLI Installer Scripts

## Install Azure Developer CLI

### Windows

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```

### Linux/MacOS

```
curl -fsSL https://aka.ms/install-azd.sh | bash
```

## Uninstall Azure Developer CLI

### Windows

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
