#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

save_error_report_if_enabled() {
    local event_name=$1
    local reason=$2

    local has_additional_items=0
    if [ "$#" -gt 2 ]; then
        has_additional_items=1
        declare -a additional_items=("${!3}")
    fi

    local timestamp
    timestamp=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)

    # Lowercase the value of AZURE_DEV_COLLECT_TELEMETRY
    local env_collect_telemetry=${AZURE_DEV_COLLECT_TELEMETRY:-}
    local collect_telemetry
    collect_telemetry=$(echo "$env_collect_telemetry" | tr "[:upper:]" "[:lower:]")

    # If user has opted out of telemetry return immediately
    if [ "$no_telemetry" = 1 ] || [ "$collect_telemetry" = "no" ]; then
        say_verbose "Telemetry disabled. No error report data stored."
        return
    fi


    local additional_items_serialized=""
    if [ "$has_additional_items" ]; then
        for entry in "${!additional_items[@]}"
        do
            local entry_value=${additional_items[$entry]}
            additional_items_serialized=$(printf "%s|%s" "$additional_items_serialized" "$entry_value")
        done
    fi

    mkdir -p ~/.azd
    echo "$timestamp|$event_name|$reason|$version$additional_items_serialized" >> ~/.azd/installer-error.log

    say_error "An error was encountered during install: $reason"
    say_error ""
    say_error "To send an error report to Microsoft, check that AZURE_DEV_COLLECT_TELEMETRY is not set and then run: "
    say_error "curl -fsSL https://aka.ms/install-azd-report.sh | bash"
    say_error ""
    say_error "Running the above script will send data to Microsoft. To learn more about data collection see:"
    say_error "https://go.microsoft.com/fwlink/?LinkId=521839"
    say_error ""
    say_error "You can also file an issue at https://github.com/Azure/azure-dev/issues/new?assignees=&labels=&template=issue_report.md&title=%5BIssue%5D"
}

say_error() {
    printf "install-azd: ERROR: %b\n" "$1" >&2
}

say() {
    printf "install-azd: %b\n" "$1"
}

say_verbose() {
    if [ "$verbose" = true ]; then
        say "$1"
    fi
}

