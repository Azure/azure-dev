#!/bin/bash

set -e

# Optional: Import test library
source dev-container-features-test-lib

# Definition specific tests
check "version" azd version

azd extension list --installed 

matching_extension_count=$(azd extension list --installed | grep "^azure.coding-agent" | wc -l)
check "check extensions" bash -c "test \"$matching_extension_count\" -eq 1"

matching_extension_count=$(azd extension list --installed | grep "^microsoft.azd.demo" | wc -l)
check "check extensions" bash -c "test \"$matching_extension_count\" -eq 1"

# Report result
reportResults
