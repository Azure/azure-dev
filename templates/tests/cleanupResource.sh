#!/bin/bash

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
ENV_NAME=""

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -s <subscription_id> -u <env_prefix>" 2>&1
    echo ""
    echo "  -e    Sets the env name"
    echo ""

    exit 1
}

while getopts "e:h" arg; do
    case ${arg} in
    e) ENV_NAME=$OPTARG ;;
    h)
        usage
        ;;
    :)
        echo "$0: Must supply an argument to -$arg." >&2
        exit 1
        ;;
    ?)
        echo "Invalid option -$arg."
        exit 2
        ;;
    *) usage ;;
    esac
done

function cleanupResource {
    cd "$FOLDER_PATH/$1"
    
    echo "Deprovisioning infrastructure for $1..."
    azd down -e "$1" --force --purge
}

cleanupResource "$ENV_NAME"