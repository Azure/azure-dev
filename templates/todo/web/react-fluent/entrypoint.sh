#!/bin/sh
set -euo pipefail

ENV_FILE_PATH=".env"
CONFIG_ROOT="ENV_CONFIG"
OUTPUT_FILE="./public/env-config.js"

function generateOutput {
    echo "Generating JS configuration output to: $OUTPUT_FILE"
    echo -e "window.$CONFIG_ROOT = {" >$OUTPUT_FILE
    for line in $1; do
        if [[ $line = REACT_APP_* ]]; then
            key=${line%%=*}
            value=${line#*=}
            echo " - Found '$key'"
            echo -e "  $key: '$value'," >>$OUTPUT_FILE
        fi
    done
    echo -e "}" >>$OUTPUT_FILE
}

function usage() {
    echo
    echo "Arguments:"
    echo -e "\t-e\t Sets the .env file to use (default: .env)"
    echo -e "\t-o\t Sets the output filename (default: ./public/env-config.js)"
    echo -e "\t-c\t Sets the JS configuration key (default: ENV_CONFIG)"
    echo
    echo "Example:"
    echo -e "\tbash entrypoint.sh -e .env -o env-config.js"
}

while getopts "e:o:c:" opt; do
    case $opt in
    e) ENV_FILE_PATH=$OPTARG ;;
    o) OUTPUT_FILE=$OPTARG ;;
    c) CONFIG_ROOT=$OPTARG ;;
    :)
        echo "Error: -${OPTARG} requires a value"
        exit 1
        ;;
    *)
        usage
        exit 1
        ;;
    esac
done

# Load .env file if supplied
ENV_FILE=""
if [ -f $ENV_FILE_PATH ]; then
    echo "Loading environment file from '$ENV_FILE_PATH'"
    ENV_FILE="$(cat $ENV_FILE_PATH)"
fi

# Load system environment variables
ENV_VARS=$(printenv)

# Merge .env file with env variables
ALL_VARS="$ENV_FILE\n$ENV_VARS"
generateOutput "$ALL_VARS"
