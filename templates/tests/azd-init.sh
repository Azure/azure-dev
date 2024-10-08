#!/bin/bash

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
SUBSCRIPTION=""
TEMPLATE_NAME=""
ENV_NAME=""

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -s <subscription_id> -u <env_prefix>" 2>&1
    echo ""
    echo "  -s    Sets the subscription"
    echo "  -t    Sets template name"
    echo ""

    exit 1
}

while getopts "s:t:e:h" arg; do
    case ${arg} in
    s) SUBSCRIPTION=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
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

function initTemplate {
    echo "Creating new project folder ..."
    cd "$FOLDER_PATH"
    mkdir "$1"
    cd "$1"

    echo "Initializing template with branch staging"
    azd init -t "$3" -b "staging" -e "$1" --subscription "$2" --location "eastus2" --no-prompt

    echo "Initializing template successfully"
}

initTemplate "$ENV_NAME" "$SUBSCRIPTION" "$TEMPLATE_NAME"