#!/usr/bin/env bash

function codespacesPortUrl() {
    local portNumber=$1
    gh codespace ports \
        -c "$CODESPACE_NAME" \
        --json sourcePort,browseUrl \
        --jq "map(select(.sourcePort == $portNumber))[0].browseUrl"
}

if [ "$CODESPACES" = 'true' ]; then
    echo "Running in Codespaces. Setting port configurations."

    webPortUrl=$(codespacesPortUrl 3000)
    echo "azd env set REACT_APP_WEB_BASE_URL \"$webPortUrl\""
    azd env set REACT_APP_WEB_BASE_URL "$webPortUrl"

else 
    echo "Running in local development mode. Setting port configurations"
    
    echo "azd env set REACT_APP_WEB_BASE_URL \"http://localhost:3000\""
    azd env set REACT_APP_WEB_BASE_URL "http://localhost:3000"

fi
