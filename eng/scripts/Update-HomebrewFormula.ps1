param(
    [string] $TemplatePath = "$PSSCriptRoot/../templates/brew.template",
    [string] $ZipFilePathAmd64,
    [string] $ZipFilePathArm64,
    [string] $Version,
    [string] $OutFile
)

$sha256amd64 =  (Get-FileHash -Path $ZipFilePathAmd64 -Algorithm SHA256).Hash.ToLower()
$sha256arm64 =  (Get-FileHash -Path $ZipFilePathArm64 -Algorithm SHA256).Hash.ToLower()

$content = Get-Content $TemplatePath -Raw
$updatedContent = $content.Replace('%VERSION%', $Version).Replace('%SHA256AMD64%', $sha256amd64).Replace('%SHA256ARM64%', $sha256arm64)

Set-Content -Path $OutFile -Value $updatedContent
