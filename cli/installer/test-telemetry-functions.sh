#!/usr/bin/env bash
set -e
set -u

say_error() {
    printf "test-telemetry-functions: ERROR: %b\n" "$1" >&2
}

say() {
    printf "test-telemetry-functions: %b\n" "$1"
}


expectation_location=${1:-telemetry.csv}

bash -c './install-azd.sh --base-url "127.0.0.1/error" --verbose' | tee install_run.log
# Use --dry-run to avoid sending test telemetry
bash -c './install-azd-report.sh --verbose --dry-run' | tee install_report.log

while IFS=, read -r expected_item;
do
    IFS=','; read -ra match_params <<< "$expected_item"
    needle="${match_params[0]}"
    expected="${match_params[1]}"
    found_item=0
    while IFS= read -r line;
    do
        if [[ "$line" == "$needle"* ]]; then
            if [[ "$line" == "$needle"*"$expected" ]]; then
                found_item=1
                break
            else
                say_error "MISMATCH"
                say_error "Term: $needle"
                say_error "Expected: $expected"
                say_error "Actual: $line"
                exit 1
            fi
        fi
    done < install_report.log

    if [ "$found_item" == 0 ]; then
        say_error "Could not find: $needle"
        exit 1
    fi

done < "$expectation_location"

say "Tests passed"