trap 'catch_all' ERR
catch_all() {
    say_error "Unhandled error"
    save_error_report_if_enabled "InstallFailed" "UnhandledError"
    exit 1
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

get_extension_for_platform() {
    platform=$1

    if [ "$platform" = "linux" ]; then
        echo 'tar.gz';
        return;
    elif [ "$platform" = "darwin" ]; then
        echo 'zip';
        return;
    else
        say_error "Platform not supported: $platform";
        exit 1;
    fi;
}

get_architecture() {
    local platform=$1
    local architecture_raw
    architecture_raw="$(uname -m)"

    if [ "$architecture_raw" = "x86_64" ]; then
        echo 'amd64';
        return;
    elif [ "$architecture_raw" = "arm64" ] && [ "$platform" = 'darwin' ]; then
        # In the case of Apple Silicon use the existing ARM64 environment
        echo 'amd64';
        return;
    else
        say_error "Architecture not supported: $architecture_raw on platform: $platform"
        exit 1;
    fi;
}

extract() {
    local compressed_file=$1
    local target_file=$2
    local extract_location=$3

    if [[ $compressed_file == *.zip ]]; then
        unzip "$compressed_file" "$target_file" -d "$extract_location"/
    elif [[ $compressed_file == *.tar.gz ]]; then
        tar -zxvf "$compressed_file" -C "$extract_location"/ "$target_file"
    else
        say_error "Target file not supported: $compressed_file";
    fi
}

DEFAULT_BASE_URL="https://azure-dev.azureedge.net/azd/standalone/release"

base_url="$DEFAULT_BASE_URL"
platform="$(get_platform)"
extension="$(get_extension_for_platform "$platform")"
architecture="$(get_architecture "$platform")"
version="latest"
dry_run=false
install_folder="/usr/local/bin"
no_telemetry=0
verbose=false

while [[ $# -ne 0 ]];
do
  name="$1"
  case "$name" in
    -u|--base-url)
        shift
        base_url="$1"
        ;;
    -e|--extension)
        shift
        extension="$1"
        ;;
    -p|--platform)
        shift
        platform="$1"
        ;;
    -a|--architecture)
        shift
        architecture="$1"
        ;;
    --version)
        shift
        version="$1"
        ;;
    --dry-run)
        dry_run=true
        ;;
    -i|--install-folder)
        shift
        install_folder="$1"
        ;;
    --no-telemetry)
        shift
        no_telemetry=true
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
        echo "  -u, --base-url <URL>          Download from the specific base URL. Defaults to"
        echo "                                $DEFAULT_BASE_URL"
        echo ""
        echo "  -e, --extension <EXTENSION>   Set the extension for the file to download."
        echo "                                Default is set based on the detected platform."
        echo "                                Possible values: zip, tar.gz"
        echo ""
        echo "  -p, --platform <PLATFORM>     Download for the specified PLATFORM. Default is"
        echo "                                detected automatically."
        echo ""
        echo "  -a, --architecture <ARCHITECTURE> Download for the specified ARCHITECTURE."
        echo "                                    Default is detected automatically."
        echo ""
        echo "  --version <VERSION>           Download specific version. Default is 'latest'"
        echo "                                which specifies the most recently released"
        echo "                                version (GA or preview)."
        echo "                                Possible values: <version>, latest, daily"
        echo ""
        echo "  --dry-run                     Do not download or install, just display the"
        echo "                                download URL."
        echo ""
        echo "  -i,--install-folder <FOLDER>  Install azd CLI to FOLDER. Default is"
        echo "                                /usr/local/bin"
        echo ""
        echo "  -t,--no-telemetry             Disable telemetry for failures. The installer"
        echo "                                will prompt before sending any telemetry. In"
        echo "                                non-interactive terminals telemetry will not"
        echo "                                be sent and no prompt will be issued."
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


say_verbose "Version: $version"
say_verbose "Platform: $platform"
say_verbose "Architecture: $architecture"
say_verbose "File extension: $extension"

if [ -z "$version" ]; then
    url="$base_url/azd-$platform-$architecture.$extension"
else
    url="$base_url/$version/azd-$platform-$architecture.$extension"
fi

if [ "$dry_run" = true ]; then
    say "$url"
    exit 0;
fi

tmp_folder="$(mktemp -d)";
compressed_file_path="$tmp_folder/azd-$platform-$architecture.$extension"
say_verbose "Downloading $url to $tmp_folder"

if ! curl -so "$compressed_file_path" "$url" --fail; then
    say_error "Could not download from $url, ensure platform and architecture are supported"

    # shellcheck disable=SC2034
    declare -a additional=( "downloadUrl:$url" )
    save_error_report_if_enabled "InstallFailed" "DownloadFailure" additional[@]
    exit 1
fi

bin_name="azd-$platform-$architecture"
extract "$compressed_file_path" "$bin_name" "$tmp_folder"
chmod +x "$tmp_folder/$bin_name"

install_location="$install_folder/azd"
if [ -w "$install_folder/" ]; then
    mv "$tmp_folder/$bin_name" "$install_location"
else
    say "Writing to $install_folder/ requires elevated permission. You may be prompted to enter credentials."
    if ! sudo mv "$tmp_folder/$bin_name" "$install_location"; then
        say_error "Could not copy file to install location: $install_location"
        save_error_report_if_enabled "InstallFailed" "SudoMoveFailure"
        exit 1
    fi
fi

say_verbose "Cleaning up temp folder: $tmp_folder"
rm -rf "$tmp_folder"
say "Successfully installed to $install_location"
say ""
say "The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience."
say "You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use."
say ""
say "Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection"
