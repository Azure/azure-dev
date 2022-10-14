function codespacesPortUrl($portNumber) {
    gh codespace ports `
        -c "$CODESPACE_NAME" `
        --json sourcePort,browseUrl `
        --jq "map(select(.sourcePort == $portNumber))[0].browseUrl"
}

if ($env:CODESPACES -eq 'true') {
    Write-host "Running in Codespaces. Setting port configurations."

    $apiPortUrl = codespacesPortUrl 3100
    Write-host "azd env set REACT_APP_API_BASE_URL `"$apiPortUrl`""
    azd env set REACT_APP_API_BASE_URL "$apiPortUrl"

    Write-host "Setting API port to public so web app can access it" 
    gh codespace ports visibility 3100:public -c "$($env:CODESPACE_NAME)"

 } else { 
    Write-host "Running in local development mode. Setting port configurations"
    
    Write-host "azd env set REACT_APP_API_BASE_URL `"http://localhost:3100`""
    azd env set REACT_APP_API_BASE_URL "http://localhost:3100"
 }
