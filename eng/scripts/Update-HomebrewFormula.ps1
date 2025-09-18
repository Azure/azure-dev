param(
    [string] $TemplatePath = "$PSSCriptRoot/../templates/brew.template",
    [string] $ZipFilePathAmd64,
    [string] $ZipFilePathArm64,
    [string] $LinuxArchivePathAmd64,
    [string] $LinuxArchivePathArm64,
    [string] $Version,
    [string] $OutFile
)

$sha256amd64 =  (Get-FileHash -Path $ZipFilePathAmd64 -Algorithm SHA256).Hash.ToLower()
$sha256arm64 =  (Get-FileHash -Path $ZipFilePathArm64 -Algorithm SHA256).Hash.ToLower()
$sha256amd64_linux =  (Get-FileHash -Path $LinuxArchivePathAmd64 -Algorithm SHA256).Hash.ToLower()
$sha256arm64_linux =  (Get-FileHash -Path $LinuxArchivePathArm64 -Algorithm SHA256).Hash.ToLower()

$content = Get-Content $TemplatePath -Raw
$updatedContent = $content.Replace('%VERSION%', $Version).Replace('%SHA256AMD64%', $sha256amd64).Replace('%SHA256ARM64%', $sha256arm64).Replace('%SHA256AMD64_LINUX%', $sha256amd64_linux).Replace('%SHA256ARM64_LINUX%', $sha256arm64_linux)

Set-Content -Path $OutFile -Value $updatedContent
