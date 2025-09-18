# Azure Developer CLI Installer Scripts

## Install Azure Developer CLI

### Windows

#### Windows Package Manager (winget)

```powershell
winget install microsoft.azd
```

#### Chocolatey

```powershell
choco install azd
```

#### Install script

The install script downloads and installs the MSI package on the machine with default parameters.

```powershell
powershell -nop -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```

#### MSI installation

Windows installations of the Azure Developer CLI now use MSI. The PowerShell script downloads the specified MSI file and installs it using `msiexec.exe`. 

See [MSI configuration](#msi-configuration) for advanced install scenarios.

### MacOS

#### Homebrew (recommended)

```bash
brew tap azure/azd && brew install azd
```

The `brew tap azure/azd` command only needs to be run once to configure the tap in `brew`.

If using `brew` to upgrade `azd` from a version not installed using `brew`, remove the existing version of `azd` using the uninstall script (if installed to the default location) or by deleting the `azd` binary manually.

#### Script

The install script can be used to install `azd` at the machine scope.

```bash
curl -fsSL https://aka.ms/install-azd.sh | bash
```

### Linux

#### Script

```bash
curl -fsSL https://aka.ms/install-azd.sh | bash
```

#### DEB/RPM Packages

The Azure Developer CLI releases signed `.deb` and `.rpm` packages to [GitHub Releases](https://github.com/Azure/azure-dev/releases). To install, download the appropriate file from the GitHub release and run the appropriate command to install the package: 

##### .deb package (distros using apt-get)

You may need to use `sudo` when running `apt`

```bash 
curl -fSL https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_<version>/azd_<version>_amd64.deb -o azd_<version>_amd64.deb

apt update 
apt install ./azd_<version>_amd64.deb -y
```

##### .rpm package (distros using yum/tdnf)

You may need to use `sudo` when running `yum`

```bash 
curl -fSL https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_<version>/azd-<version>-1.x86_64.rpm -o azd-<version>-1.x86_64.rpm

yum install -y azd-<version>-1.x86_64.rpm 
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

#### Uninstall script

If you installed `azd` using the install script, you can use the uninstall script to remove `azd`. 

```
curl -fsSL https://aka.ms/uninstall-azd.sh | bash
```

#### DEB/RPM packages

If you installed `azd` using one of the .deb or .rpm packages, use the appropriate uninstall method for your package manager.

##### .deb package

You may need to use `sudo` when running `apt`.

```bash 
apt remove -y azd
```

##### .rpm package

You may need to use `sudo` when running `yum`.

```bash 
yum remove -y azd
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

### Custom install location 

#### Windows 

The installer script can specify a custom location to the MSI installation: 

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'; ./install-azd.ps1 -InstallFolder 'C:\utils\azd'"
```

#### Linux/MacOS

Specify the `--install-folder` when running the script. For example: 

```bash 
curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --install-folder "~/mybin"`
```

The `--install-folder` parameter places the `azd` binary in the specified location. If the current user has write access to that location the install script will not attempt to elevate permissions using `sudo`. If the specified install folder does not exist the install will fail.

### Download from daily builds 

The `daily` feed is periodically updated with builds from the latest source code in the `main` branch. Use the `version` parameter to download the latest daily release.

#### Windows

##### Install

```pwsh
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile 'install-azd.ps1'; ./install-azd.ps1 -Version 'daily'"
```

##### Uninstall or switch to another version

To uninstall a daily version of `azd` or switch to another version you will need to first uninstall "Azure Developer CLI" using the "Add or remove programs" dialog. This is because daily builds often has a version number that supersedes the `latest` build.


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

## Uninstall

The Azure Developer CLI will write files to `~/.azd/` that are specific to the application's usage. Since this is user data uninstall processes do not alter or remove this data.

### Windows 
 For versions released after `0.5.0-beta.1` use the following procedure to remove `azd`: 

1. Search for `Add or remove programs` in Windows
2. Locate `Azure Developer CLI` 
3. Select `Uninstall`

Uninstall script for version s released before `0.5.0-beta.1` (does not work on versions `0.5.0-beta.1` and later): 

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/uninstall-azd.ps1' | Invoke-Expression"
```

### Linux/MacOS

If installed to the default location using the installation script `azd` can be removed using the uninstall script.

```bash
curl -fsSL https://aka.ms/uninstall-azd.sh | bash 
```

If installed to a custom location, remove `azd` by deleting the `azd` executable at the custom install location.


## Installer Troubleshooting

### Apt installer removes empty `/usr/local/bin` folder

The `/usr/local/bin` folder where the symlink `azd` is created may be removed on uninstall if the `azd` symlink is the only file in that folder. In this case, later installers or other programs that assume the presence of `/usr/local/bin` will fail. To mitigate, re-create the folder using `mkdir -p /usr/local/bin` (`sudo` may be required).
