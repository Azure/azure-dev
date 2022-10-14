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

    apiPortUrl=$(codespacesPortUrl 3100)
    echo "azd env set REACT_APP_API_BASE_URL \"$apiPortUrl\""
    azd env set REACT_APP_API_BASE_URL "$apiPortUrl"

    echo "Setting API port to public so web app can access it" 
    gh codespace ports visibility 3100:public -c "$CODESPACE_NAME"

else 
    echo "Running in local development mode. Setting port configurations"
    
    echo "azd env set REACT_APP_API_BASE_URL \"http://localhost:3100\""
    azd env set REACT_APP_API_BASE_URL "http://localhost:3100"
fi
