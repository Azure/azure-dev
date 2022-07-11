#!/bin/bash
set -euo pipefail

FOLDER_PATH=""
TEMPLATE_NAME=""
BRANCH_NAME=""
ENV_NAME_PREFIX=""
PLAYWRIGHT_RETRIES="3"
LOCATION="eastus2"

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -e <env_prefix>" 2>&1
    echo ""
    echo "  -f    Sets the root folder on the local machine for the test projects to be generated (default: User's HOME folder)"
    echo "  -b    Sets the template branch name. Override to test a any custom branches (default: main)"
    echo "  -e    Sets the environment name prefix. Environment prefix is used in the azd environment name along with the template name (default: whoami)"
    echo "  -t    Sets the template name. Use values from 'azd template list'. When omitted will run for all templates available in 'azd template list'"
    echo "  -r    Sets the number of retries for playwright tests (default: 3)"
    echo "  -l    Sets the Azure location for the template infrastructure (default: eastus2)"
    echo ""
    echo "Examples:"
    echo "  Testing a single template with custom branch"
    echo "      bash ./test-templates.sh -t \"Azure-Samples/todo-nodejs-mongo\" -b \"<custom_branch_name>\""
    echo ""
    echo "  Testing all templates with default values"
    echo "      bash ./test-templates.sh"

    exit 1
}

while getopts "f:t:b:e:r:l:h" arg; do
    case ${arg} in
    f) FOLDER_PATH=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
    b) BRANCH_NAME=$OPTARG ;;
    e) ENV_NAME_PREFIX=$OPTARG ;;
    r) PLAYWRIGHT_RETRIES=$OPTARG ;;
    l) LOCATION=$OPTARG ;;
    h)
        usage
        ;;
    :)
        echo "$0: Must supply an argument to -$OPTARG." >&2
        exit 1
        ;;
    ?)
        echo "Invalid option -$OPTARG."
        exit 2
        ;;
    *) usage ;;
    esac
done

# Default to user HOME directory if not specified
if [[ -z $FOLDER_PATH ]]; then
    FOLDER_PATH=$HOME
fi

# Default to 'main' if not specified
if [[ -z $BRANCH_NAME ]]; then
    BRANCH_NAME='main'
fi

# Default to current logged in user if not specified
if [[ -z $ENV_NAME_PREFIX ]]; then
    ENV_NAME_PREFIX=$(whoami)
fi

# Deploys the specified template
# $1 - The template name
# $2 - The branch name
# $3 - The environment name
function deployTemplate {
    echo "Creating new project folder @ '$FOLDER_PATH/$3'..."
    cd "$FOLDER_PATH"
    mkdir "$3"
    cd "$3"

    echo "Initializing template '$1' with branch '$2'"
    azd init -t "$1" -b "$2" -e "$3" --no-prompt

    echo "Provisioning infrastructure for $3..."
    azd provision -e "$3"

    echo "Deploying apps for $3..."
    azd deploy -e "$3"
}

# Tests the specified template
# $1 - The template name
# $2 - The branch name
# $3 - The environment name
function testTemplate {
    echo "Running template smoke tests for $3..."
    cd "$FOLDER_PATH/$3/tests"
    npm i && npx playwright install
    npx playwright test --retries=$PLAYWRIGHT_RETRIES
}

# Cleans the specified template
# $1 - The template name
# $2 - The branch name
# $3 - The environment name
function cleanupTemplate {
    echo "Deprovisioning infrastructure for $3..."
    cd "$FOLDER_PATH/$3"
    azd down -e "$3" --force --purge

    echo "Cleaning up local project @ '$FOLDER_PATH/$3'..."
    rm -rf "$FOLDER_PATH/$3"
}

export AZURE_LOCATION="$LOCATION"

if [[ -z $TEMPLATE_NAME ]]; then
    declare -A ENV_TEMPLATE_MAP

    # If a template is not specified, run for all templates from output of 'azd template list'
    echo "Getting list of availabe templates..."
    TEMPLATES_JSON=$(azd template list --output json)

    while read -r TEMPLATE; do
        ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE:14}-$RANDOM"
        ENV_TEMPLATE_MAP[$TEMPLATE]=$ENV_NAME
    done < <( echo "$TEMPLATES_JSON" | jq -r '.[].name' | sed 's/\\n/\n/g' )

    # Deploy the templates in parallel
    for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
        (deployTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue) &
    done

    wait

    # Test the templates serially
    for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
        testTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue
    done

    # Cleanup the templates in parallel
    for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
        (cleanupTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue) &
    done

    wait

    echo ""
    echo "Done!"
else
    # Run test for the specified template name
    ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE_NAME:14}-$RANDOM"
    testTemplate "$TEMPLATE_NAME" "$BRANCH_NAME" "$ENV_NAME"
fi
