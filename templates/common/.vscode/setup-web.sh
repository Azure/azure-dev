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
    echo "Copying .env file to backup $envFile -> $backupEnvFile" 
    cp "$envFile" "$backupEnvFile"
fi

if [ -f "$debugEnvFile" ]; then
    echo "Existing debug envfile, using that $debugEnvFile -> $envFile"
    mv "$debugEnvFile" "$envFile"
fi

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

echo "Moving .env file to .env.debug"
mv -f "$envFile" "$debugEnvFile"

echo "Restoring .env.backup"
mv "$backupEnvFile" "$envFile"
