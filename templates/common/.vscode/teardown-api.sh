#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

say_error() {
    printf "teardown-api: ERROR: %b\n" "$1" >&2
}

say() {
    printf "teardown-api: %b\n" "$1"
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

if [ ! -f "$debugEnvFile" ]; then
    say_error "Could not locate debug .env file: $debugEnvFile"
    exit 1
fi

say "Removing debug .env file: $debugEnvFile"
rm "$debugEnvFile" 
