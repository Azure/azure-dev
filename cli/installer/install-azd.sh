#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

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

get_platform() {
    platform_raw="$(uname -s)";

    if [ "$platform_raw" == "Linux" ]; then
        echo 'linux';
        return;
    elif [ "$platform_raw" == "Darwin" ]; then
        echo 'darwin';
        return;
    else
        say_error "Platform not supported: $platform_raw";
        exit 1;
    fi
}

get_extension_for_platform() {
    platform=$1

    if [ "$platform" == "linux" ]; then
        echo 'tar.gz';
        return;
    elif [ "$platform" == "darwin" ]; then
        echo 'zip';
        return;
    else
        say_error "Platform not supported: $platform";
        exit 1;
    fi;
}

get_architecture() {
    platform=$1
    architecture_raw="$(uname -m)"

    if [ "$architecture_raw" == "x86_64" ]; then
        echo 'amd64';
        return;
    elif [ "$architecture_raw" == "arm64" ] && [ "$platform" == 'darwin' ]; then
        # In the case of Mac M1
        echo 'amd64';
        return;
    else
        say_error "Architecture not supported: $architecture_raw on platform: $platform"
        exit 1;
    fi;

}

extract() {
    compressed_file=$1
    target_file=$2
    extract_location=$3

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
    exit 1
fi

bin_name="azd-$platform-$architecture"
extract "$compressed_file_path" "$bin_name" "$tmp_folder"
chmod +x "$tmp_folder/$bin_name"

install_location="$install_folder/azd"
if [ -w "$install_folder/" ]; then
    cp "$tmp_folder/$bin_name" "$install_location"
else
    say "Writing to $install_folder/ requires elevated permission. You may be prompted to enter credentials."
    sudo cp "$tmp_folder/$bin_name" "$install_location"
fi

say_verbose "Cleaning up temp folder: $tmp_folder"
rm -rf "$tmp_folder"
say "Successfully installed to $install_location"
