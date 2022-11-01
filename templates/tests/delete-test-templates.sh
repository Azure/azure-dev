#!/bin/bash
set -euo pipefail

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
# Default to current logged in user if not specified
ENV_NAME_PREFIX=$(whoami)
TEMPLATE_NAME=""
ENV_SUFFIX=""

function usage {
    echo "Deletes azd template"
    echo ""
    echo "Usage: delete-test-templates -t <template> -b <branch> -e <env_prefix>" 2>&1
    echo ""
    echo "  -u    Sets the environment suffix (required)"
    echo "  -e    Sets the environment name prefix. Environment prefix is used in the azd environment name along with the template name (default: whoami)"
    echo "  -f    Sets the root folder on the local machine for the test projects to be generated (default: User's HOME folder)"
    echo "  -t    Sets the template name. Use values from 'azd template list'. When omitted will run for all templates available in 'azd template list'"
    echo ""
    echo "Examples:"
    echo "  Deleting all templates with default values (must have environment suffix)"
    echo "      bash ./delete-test-templates.sh -u \"<env_suffix>\""

    exit 1
}

while getopts "f:t:e:u:h" arg; do
    case ${arg} in
    f) FOLDER_PATH=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
    e) ENV_NAME_PREFIX=$OPTARG ;;
    u) ENV_SUFFIX=$OPTARG ;;
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

# Cleans the specified template
# $1 - The environment name
function cleanupTemplate {
    echo "Deprovisioning infrastructure for $1..."
    cd "$FOLDER_PATH/$1"
    azd down -e "$1" --force --purge

    echo "Cleaning up local project @ '$FOLDER_PATH/$1'..."
    rm -rf "$FOLDER_PATH/${1:?}"
}

if [[ -z $ENV_SUFFIX ]]; then
    echo "Must pass in environment suffix"
    echo "Examples:"
    echo "      bash ./delete-test-templates.sh -u \"<env_suffix>\""
    exit 0
else
    if [[ -z $TEMPLATE_NAME ]]; then
        declare -A ENV_TEMPLATE_MAP

        # If a template is not specified, run for all templates from output of 'azd template list'
        echo "Getting list of available templates..."
        TEMPLATES_JSON=$(azd template list --output json)

        while read -r TEMPLATE; do
            ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE:14}-$ENV_SUFFIX"
            ENV_TEMPLATE_MAP[$TEMPLATE]=$ENV_NAME
        done < <(echo "$TEMPLATES_JSON" | jq -r '.[].name' | sed 's/\\n/\n/g')

        # Cleanup the templates in parallel
        for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
            (cleanupTemplate "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue) &
        done

        wait

        echo ""
        echo "Done!"

    else
        # Delete test for the specified template name
        ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE_NAME:14}-$ENV_SUFFIX"
        cleanupTemplate "$ENV_NAME"

    fi
fi