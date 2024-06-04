param(
    [string] $PackageIdentifier,
    [string] $Version,
    [string] $Url,
    [string] $GitHubToken,
    [string] $OutLocation = "winget",
    [switch] $Submit
)
$PSNativeCommandArgumentPassing = 'Legacy'

Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile wingetcreate.exe

$AdditionalArgs = ""
if ($Submit) {
    $AdditionalArgs = "--submit"
}

./wingetcreate.exe update `
    $PackageIdentifier `
    --version $Version `
    --urls $Url `
    --token $GitHubToken `
    --out winget `
    $AdditionalArgs
