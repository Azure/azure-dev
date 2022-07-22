#!/usr/bin/env bash
# shellcheck disable=SC2002

say_error() {
    printf "test-sh-install: ERROR: %b\n" "$1" >&2
}

say() {
    printf "test-sh-install: %b\n" "$1"
}

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

say "Test passed"
exit 0