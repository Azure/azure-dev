#!/bin/bash

# Default to user HOME directory if not specified
ENV_NAME=""
FOLDER_PATH=$HOME

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

function trackingGithubPipeline {
    echo "cd into the project folder ..."
    cd "$FOLDER_PATH"
    cd "$1"

    # Print the Action id and monitor the result of pipeline
    ActionId=$(gh run list --jq .[].databaseId --json databaseId --limit 1)
    echo "The pipeline Action id is: "$ActionId
    gh run watch $ActionId --interval 20 --exit-status
    echo "Pipeline completed."

    # Judge Azure Dev Provision and Azure Dev Deploy whether exist
    runResult=$(gh run view $ActionId -v)
    provision="Azure Dev Provision"
    deploy="Azure Dev Deploy"

    if [[ "$runResult" == *"$deploy"* && "$runResult" == *"$provision"* ]]; then 
        echo "Azure Dev Provision and Azure Dev Deploy exist and execute successfully"
    else
        echo "provision or deploy not exist"
        exit 1
    fi
}

trackingGithubPipeline "$ENV_NAME"