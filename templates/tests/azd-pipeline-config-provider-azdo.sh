#!/bin/bash

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
TEMPLATE_NAME=""
ENV_NAME=""

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -s <subscription_id> -u <env_prefix>" 2>&1
    echo ""
    echo "  -o    Sets the organization name"
    echo "  -p    Sets the personal access token"
    echo "  -t    Sets the template name"
    echo "  -e    Sets the env name"
    echo ""

    exit 1
}

while getopts "p:o:t:e:h" arg; do
    case ${arg} in
    p) AZURE_DEVOPS_EXT_PAT=$OPTARG ;;
    o) AZURE_DEVOPS_ORG_NAME=$OPTARG ;;
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

function configAzdo {
    echo "Cd into the project folder ..."
    cd "$FOLDER_PATH"
    cd "$1"

    # If there is a .git folder here, delete it.
    if [ -d ".git" ]; then
        rm -rf .git
    fi

    # Set git credential
    echo "https://${AZURE_DEVOPS_ORG_NAME}:${AZURE_DEVOPS_EXT_PAT}@dev.azure.com" > ~/.git-credentials
    git config --global credential.helper store

    echo "Set git credential successfully"

    # Azd pipeline config steps:
    echo "Azd pipeline config --provider azdo for $2..."
    azd pipeline config --provider azdo --no-prompt
    if [[ $? -ne 0 ]];then
        echo "Some error message..."
        exit 1
    fi

    echo "Pipeline is being prepared"
    sleep 20
}

configAzdo "$ENV_NAME" "$TEMPLATE_NAME"