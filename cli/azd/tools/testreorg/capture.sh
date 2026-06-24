#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.
#
# capture.sh prints the set of test/helper function names (qualified by package
# directory and receiver type) defined in every non-extension *_test.go file
# under the current directory, one per line, sorted.
#
# Run it before and after a reorganization and diff the two outputs: an empty
# diff proves that every declaration was moved exactly once and that no test was
# dropped or duplicated. This is the safety invariant used for issue #8799.
#
# Usage (from cli/azd):
#   tools/testreorg/capture.sh > before.txt
#   # ... perform the reorganization ...
#   tools/testreorg/capture.sh > after.txt
#   diff before.txt after.txt && echo "IDENTICAL"
set -euo pipefail

find . -name '*_test.go' | grep -v '/extensions/' | while read -r f; do
	dir=$(dirname "$f")
	grep -hoE '^func (\([^)]*\) )?[A-Za-z0-9_]+' "$f" |
		sed -E 's/^func \(([a-zA-Z0-9_]+ )?\*?([A-Za-z0-9_]+)\) /\2./' |
		sed -E 's/^func //' |
		sed "s|^|$dir |"
done | sort
