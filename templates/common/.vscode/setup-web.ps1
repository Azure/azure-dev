function codespacesPortUrl($portNumber) {
    gh codespace ports `
        -c "$CODESPACE_NAME" `
        --json sourcePort,browseUrl `
        --jq "map(select(.sourcePort == $portNumber))[0].browseUrl"
}

if ($env:CODESPACES -eq 'true') {
    Write-Host "Running in Codespaces. Setting port configurations."

    $webPortUrl = codespacesPortUrl 3000
    Write-Host "azd env set REACT_APP_WEB_BASE_URL `"$webPortUrl`""
    azd env set REACT_APP_WEB_BASE_URL "$webPortUrl"
} else { 
    Write-Host "Running in local development mode. Setting port configurations"
    
    Write-Host "azd env set REACT_APP_WEB_BASE_URL `"http://localhost:3000`""
    azd env set REACT_APP_WEB_BASE_URL "http://localhost:3000"
}
