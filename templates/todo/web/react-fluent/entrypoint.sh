#!/bin/sh
set -eu

ENV_FILE_PATH=".env"
CONFIG_ROOT="ENV_CONFIG"
OUTPUT_FILE="./public/env-config.js"

generateOutput() {
    echo "Generating JS configuration output to: $OUTPUT_FILE"
    echo "window.$CONFIG_ROOT = {" >"$OUTPUT_FILE"
    for line in $1; do
        if beginswith REACT_APP_ "$line"; then
            key=${line%%=*}
            value=${line#*=}
            printf " - Found '%s'" "$key"
            printf "\t%s: '%s',\n" "$key" "$value" >>"$OUTPUT_FILE"
        fi
    done
    echo "}" >>"$OUTPUT_FILE"
}

beginswith() { case $2 in "$1"*) true;; *) false;; esac; }

usage() {
    printf
    printf "Arguments:"
    printf "\t-e\t Sets the .env file to use (default: .env)"
    printf "\t-o\t Sets the output filename (default: ./public/env-config.js)"
    printf "\t-c\t Sets the JS configuration key (default: ENV_CONFIG)"
    printf
    printf "Example:"
    printf "\tbash entrypoint.sh -e .env -o env-config.js"
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
if [ -f "$ENV_FILE_PATH" ]; then
    echo "Loading environment file from '$ENV_FILE_PATH'"
    ENV_FILE="$(cat "$ENV_FILE_PATH")"
fi

# Load system environment variables
ENV_VARS=$(printenv)

# Merge .env file with env variables
ALL_VARS="$ENV_FILE\n$ENV_VARS"
generateOutput "$ALL_VARS"
