#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

say_error() {
    printf "install-azd-report: ERROR: %b\n" "$1" >&2
}

say() {
    printf "install-azd-report: %b\n" "$1"
}

say_verbose() {
    if [ "$verbose" = true ]; then
        say "$1"
    fi
}

get_platform() {
    local platform_raw
    platform_raw="$(uname -s)";

    if [ "$platform_raw" = "Linux" ]; then
        echo 'linux';
        return 0;
    elif [ "$platform_raw" = "Darwin" ]; then
        echo 'darwin';
        return 0;
    else
        say_error "Platform not supported: $platform_raw";
        return 1;
    fi
}

get_os() {
    local platform
    platform="$(get_platform)"

    if [ "$platform" = "linux"  ]; then
        # shellcheck source=/dev/null
        source /etc/os-release && echo "$ID"
    elif [ "$platform" = "darwin" ]; then
        echo "MacOS"
    else
        echo "error"
    fi
}

get_os_version() {
    local platform
    platform="$(get_platform)"
    # shellcheck source=/dev/null

    if [ "$platform" = "linux"  ]; then
        # shellcheck source=/dev/null
        source /etc/os-release && echo "$VERSION_ID"
    elif [ "$platform" = "darwin" ]; then
        sw_vers -productVersion
    else
        echo "error"
    fi
}

get_is_wsl() {
    local platform
    platform="$(get_platform)"

    if [ "$platform" != "linux" ]; then
        echo "false"
        return
    fi 


    local kernel_release
    kernel_release=$(uname --kernel-release)

    # Lower-case $kernel_release for a case-insensitive compare
    # Assumes a
    if [[ "${kernel_release,,}" == *"wsl"* ]]; then
        echo "true"
    else
        echo "false"
    fi
}

get_terminal () {
    if [ -f "/proc/$$/comm" ]; then
        echo "$(</proc/$$/comm)"
    else
        ps $$ -o comm=
    fi
}

get_execution_environment() {
    local env_gh_actions=${GITHUB_ACTIONS:-}
    local env_system_teamprojectid=${SYSTEM_TEAMPROJECTID:-}

    local execution_environment="Desktop"
    if [ -n "$env_gh_actions" ]; then
        execution_environment="GitHub Actions"
    elif [ -n "$env_system_teamprojectid" ]; then
        execution_environment="Azure DevOps"
    fi
    echo "$execution_environment"
}

send_report() {
    local event_timestamp=$1
    local event_name=$2
    local reason=$3
    local version=$4

    local has_additional_items=0
    if [ "$#" -gt 4 ]; then
        has_additional_items=1
        read -ra additional_items <<< "$5"
    fi

    local IKEY='a9e6fa10-a9ac-4525-8388-22d39336ecc2'
    # Replace all "-" in IKEY with ""
    local AI_EVENT_NAME="Microsoft.ApplicationInsights.${IKEY//-/}.Event"

    local timestamp os os_version is_wsl terminal execution_environment
    timestamp=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
    os="$(get_os)"
    os_version="$(get_os_version)"
    is_wsl="$(get_is_wsl)"
    terminal="$(get_terminal)"
    execution_environment="$(get_execution_environment)"

    # If there are additional items, add those to the report outputs
    local additional_json=""
    local additional_table=""
    if [ "$has_additional_items" ]; then
        for entry in "${additional_items[@]}"
        do
            # "identifier:value:value_continued" becomes:
            # key="identifier"
            # value="value:value_continued"
            local key=${entry%%:*}
            local value=${entry#*:}
            additional_json=$(printf "%s, \"%s\": \"%s\"" "$additional_json" "$key" "$value")
            additional_table=$(printf "%s%s        \t%s\n" "$additional_table" "$key" "$value")
        done
    fi

    local json_template='{
        "iKey": "%s",
        "name": "%s",
        "time": "%s",
        "data": {
            "baseType": "EventData",
            "baseData": {
                "ver": 2,
                "name": "%s",
                "properties": {
                    "installVersion": "%s",
                    "reason": "%s",
                    "os": "%s",
                    "osVersion": "%s",
                    "isWsl": "%s",
                    "terminal": "%s",
                    "executionEnvironment": "%s",
                    "eventTimestamp": "%s"
                    %s
                }
            }
        }
    }'

    local telemetry_json
    telemetry_json=$(printf "$json_template" \
        "$IKEY" \
        "$AI_EVENT_NAME" \
        "$timestamp" \
        "$event_name" \
        "$version" \
        "$reason" \
        "$os" \
        "$os_version" \
        "$is_wsl" \
        "$terminal" \
        "$execution_environment" \
        "$event_timestamp" \
        "$additional_json"
    )

    local table="
Item                  \tValue
===============================
installVersion        \t$version
reason                \t$reason
os                    \t$os
osVersion             \t$os_version
isWsl                 \t$is_wsl
terminal              \t$terminal
executionEnvironment  \t$execution_environment
$additional_table"

    say "Sending report for installer error:"
    say "$table"

    if [ "$dry_run" = true ]; then
        say_verbose "Dry run. No telemetry posted."
        return 0
    fi

    if ! curl \
        --silent \
        --out /dev/null \
        --fail \
        --show-error \
        --header "Content-Type: application/json" \
        --request POST \
        --data "$telemetry_json" \
        "https://centralus-2.in.applicationinsights.azure.com/v2/track";
    then
        say_verbose "curl returned exit code: $?"
        return "$?"
    else
        say_verbose "Report sent"
        return 0
    fi
}

