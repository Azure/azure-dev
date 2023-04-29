param(
    $ExpectedFieldMap = 'telemetry.json',
    $Shell = 'pwsh',
    [switch] $NonInteractive
)

$exitCode = 0


# Sample output:

# An error was encountered during install: DownloadFailed
# Error data collected:

# Name                           Value
# ----                           -----
# reason                         DownloadFailed
# installVersion                 latest
# os                             Ubuntu
# osVersion                      20.04
# downloadUrl                    127.0.0.1/latest/azd-linux-amd64.tar.gz
# isWsl                          True
# terminal                       pwsh
# executionEnvironment           Desktop

# Sample result:
# findOutputEntry $sampleOutput 'os'
# Ubuntu
function findOutputEntry($haystack, $needle) {
    $output = $null
    foreach ($straw in $haystack) {
        if ($straw -like "$needle *") {
            $output = $straw.Split( `
                ' ', `
                [System.StringSplitOptions]::RemoveEmptyEntries`
            )[1]
            break
        }
    }

    return $output.Trim()
}

if (!(Test-Path $ExpectedFieldMap)) {
    Write-Error "Could not find expected telemetry output file"
    exit 1
}

# Run the script in a way that will result in an error. Telemetry must be
# enabled so leave off any parameters which disable telemetry.
if ($NonInteractive) {
    Write-Host "$Shell -NonInteractive -c `"$PSScriptRoot/install-azd.ps1 -BaseUrl '127.0.0.1/error' -Verbose`""
    $output = &$Shell -NonInteractive -c "$PSScriptRoot/install-azd.ps1 -BaseUrl '127.0.0.1/error' -Verbose"

    if ($IsLinux -or $IsMacOS) {
        Write-Host "Running on Linux or macOS, running install-azd-report.sh to get output"
        $output = & bash -c './install-azd-report.sh --verbose --dry-run' 
    }

} else {
    $originalErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    &"./install-azd.ps1" -BaseUrl '127.0.0.1/error' -Verbose *>install.log
    $ErrorActionPreference = $originalErrorActionPreference
    $output = Get-Content install.log

    if ($IsLinux -or $IsMacOS) {
        Write-Host "Running on Linux or macOS, running install-azd-report.sh to get output"
        $output = & bash -c './install-azd-report.sh --verbose --dry-run' 
    }

    Write-Host $output
}

Write-Host "Output type: $($output.GetType().Name)"
Write-Host "Output from error execution:"
Write-Host $output

$telemetryJson = Get-Content $ExpectedFieldMap -Raw
$telemetryPairs = ConvertFrom-Json $telemetryJson
$keys = (Get-Member -InputObject $telemetryPairs -MemberType NoteProperty).Name

foreach ($key in $keys) {
    # Listing NoteProperty items includes an empty key. Skip the empty key.
    if (!$key) {
        continue
    }

    $expectedName = $key
    $expectedValue = $telemetryPairs.$key
    $actualValue = findOutputEntry $output $expectedName

    if ($actualValue -ne $expectedValue) {
        Write-Error "Missed expected value for $expectedName (expected: $expectedValue, actual: $actualValue)"
        $exitCode = 1
    } else {
        Write-Host "Found $expectedName with expected value: $expectedValue"
    }
}

if ($exitCode) {
    Write-Error "Tests failed"
} else {
    Write-Host "Tests passed"
}

exit $exitCode
