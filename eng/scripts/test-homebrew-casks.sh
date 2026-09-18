#!/usr/bin/env bash
# cspell:ignore postflight

set -euo pipefail

get_version() {
    local version
    version="$(curl --fail --location --silent --show-error https://aka.ms/azure-dev/versions/cli/latest | tr -d '[:space:]')"
    if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "Latest stable azd version is invalid: $version" >&2
        return 1
    fi
    printf '%s' "$version"
}

get_sha() {
    local release_url_arg="$1"
    local archive_path="$2"

    curl --fail --location --silent --show-error "$release_url_arg" --output "$archive_path"
    sha256sum "$archive_path" | cut -d ' ' -f 1
}

# Takes the cask template and injects all the SHA/version values you need to actually install
# the latest azd release.
render_cask() {
    local template_path="$1"
    local output_path="$2"
    local version_arg="$3"
    local sha256_arg="$4"
    local release_url_arg="$5"

    sed \
        -e "s/%VERSION%/$version_arg/g" \
        -e "s/%SHA256AMD64%/$sha256_arg/g" \
        -e "s/%SHA256ARM64%/$sha256_arg/g" \
        -e "s/%SHA256AMD64_LINUX%/$sha256_arg/g" \
        -e "s/%SHA256ARM64_LINUX%/$sha256_arg/g" \
        -e "s|url \".*azd-linux-#{arch}.tar.gz\"|url \"$release_url_arg\"|" \
        "$template_path" > "$output_path"
}

test_cask() {
    local cask_token="$1"
    local version_arg="$2"
    local full_cask_name="azure/azd/$cask_token"

    local brew_output

    # Load the cask and make sure Homebrew reports no deprecation warnings.
    if ! brew_output="$(brew deps --cask "$full_cask_name" 2>&1)"; then
        echo "$brew_output" >&2
        return 1
    fi
    if grep -qi "deprecated" <<<"$brew_output"; then
        echo "$brew_output" >&2
        return 1
    fi

    # check that we're installing the right version of azd
    brew install --cask "$full_cask_name"
    local azd_version
    azd_version="$(azd version)"
    if [[ "$azd_version" != *"azd version $version_arg "* ]]; then
        echo "Expected azd version $version_arg, got: $azd_version" >&2
        return 1
    fi

    # check that our little "installed by" marker file is written out (used to
    # determine if we were installed by homebrew)
    local marker
    marker="$(brew --caskroom "$cask_token")/$version_arg/.installed-by.txt"
    if [[ "$(cat "$marker")" != "brew" ]]; then
        echo "Expected $marker to contain 'brew'" >&2
        return 1
    fi

    brew uninstall --cask "$cask_token"
}

# use a clean directory for the test
test_dir="$(mktemp -d)"
readonly test_dir
trap 'rm -rf "$test_dir"' EXIT

case "$(uname -m)" in
    aarch64) arch="arm64" ;;
    x86_64) arch="amd64" ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
readonly arch

version="$(get_version)"
readonly version
release_url="https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_$version/azd-linux-$arch.tar.gz"
readonly release_url
sha256="$(get_sha "$release_url" "$test_dir/azd.tar.gz")"
readonly sha256

export HOMEBREW_NO_AUTO_UPDATE=1

# (brew needs these set)
# Don't worry, this is running _inside_ of the container :)
git config --global user.name "azd test"
git config --global user.email "azd-test@example.invalid"

# Activating developer mode so we can use tap-new's developer-mode
# without warnings.
brew developer on
brew tap-new azure/azd

tap_casks="$(brew --repository azure/azd)/Casks"
readonly tap_casks
mkdir -p "$tap_casks"

render_cask "/workspace/eng/templates/brew.cask.template" "$tap_casks/azd.rb" "$version" "$sha256" "$release_url"
render_cask "/workspace/eng/templates/brew.cask.daily.template" "$tap_casks/azd@daily.rb" "$version" "$sha256" "$release_url"

test_cask "azd" "$version"
test_cask "azd@daily" "$version"
