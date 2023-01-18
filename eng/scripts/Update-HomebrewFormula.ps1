param(
    [string] $TemplatePath = "$PSSCriptRoot/../templates/brew.template"
    [string] $ZipFilePath,
    [string] $Version,
    [string] $OutFile
)

$sha256 =  (Get-FileHash -Path $ZipFilePath -Algorithm SHA256).Hash.ToLower()

$content = Get-Content $TemplatePath -Raw
$updatedContent = $content.Replace('%VERSION%', $Version).Replace('%SHA256', $sha256)

Set-Content -Path $OutFile -Value $updatedContent
