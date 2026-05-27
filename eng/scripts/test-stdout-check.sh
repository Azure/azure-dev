#!/bin/sh
# test-stdout-check.sh — Detect direct stdout writes in Go test files.
#
# Tests that write to os.Stdout corrupt the go test -json event stream under
# parallel execution, causing phantom test failures in CI. This check flags
# common patterns: fmt.Print/Println/Printf (without a writer argument) and
# direct os.Stdout references.
#
# Usage:
#   ./eng/scripts/test-stdout-check.sh <directory>
#
# Exit codes:
#   0  No violations found
#   1  Violations found (or usage error)

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <directory>"
    exit 1
fi

DIRECTORY="$1"
VIOLATIONS=0

# Allowlisted files (intentional stdout usage in tests)
is_allowlisted() {
    case "$1" in
        */testing_helpers_test.go) return 0 ;;       # Tests CaptureOutput which intentionally writes to stdout
        */extension_commands_test.go) return 0 ;;     # Tests stdout redirection
        */show_test.go) return 0 ;;                   # Tests stdout capture for CLI output
        */external_prompt_test.go) return 0 ;;        # Sets cobra command output
        */colors_test.go) return 0 ;;                 # Checks os.Stdout.Stat() for TTY detection
        */ux_test.go) return 0 ;;                     # Checks os.Stdout.Stat() for TTY detection
        *) return 1 ;;
    esac
}

# Find Go test files and check for stdout-writing patterns
for file in $(find "$DIRECTORY" -name '*_test.go' -not -path '*/vendor/*'); do
    if is_allowlisted "$file"; then
        continue
    fi

    # Check for fmt.Print, fmt.Println, fmt.Printf (bare calls that write to stdout)
    # Exclude fmt.Fprint*, fmt.Sprint*, fmt.Errorf which write to a writer or return strings
    # Use fmt\.Print(f|ln)?\( to match actual function calls, avoiding substring matches
    if grep -nE 'fmt\.(Print|Println|Printf)\(' "$file" | grep -vE 'fmt\.(Fprint|Sprint|Errorf|Fprintf|Fprintln|Sprintf)\(' > /dev/null 2>&1; then
        grep -nE 'fmt\.(Print|Println|Printf)\(' "$file" | grep -vE 'fmt\.(Fprint|Sprint|Errorf|Fprintf|Fprintln|Sprintf)\(' | while read -r match; do
            # Skip lines with nolint:forbidigo or nolint:test-stdout
            case "$match" in
                *nolint*) continue ;;
            esac
            echo "ERROR: $file:$match"
            echo "  Use t.Log/t.Logf instead of fmt.Print* in tests to avoid corrupting go test -json output."
        done
        VIOLATIONS=1
    fi

    # Check for os.Stdout references (exclude read-only .Stat() and comments)
    # Match os.Stdout as a standalone reference (not as part of another identifier)
    if grep -nE 'os\.Stdout[^a-zA-Z_]|os\.Stdout$' "$file" | grep -vE '\.Stat\(\)|^[[:space:]]*//' > /dev/null 2>&1; then
        grep -nE 'os\.Stdout[^a-zA-Z_]|os\.Stdout$' "$file" | grep -vE '\.Stat\(\)|^[[:space:]]*//' | while read -r match; do
            case "$match" in
                *nolint*) continue ;;
            esac
            echo "ERROR: $file:$match"
            echo "  Use io.Discard or a bytes.Buffer instead of os.Stdout in tests."
        done
        VIOLATIONS=1
    fi
done

if [ "$VIOLATIONS" -ne 0 ]; then
    echo ""
    echo "Found test files writing directly to stdout."
    echo "This corrupts the go test -json event stream and causes phantom test failures in CI."
    echo "See https://github.com/Azure/azure-dev/issues/8385 for details."
    exit 1
fi

echo "No stdout-writing violations found in test files."
exit 0
