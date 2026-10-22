param(
    [Parameter(Mandatory)]
    [string] $BinaryPath,
    [Parameter(Mandatory)]
    [string] $Version
)

$ErrorActionPreference = 'Stop'
$BinaryPath = (Resolve-Path -LiteralPath $BinaryPath).Path
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
if ($IsWindows -and $architecture -eq 'x64') {
    $asset = 'azd-windows-amd64.zip'
    $checksum = '98b772492c0349f911ad38c2bab7c165f23783d702723f848047948afa74d176'
} elseif ($IsMacOS -and $architecture -eq 'x64') {
    $asset = 'azd-darwin-amd64.zip'
    $checksum = '7d709ad8818ff816f7cc643b48c1857856e46170875ac7d4532d5d201dbe5efb'
} elseif ($IsMacOS -and $architecture -eq 'arm64') {
    $asset = 'azd-darwin-arm64.zip'
    $checksum = 'f5e6904e01379b5e30abdab24f644523c26deee5afdf10ecf31f770176683063'
} else {
    throw "Installed-host CI smoke requires Windows x64 or macOS x64/arm64."
}

$scratch = Join-Path ([System.IO.Path]::GetTempPath()) "cu-host-smoke-$([guid]::NewGuid())"
$saved = @{}
foreach ($name in @('CU_SMOKE_HOST', 'CU_SMOKE_EXTENSION', 'CU_SMOKE_VERSION')) {
    $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
try {
    New-Item -ItemType Directory -Path $scratch | Out-Null
    $archive = Join-Path $scratch $asset
    Invoke-WebRequest `
        -Uri "https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_1.37.0/$asset" `
        -OutFile $archive
    if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $checksum) {
        throw "azd 1.37.0 archive checksum mismatch."
    }
    $hostDirectory = Join-Path $scratch 'host'
    Expand-Archive -LiteralPath $archive -DestinationPath $hostDirectory
    $executables = @(Get-ChildItem -LiteralPath $hostDirectory -Recurse -File | Where-Object {
        $_.Name -in @('azd.exe', 'azd-windows-amd64.exe', 'azd-darwin-amd64', 'azd-darwin-arm64')
    })
    if ($executables.Count -ne 1) {
        throw "Expected exactly one azd executable in the release archive."
    }
    $env:CU_SMOKE_HOST = $executables[0].FullName
    if (-not $IsWindows) {
        & chmod +x $env:CU_SMOKE_HOST
        if ($LASTEXITCODE -ne 0) { throw "Unable to set azd executable permission." }
    }
    $env:CU_SMOKE_EXTENSION = $BinaryPath
    $env:CU_SMOKE_VERSION = $Version
    Push-Location $PSScriptRoot
    try {
        go test ./internal/cmd -run '^TestInstalledHostOffline$' -v -count=1 -timeout=5m
        if ($LASTEXITCODE -ne 0) { throw "Installed-host smoke failed." }
    } finally {
        Pop-Location
    }
} finally {
    foreach ($name in $saved.Keys) {
        [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process')
    }
    if (Test-Path -LiteralPath $scratch) {
        Remove-Item -LiteralPath $scratch -Recurse -Force
    }
}
