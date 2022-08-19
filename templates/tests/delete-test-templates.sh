#!/bin/bash
set -euo pipefail

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
# Default to 'main' if not specified
BRANCH_NAME="main"
# Default to current logged in user if not specified
ENV_NAME_PREFIX=$(whoami)
TEMPLATE_NAME=""
LOCATION="eastus2"
# Default to a random value if not specified
ENV_SUFFIX=""

function usage {
    echo "Deletes azd template"
    echo ""
    echo "Usage: delete-test-templates -t <template> -b <branch> -e <env_prefix>" 2>&1
    echo ""
    echo "  -s    Sets the environment suffix (default: RANDOM)"
    echo "  -e    Sets the environment name prefix. Environment prefix is used in the azd environment name along with the template name (default: whoami)"
    echo "  -f    Sets the root folder on the local machine for the test projects to be generated (default: User's HOME folder)"
    echo "  -b    Sets the template branch name. Override to test a any custom branches (default: main)"
    echo "  -t    Sets the template name. Use values from 'azd template list'. When omitted will run for all templates available in 'azd template list'"
    echo "  -l    Sets the Azure location for the template infrastructure (default: eastus2)"
    echo ""
    echo "Examples:"
    echo "  Deleting all templates with default values (must have environment suffix)"
    echo "      bash ./delete-test-templates.sh -s \"<env_suffix>\""
    echo ""
    echo "  Deleting a single template with custom branch"
    echo "      bash ./delete-test-templates.sh -t \"Azure-Samples/todo-nodejs-mongo\" -b \"<custom_branch_name>\" -s \"<env_suffix>\""

    exit 1
}

while getopts "f:t:b:e:l:s:h" arg; do
    case ${arg} in
    f) FOLDER_PATH=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
    b) BRANCH_NAME=$OPTARG ;;
    e) ENV_NAME_PREFIX=$OPTARG ;;
    l) LOCATION=$OPTARG ;;
    s) ENV_SUFFIX=$OPTARG ;;
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
# $1 - The template name
# $2 - The branch name
# $3 - The environment name
function cleanupTemplate {
    echo "Deprovisioning infrastructure for $3..."
    cd "$FOLDER_PATH/$3"
    azd down -e "$3" --force --purge

    echo "Cleaning up local project @ '$FOLDER_PATH/$3'..."
    rm -rf "$FOLDER_PATH/${3:?}"
}

export AZURE_LOCATION="$LOCATION"

if [[ -z $ENV_SUFFIX ]]; then
    echo "Must pass in environment suffix"
    echo "Examples:"
    echo "      bash ./delete-test-templates.sh -s \"<env_suffix>\""
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
            (cleanupTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue) &
        done

        wait

        echo ""
        echo "Done!"

    else
        # Delete test for the specified template name
        ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE_NAME:14}-$ENV_SUFFIX"
        cleanupTemplate "$TEMPLATE_NAME" "$BRANCH_NAME" "$ENV_NAME"

    fi
fi