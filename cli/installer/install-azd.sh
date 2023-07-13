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

ensure_rosetta() {
    if [[ $(uname -m) == 'x86_64' ]]; then
        # The current system is identified as an Intel system (either because it
        # is running in Rosetta 2 or the system is running on Intel silicon) so
        # Rosetta 2 is not needed.
        say_verbose "Detected x86_64 system. Rosetta 2 is not needed."
        return
    fi

    if /usr/bin/pgrep oahd >/dev/null 2>&1; then
        say "Rosetta 2 is already installed and running. Nothing to do."
    else
        say "Rosetta 2 is not installed. You may be prompted to accept terms necessary to install Rosetta 2."

        # Ensure that softwareupdate gets input from the terminal
        if /usr/sbin/softwareupdate --install-rosetta </dev/tty; then
            say "Rosetta has been successfully installed."
        else
            say_error "Rosetta 2 installation failed!"
            save_error_report_if_enabled "InstallFailed" "Rosetta2InstallFailed"
            exit 1
        fi
    fi
}

extract() {
    local compressed_file=$1
    local extract_location=$2

    if [[ $compressed_file == *.zip ]]; then
        unzip "$compressed_file" -d "$extract_location"/
    elif [[ $compressed_file == *.tar.gz ]]; then
        tar -zxvf "$compressed_file" -C "$extract_location"/
    else
        say_error "Target file not supported: $compressed_file";
    fi
}

DEFAULT_BASE_URL="https://azure-dev.azureedge.net/azd/standalone/release"

base_url="$DEFAULT_BASE_URL"
platform="$(get_platform)"
extension="$(get_extension_for_platform "$platform")"
version="stable"
dry_run=false
skip_verify=false
symlink_folder="/usr/local/bin"
install_folder="/opt/microsoft/azd"
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
    --skip-verify)
        skip_verify=true
        ;;
    -i|--install-folder)
        shift
        install_folder="$1"
        ;;
    -s|--symlink-folder)
        shift
        symlink_folder="$1"
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
        echo "  --skip-verify                 Skip verification of the downloaded file (macOS only)."
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

if [ "${architecture:-}" = "" ]; then
    architecture="$(get_architecture "$platform")"
fi

if [ "$symlink_folder" != "" ] && [ ! -d "$symlink_folder" ]; then
    say_error "Symlink folder does not exist: $symlink_folder. The symlink folder should exist and be in \$PATH"
    say_error "Create the folder (and ensure that it is in your \$PATH), specify a different folder using -s or --symlink-folder, or specify an empty value using -s \"\" or --symlink-folder \"\""
    save_error_report_if_enabled "InstallFailed" "SymlinkFolderDoesNotExist"
    exit 1
fi

say_verbose "Version: $version"
say_verbose "Platform: $platform"
say_verbose "Architecture: $architecture"
say_verbose "File extension: $extension"

if [ "$platform" = "darwin" ] && [ "$architecture" = "amd64" ]; then
    say_verbose "Mac detected, ensuring compatibility with amd64 binaries"
    ensure_rosetta
fi

# ARM64 bits are in beta, and so both the distribution package and the azd binary inside have a `-beta` suffix we
# need to take into account.
suffix=""
if [ "$architecture" = "arm64" ]; then
    suffix="-beta"
fi

if [ -z "$version" ]; then
    url="$base_url/azd-$platform-$architecture$suffix.$extension"
else
    url="$base_url/$version/azd-$platform-$architecture$suffix.$extension"
fi

if [ "$dry_run" = true ]; then
    say "$url"
    exit 0;
fi

tmp_folder="$(mktemp -d)";
compressed_file_path="$tmp_folder/azd-$platform-$architecture$suffix.$extension"
say_verbose "Downloading $url to $tmp_folder"

if ! curl -so "$compressed_file_path" "$url" --fail; then
    say_error "Could not download from $url, ensure platform and architecture are supported"

    # shellcheck disable=SC2034
    declare -a additional=( "downloadUrl:$url" )
    save_error_report_if_enabled "InstallFailed" "DownloadFailure" additional[@]
    exit 1
fi

bin_name="azd-$platform-$architecture$suffix"
extract "$compressed_file_path" "$tmp_folder"
rm "$compressed_file_path"
chmod +x "$tmp_folder/$bin_name"

say_verbose "Writing to $tmp_folder/.installed-by.txt"
echo "install-azd.sh" > "$tmp_folder/.installed-by.txt"

if [ "$platform" = "darwin" ] && [ "$skip_verify" = false ]; then
    say_verbose "Verifying signature of $bin_name"
    if ! output=$( codesign -v "$tmp_folder/$bin_name" 2>&1); then
        say_error "Could not verify signature of $bin_name, error output:"
        say_error "$output"
        save_error_report_if_enabled "InstallFailed" "SignatureVerificationFailure"
        exit 1
    fi
fi

if [[ ! -d "$install_folder" ]]; then
    say_verbose "Install folder does not exist: $install_folder. Creating..." 

    if ! mkdir -p "$install_folder"; then 
        say "Creating $install_folder requires elevated permission. You may be prompted to enter credentials." 
        if ! sudo mkdir -p "$install_folder"; then
            say_error "Could not create install folder: $install_folder"
            save_error_report_if_enabled "InstallFailed" "SudoMkdirFailure"
            exit 1
        fi
    fi
fi

mv_preface=""
if [ ! -w "$install_folder/" ]; then
    say "Writing to $install_folder/ requires elevated permission. You may be prompted to enter credentials."
    mv_preface="sudo"
fi
if ! $mv_preface mv -f "$tmp_folder"/* "$tmp_folder"/.*.txt "$install_folder"; then
    say_error "Could not move files to install location: $install_folder"
    save_error_report_if_enabled "InstallFailed" "MoveFailure"
    exit 1
fi

if [ "$symlink_folder" != "" ]; then
    ln_preface=""
    if [ ! -w "$symlink_folder/" ]; then
        say "Writing to $symlink_folder/ requires elevated permission. You may be prompted to enter credentials." 
        ln_preface="sudo"
    fi
    if ! $ln_preface ln -fs "$install_folder/$bin_name" "$symlink_folder/azd"; then
        say_error "Could not create symlink to azd in $symlink_folder"
        save_error_report_if_enabled "InstallFailed" "SymlinkCreateFailure"
        exit 1
    fi
fi

say_verbose "Cleaning up temp folder: $tmp_folder"
rm -rf "$tmp_folder"
say "Successfully installed to $install_folder"
if [ "$symlink_folder" != "" ]; then
    say "Symlink created at $symlink_folder/azd and pointing to $install_folder/$bin_name"
fi
say ""
say "The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience."
say "You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use."
say ""
say "Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection"