verbose=false
error_logs_file="$HOME/.azd/installer-error.log"
dry_run=false

while [[ $# -ne 0 ]];
do
  name="$1"
  case "$name" in
    -f|--file)
        shift
        error_logs_file="$1"
        ;;
    --dry-run)
        dry_run=true
        ;;
    -v|--verbose)
        verbose=true
        ;;
    -h|-?|--help)
        script_name="$(basename "$0")"
        echo "Azure Dev CLI Installer"
        echo "Usage: $script_name [--version <VERSION>] [--install-location <LOCATION>]"
        echo "       $script_name -h|-?|--help"
        echo ""
        echo "$script_name is a simple command for downloading and installing the azd CLI"
        echo ""
        echo "Options:"
        echo ""
        echo "  --file                        Error logging file (default: $error_logs_file)"
        echo ""
        echo "  --verbose                     Enable verbose logging"
        exit 0
        ;;
    *)
        say_error "Unknown argument $name"
        exit 1
        ;;
  esac
  shift
done

# Lowercase the value of AZURE_DEV_COLLECT_TELEMETRY
env_collect_telemetry=${AZURE_DEV_COLLECT_TELEMETRY:-}
collect_telemetry=$(echo "$env_collect_telemetry" | tr "[:upper:]" "[:lower:]")

# If user has opted out of telemetry return immediately
if [ "$collect_telemetry" = "no" ]; then
    say "Telemetry disabled. No error report data will be reported."
    exit 0
fi

if [ ! -f "$error_logs_file" ]; then
    say "No log file found at $error_logs_file"
    exit 0
fi


error_count=0
while IFS="|" read -r timestamp event_name reason version additional_items_serialized
do
    if ! send_report "$timestamp" "$event_name" "$reason" "$version" "$additional_items_serialized"; then
        say_error "Could not send report: $event_name at $timestamp."

        # Append the failed error report into a file for further investigation.
        # The file at $error_logs_file will be deleted to prevent
        # re-sending of events that were successfully posted.
        echo "$timestamp|$event_name|$reason|$version|$additional_items_serialized" >> "$HOME/.azd/installer-error.err"
        say_verbose "Event written $HOME/.azd/installer-error.err"
    fi
done < "$error_logs_file"

# Done sending errors, delete logs to avoid re-sending errors on another run
if [ ! "$dry_run" = true ]; then
    say_verbose "Removing $error_logs_file"
    rm "$error_logs_file"
else
    say_verbose "Dry run. Do not remove $error_logs_file"
fi

if [ "$error_count" -gt 0 ]; then
    say_error "Encountered errors sending telemetry events. Log entries which could not be posted are in $HOME/.azd/installer-error.err"
fi
