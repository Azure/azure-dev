$ErrorActionPreference = 'Stop'
$toolsDir   = "$(Split-Path -parent $MyInvocation.MyCommand.Definition)"
$url64      = Join-Path $toolsDir 'azd-windows-amd64.msi'
$sha256     = 'DBD645DD848D7A0B488AD58A5C6D657F96EACCE18FC400337065526A750CF81E'
$packageArgs = @{
  packageName   = $env:ChocolateyPackageName
  unzipLocation = $toolsDir
  fileType      = 'MSI'
  file64        = $url64
  softwareName  = 'Azure Developer CLI'
  checksum64    = $sha256
  checksumType64= 'sha256'
  silentArgs    = "/qn /norestart /l*v `"$($env:TEMP)\$($packageName).$($env:chocolateyPackageVersion).MsiInstall.log`" ALLUSERS=1"
  validExitCodes= @(0, 3010, 1641)
}

Install-ChocolateyPackage @packageArgs


