#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

say_error() {
    printf "setup-api: ERROR: %b\n" "$1" >&2
}

say() {
    printf "setup-api: %b\n" "$1"
}

function codespacesPortUrl() {
    local portNumber=$1
    gh codespace ports \
        -c "$CODESPACE_NAME" \
        --json sourcePort,browseUrl \
        --jq "map(select(.sourcePort == $portNumber))[0].browseUrl"
}

function getEnvFile() {
    local envFile
    envFile=$(azd env list --output json | jq -r "map(select(.IsDefault == true))[].DotEnvPath")

    if [ ! $? ]; then 
        say_error "Could not locate .env file: $envFile"
        say_error "Resources may not be deployed. Use 'azd provision' to deploy resources."
        exit 1
    fi 
    echo "$envFile"
}

envFile="$(getEnvFile)"
backupEnvFile="$envFile.backup"
debugEnvFile="$envFile.debug"

if [ ! -f "$envFile" ]; then 
    say_error "Could not locate .env file: $envFile"
    say_error "Try 'azd env refresh' if resources are already deployed or 'azd provision'"
    exit 1
fi

say "Copying .env file to backup" 
cp "$envFile" "$backupEnvFile"

if [ -f "$debugEnvFile" ]; then
    say "Existing debug envfile, using that instead"
    mv "$debugEnvFile" "$envFile"
fi

if [ "$CODESPACES" = 'true' ]; then
    say "Running in Codespaces. Setting port configurations."

    apiPortUrl=$(codespacesPortUrl 3100)
    say "azd env set REACT_APP_API_BASE_URL \"$apiPortUrl\""
    azd env set REACT_APP_API_BASE_URL "$apiPortUrl"

    say "Setting API port to public so web app can access it" 
    gh codespace ports visibility 3100:public -c "$CODESPACE_NAME"

else 
    say "Running in local development mode. Setting port configurations"
    
    say "azd env set REACT_APP_API_BASE_URL \"http://localhost:3100\""
    azd env set REACT_APP_API_BASE_URL "http://localhost:3100"
fi

say "Moving .env file to .env.debug"
mv -f "$envFile" "$debugEnvFile"

say "Restoring .env.backup"
mv "$backupEnvFile" "$envFile"
