#!/bin/bash

# Default to user HOME directory if not specified
ENV_NAME=""
FOLDER_PATH=$HOME
GITHUB_PAT=""
TEMPLATE_NAME=""

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -s <subscription_id> -u <env_prefix>" 2>&1
    echo ""
    echo "  -e    Sets the env name"
    echo "  -t    Sets the template name"
    echo "  -p    Sets the github-pat"
    echo ""

    exit 1
}

while getopts "e:t:p:h" arg; do
    case ${arg} in
    e) ENV_NAME=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
    p) GITHUB_PAT=$OPTARG ;;
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

function configGithub {
    echo "cd into the project folder ..."
    cd "$FOLDER_PATH"
    cd "$1"

    # If there is a .git folder here, delete it.
    if [ -d ".git" ]; then
        rm -rf .git
    fi

    # github auth login
    echo "$3" > mytoken.txt
    gh auth login --with-token < mytoken.txt
    rm -rf mytoken.txt

    # Azd pipeline config steps:
    echo "Azd pipeline config for $2..."
    azd pipeline config --no-prompt
    if [[ $? -ne 0 ]];then
        echo "Some error message..."
        exit 1
    fi

    echo "Pipeline is being prepared"
    sleep 10
}

configGithub "$ENV_NAME" "$TEMPLATE_NAME" "${GITHUB_PAT}"