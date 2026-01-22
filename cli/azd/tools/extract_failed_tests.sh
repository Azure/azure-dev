#!/bin/bash

# Extract failed test names from a failed test CI pipeline and output as go test arguments
# Usage: ./extract_failed_tests.sh [pipeline.log] [-l|--list]
# 
# Examples:
#   ./extract_failed_tests.sh pipeline.log              # Output pipe-separated for: go test -run "..."
#   ./extract_failed_tests.sh pipeline.log -l           # Output test names, one per line
#   go test -run "$(./extract_failed_tests.sh pipeline.log)" ./...

LOGFILE="${1:-pipeline.log}"
FORMAT="${2:--r}"

if [[ ! -f "$LOGFILE" ]]; then
    echo "Error: File '$LOGFILE' not found" >&2
    exit 1
fi

# Extract test names from FAIL lines
# Expected format: [timestamp] === FAIL: test/functional Test_CLI_Up_Down_ContainerAppDotNetPublish (duration)
tests=$(grep "=== FAIL: test/functional" "$LOGFILE" | sed -E 's/.*=== FAIL: test\/functional ([^ ]+).*/\1/' | sort -u)

if [[ -z "$tests" ]]; then
    echo "No failed tests found in $LOGFILE" >&2
    exit 1
fi

# Output format
case "$FORMAT" in
    -r|--run|*)
        # Pipe-separated format for: go test -run "Test1|Test2|Test3" (default)
        echo "$tests" | paste -sd '|' -
        ;;
    -l|--list)
        # One per line
        echo "$tests"
        ;;
esac
