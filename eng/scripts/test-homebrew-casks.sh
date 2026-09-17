#!/usr/bin/env bash
# cspell:ignore postflight

set -euo pipefail

version="$(curl --fail --location --silent --show-error https://aka.ms/azure-dev/versions/cli/latest | tr -d '[:space:]')"
readonly version
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Latest stable azd version is invalid: $version" >&2
    exit 1
fi

test_dir="$(mktemp -d)"
readonly test_dir
trap 'rm -rf "$test_dir"' EXIT

case "$(uname -m)" in
    aarch64) arch="arm64" ;;
    x86_64) arch="amd64" ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
readonly arch

release_url="https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_$version/azd-linux-$arch.tar.gz"
readonly release_url
curl --fail --location --silent --show-error "$release_url" --output "$test_dir/azd.tar.gz"
sha256="$(sha256sum "$test_dir/azd.tar.gz" | cut -d ' ' -f 1)"
readonly sha256

render_cask() {
    local template_path="$1"
    local output_path="$2"

    sed \
        -e "s/%VERSION%/$version/g" \
        -e "s/%SHA256AMD64%/$sha256/g" \
        -e "s/%SHA256ARM64%/$sha256/g" \
        -e "s/%SHA256AMD64_LINUX%/$sha256/g" \
        -e "s/%SHA256ARM64_LINUX%/$sha256/g" \
        -e "s|url \".*azd-linux-#{arch}.tar.gz\"|url \"$release_url\"|" \
        "$template_path" > "$output_path"
}

test_cask() {
    local token="$1"
    local qualified_token="azure/azd/$token"
    local deps_output
    local deprecated_warning="Calling \`postflight\` is deprecated"

    if ! deps_output="$(brew deps --cask "$qualified_token" 2>&1)"; then
        echo "$deps_output" >&2
        return 1
    fi
    if [[ "$deps_output" == *"$deprecated_warning"* ]]; then
        echo "$deps_output" >&2
        return 1
    fi

    brew install --cask "$qualified_token"
    azd_version="$(azd version)"
    if [[ "$azd_version" != *"azd version $version "* ]]; then
        echo "Expected azd version $version, got: $azd_version" >&2
        return 1
    fi

    local marker
    marker="$(brew --caskroom "$token")/$version/.installed-by.txt"
    if [[ "$(cat "$marker")" != "brew" ]]; then
        echo "Expected $marker to contain 'brew'" >&2
        return 1
    fi

    brew uninstall --cask "$token"
}

export HOMEBREW_NO_AUTO_UPDATE=1
export GIT_AUTHOR_NAME="azd test"
export GIT_AUTHOR_EMAIL="azd-test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

# Don't worry, this is running _inside_ of the container :)
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"

# Avoid tap-new's developer-mode warning in test output.
brew developer on
brew tap-new azure/azd

tap_casks="$(brew --repository azure/azd)/Casks"
readonly tap_casks
mkdir -p "$tap_casks"

render_cask "/workspace/eng/templates/brew.cask.template" "$tap_casks/azd.rb"
render_cask "/workspace/eng/templates/brew.cask.daily.template" "$tap_casks/azd@daily.rb"

test_cask "azd"
test_cask "azd@daily"
