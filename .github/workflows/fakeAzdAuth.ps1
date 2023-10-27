echo 'Creating fake auth'
Write-Host "Start fake azd auth"
$targetDirectory = "$HOME/.azd"

# Check if the directory exists, and if not, create it
if (-not (Test-Path $targetDirectory)) {
    New-Item -ItemType Directory -Path $targetDirectory -Force
}

# Create a sample JSON object
$fakeAzdAuth = @{
    auth = @{
        account = @{
            currentUser = @{
                homeAccountId = "xxxxx"
            }
        }
    }
}

$jsonString = $fakeAzdAuth | ConvertTo-Json -Depth 4
$jsonFileName = "auth.json"
$jsonFilePath = Join-Path -Path $targetDirectory -ChildPath $jsonFileName

# Write the JSON content to the file
$jsonString | Set-Content -Path $jsonFilePath -Encoding UTF8

Write-Host "Created folder: $targetDirectory"
Write-Host "Created JSON file: $jsonFileName"