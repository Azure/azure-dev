#!/bin/bash
set -euo pipefail

# Default to user HOME directory if not specified
FOLDER_PATH=$HOME
# Default to 'main' if not specified
BRANCH_NAME="main"
# Default to current logged in user if not specified
ENV_NAME_PREFIX=$(whoami)
TEMPLATE_NAME=""
PLAYWRIGHT_RETRIES="1"
PLAYWRIGHT_REPORTER="list"
LOCATION="eastus2"
SUBSCRIPTION="2cd617ea-1866-46b1-90e3-fffb087ebf9b"
# Default to a random value if not specified
ENV_SUFFIX="$RANDOM"
# When set will only run tests without deployments
TEST_ONLY=false
# When set will clean up local and remote resources
CLEANUP=true

function usage {
    echo "Tests azd template init, provision & deploy"
    echo ""
    echo "Usage: test-templates -t <template> -b <branch> -s <subscription_id> -u <env_prefix>" 2>&1
    echo ""
    echo "  -f    Sets the root folder on the local machine for the test projects to be generated (default: User's HOME folder)"
    echo "  -b    Sets the template branch name. Override to test a any custom branches (default: main)"
    echo "  -e    Sets the environment name prefix. Environment prefix is used in the azd environment name along with the template name (default: whoami)"
    echo "  -t    Sets the template name. Use values from 'azd template list'. When omitted will run for all templates available in 'azd template list'"
    echo "  -r    Sets the number of retries for playwright tests (default: 1)"
    echo "  -p    Sets the reporter for playwright tests (default: list)"
    echo "  -l    Sets the Azure location for the template tests to run in (default: eastus2)"
    echo "  -s    Sets the Azure subscription name or ID for the template tests to run in. (default: 2cd617ea-1866-46b1-90e3-fffb087ebf9b)"
    echo "  -u    Sets the environment suffix (default: RANDOM)"
    echo "  -n    When set will only run test commands. If true script won't deploy the templates. This is helpful when you already have the environments provisioned and you want to re-run the tests (default: false)"
    echo "  -c    when set will clean up resources (default: true)"
    echo ""
    echo "Examples:"
    echo "  Testing a single template with custom branch"
    echo "      bash ./test-templates.sh -t \"Azure-Samples/todo-nodejs-mongo\" -b \"<custom_branch_name>\""
    echo ""
    echo "  Testing all templates with default values"
    echo "      bash ./test-templates.sh"

    exit 1
}

while getopts "f:t:b:e:r:p:l:s:u:n:c:h" arg; do
    case ${arg} in
    f) FOLDER_PATH=$OPTARG ;;
    t) TEMPLATE_NAME=$OPTARG ;;
    b) BRANCH_NAME=$OPTARG ;;
    e) ENV_NAME_PREFIX=$OPTARG ;;
    r) PLAYWRIGHT_RETRIES=$OPTARG ;;
    p) PLAYWRIGHT_REPORTER=$OPTARG ;;
    l) LOCATION=$OPTARG ;;
    s) SUBSCRIPTION=$OPTARG ;;
    u) ENV_SUFFIX=$OPTARG ;;
    n) TEST_ONLY=true ;;
    c) CLEANUP=$OPTARG ;;
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

# Deploys the specified template
# $1 - The template name
# $2 - The branch name
# $3 - The environment name
# $4 - The Azure subscription name or ID
# $5 - The Azure location
function deployTemplate {
    echo "Creating new project folder @ '$FOLDER_PATH/$3'..."
    cd "$FOLDER_PATH"
    mkdir "$3"
    cd "$3"

    echo "Initializing template '$1' with branch '$2'"
    azd init -t "$1" -b "$2" -e "$3" --subscription "$4" --location "$5" --no-prompt

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
    npx -y playwright test --retries="$PLAYWRIGHT_RETRIES" --reporter="$PLAYWRIGHT_REPORTER"
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
    rm -rf "$FOLDER_PATH/${3:?}"
}

export AZURE_LOCATION="$LOCATION"

if [[ -z $TEMPLATE_NAME ]]; then
    declare -A ENV_TEMPLATE_MAP

    # If a template is not specified, run for all templates from output of 'azd template list'
    echo "Getting list of available templates..."
    TEMPLATES_JSON=$(azd template list --output json)

    while read -r TEMPLATE; do
        ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE:14}-$ENV_SUFFIX"
        ENV_TEMPLATE_MAP[$TEMPLATE]=$ENV_NAME
    done < <(echo "$TEMPLATES_JSON" | jq -r '.[].name' | sed 's/\\n/\n/g')

    if [ $TEST_ONLY == false ]; then
        # Deploy the templates in parallel
        for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
            (deployTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" "${SUBSCRIPTION}" "${LOCATION}" || continue) &
        done

        wait
    fi

    # Test the templates serially
    for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
        testTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue
    done

    if [ "$CLEANUP" == true ]; then
        # Cleanup the templates in parallel
        for TEMPLATE in "${!ENV_TEMPLATE_MAP[@]}"; do
            (cleanupTemplate "$TEMPLATE" "$BRANCH_NAME" "${ENV_TEMPLATE_MAP[$TEMPLATE]}" || continue) &
        done

        wait
    fi

    echo ""
    echo "Done!"
else
    # Run test for the specified template name
    ENV_NAME="${ENV_NAME_PREFIX}-${TEMPLATE_NAME:14}-$ENV_SUFFIX"
    if [ $TEST_ONLY == false ]; then
        deployTemplate "$TEMPLATE_NAME" "$BRANCH_NAME" "$ENV_NAME" "${SUBSCRIPTION}" "${LOCATION}" 
    fi

    testTemplate "$TEMPLATE_NAME" "$BRANCH_NAME" "$ENV_NAME"

    if [ "$CLEANUP" == true ]; then
        cleanupTemplate "$TEMPLATE_NAME" "$BRANCH_NAME" "$ENV_NAME"
    fi
fi
