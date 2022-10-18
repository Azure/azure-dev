#!/usr/bin/env bash

function codespacesPortUrl() {
    local portNumber=$1
    gh codespace ports \
        -c "$CODESPACE_NAME" \
        --json sourcePort,browseUrl \
        --jq "map(select(.sourcePort == $portNumber))[0].browseUrl"
}

function getEnvFile() { 
    azd env list --output json | jq -r "map(select(.IsDefault == true))[].DotEnvPath"
}

envFile="$(getEnvFile)"
backupEnvFile="$envFile.backup"
debugEnvFile="$envFile.debug"

if [ -f "$envFile" ]; then 
    echo "Copying .env file to backup" 
    cp "$envFile" "$backupEnvFile"
fi

if [ -f "$debugEnvFile" ]; then
    echo "Existing debug envfile, using that"
    mv "$debugEnvFile" "$envFile"
fi

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

echo "Moving .env file to .env.debug"
mv -f "$envFile" "$debugEnvFile"

echo "Restoring .env.backup"
mv "$backupEnvFile" "$envFile"
