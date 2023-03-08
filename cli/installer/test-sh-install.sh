#!/usr/bin/env bash
# shellcheck disable=SC2002

say_error() {
    printf "test-sh-install: ERROR: %b\n" "$1" >&2
}

say() {
    printf "test-sh-install: %b\n" "$1"
}

# Normal install scenario
if ! cat ./install-azd.sh | "$1" -s -- --verbose --base-url "$2" --version "$3"; then
    say_error "Install failed"
    exit 1
fi

if ! azd version; then
    say_error "azd version failed"
    exit 1
fi

if ! cat ./uninstall-azd.sh | "$1"; then
    say_error "Uninstall failed"
    exit 1
fi

if which azd; then
    say_error "Uninstall did not remove azd"
    exit 1
fi


# Test install when folder does not exist
install_folder_error=$(cat ./install-azd.sh | "$1" -s -- --no-telemetry --verbose --base-url "$2" --version "$3" --install-folder "/install/folder/does/not/exist" 2>&1)

if [ ! $? ]; then
    say_error "Install should have failed on folder not existing"
    exit 1
fi

if [[ "$install_folder_error" != *"Install folder does not exist"* ]]; then
    say_error "Install should have notified the user that the folder does not exist"
    exit 1
fi

say "Test passed"
exit 0
