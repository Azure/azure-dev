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
    echo "  -p    Sets azure dev ops pat"
    echo "  -o    Sets azure dev organization name"
    echo ""

    exit 1
}

while getopts "e:p:o:h" arg; do
    case ${arg} in
    e) ENV_NAME=$OPTARG ;;
    p) AZURE_DEVOPS_EXT_PAT=$OPTARG ;;
    o) AZURE_DEVOPS_ORG_NAME=$OPTARG ;;
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

function trackingAzdoPipeline {
    echo "Cd into the project folder ..."
    cd "$FOLDER_PATH"
    cd "$1"

    # Monitor the process of child pipeline
    BUILD_ID=$(az pipelines build list --project $1 --query [0].id)
    echo "Build id is: "$BUILD_ID
    STATUS=$(az pipelines build show --id $BUILD_ID --query status | sed 's/\"//g')

    while [ "$STATUS" != "completed" ]
    do
        echo "Pipeline is "$STATUS", please waiting..."
        sleep 30
        STATUS=$(az pipelines build show --id $BUILD_ID --query status | sed 's/\"//g')
    done

    PIPELINE_RESULT=$(az pipelines build show --id $BUILD_ID --query result | sed 's/\"//g')
    if [ "$PIPELINE_RESULT" == "failed" ];then
        echo "Pipelines failed..."
        exit 1
    fi
    echo "***********  Pipelines succeed!  ***********"


    # Verify 'Azure Dev Provision' and 'Azure Dev Deploy' whether exist.
    # Some variables to match log files
    FIND_PROVISION="Starting: Azure Dev Provision"
    FIND_DEPLOY="Starting: Azure Dev Deploy"
    PROVISION_EXIST=false
    DEPLOY_EXIST=false

    # Get the Url of all logs.
    logsUrl=$(az pipelines build show --id $BUILD_ID --query logs.url | sed 's/\"//g')
    # Get the log details
    logsDetail=$(curl -u ${AZURE_DEVOPS_ORG_NAME}:${AZURE_DEVOPS_EXT_PAT} -X GET $logsUrl)
    totalCounts=$(echo $logsDetail | jq .count)

    # Use loop processing the boolean value of 'PROVISION_EXIST' and 'DEPLOY_EXIST'.
    for ((i=1; i<=$totalCounts; i++))
    do
    FIND_FILE="log$i.txt"
    curl -u ${AZURE_DEVOPS_ORG_NAME}:${AZURE_DEVOPS_EXT_PAT} -o $FIND_FILE ${logsUrl}/$i
    if [ `grep -c "$FIND_PROVISION" $FIND_FILE` -ne '0' ]; then
        PROVISION_EXIST=true
    fi
    if [ `grep -c "$FIND_DEPLOY" $FIND_FILE` -ne '0' ]; then
        DEPLOY_EXIST=true
    fi
    done

    echo "======================================================"

    # Print the result of verification.
    if [ "$PROVISION_EXIST" = true ] && [ "$DEPLOY_EXIST" = true ]
    then
        echo "Azure Dev Provision and Azure Dev Deploy exist and execute successfully"
    else
        echo "provision or deploy not exist"
        exit 1
    fi
}

trackingAzdoPipeline "$ENV_NAME"