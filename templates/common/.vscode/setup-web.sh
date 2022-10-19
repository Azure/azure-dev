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
debugEnvFile="$envFile.debug"

if [ -f "$debugEnvFile" ]; then
    say "Existing debug .env file, no action necessary"
    exit 0
fi

if [ ! -f "$envFile" ]; then 
    say_error "Could not locate .env file: $envFile"
    say_error "Try 'azd env refresh' if resources are already deployed or 'azd provision'"
    exit 1
fi

say "Could not locate debug .env file ($debugEnvFile). Using default env file"
cp "$envFile" "$debugEnvFile" 
