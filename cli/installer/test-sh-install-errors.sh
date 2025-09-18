#!/usr/bin/env bash
# shellcheck disable=SC2002

say_error() {
    printf "test-sh-install-errors: ERROR: %b\n" "$1" >&2
}

say() {
    printf "test-sh-install-errors: %b\n" "$1"
}

echo "Test install with invalid install folder"
install_folder_error=$(cat install-azd.sh | "$1" -s -- --no-telemetry --verbose --base-url "$2" --version "$3" --symlink-folder "/symlink/folder/does/not/exist" 2>&1)

if [[ "$install_folder_error" != *"Symlink folder does not exist"* ]]; then
    say_error "Install should have notified the user that the folder does not exist"
    exit 1
fi

say "Test passed"
exit 0
