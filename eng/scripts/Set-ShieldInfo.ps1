param(
    $TemplatePath,
    $OutputPath = $TemplatePath,
    $Version = ""
)

if (!(Test-Path $TemplatePath)) {
    Write-Error "Cannot find template at $TemplatePath"
    exit 1
}

$templateJson = Get-Content $TemplatePath -Raw
$template = ConvertFrom-Json $templateJson

$template.message = $Version

$outputJson = ConvertTo-Json $template -Depth 100
Set-Content -Path $OutputPath -Value $outputJson
