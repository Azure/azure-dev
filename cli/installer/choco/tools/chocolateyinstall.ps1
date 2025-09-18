$ErrorActionPreference = 'Stop'
$toolsDir   = "$(Split-Path -parent $MyInvocation.MyCommand.Definition)"
$file64      = Join-Path $toolsDir 'azd-windows-amd64.msi'
$packageArgs = @{
  packageName   = $env:ChocolateyPackageName
  unzipLocation = $toolsDir
  fileType      = 'MSI'
  file64        = $file64
  softwareName  = 'Azure Developer CLI'
  silentArgs    = "/qn /norestart /l*v `"$($env:TEMP)\$($packageName).$($env:chocolateyPackageVersion).MsiInstall.log`" ALLUSERS=1 INSTALLEDBY=choco"
  validExitCodes= @(0, 3010, 1641)
}

Install-ChocolateyPackage @packageArgs
