#!/usr/bin/env bash

if [ -w "$HOME/.azd/bin" ]; then
    if ! rm -rf "$HOME/.azd/bin"; then
        echo "Could not remove files in $HOME/.azd/bin. These will need to be removed manually"
    fi
fi

echo ""
echo "======================================"
echo " Azure Developer CLI Uninstall Notice "
echo "======================================"
echo "The Azure Developer CLI may have downloaded binaries to ~/.azd/bin and,"
echo "depending on how azd was used on this machine, may have downloaded binaries"
echo "to other users' home directories in their .azd/bin directory."
echo "These binaries will need to be removed manually."
echo "To remove such binaries from your home directory, run 'rm -rf ~/.azd/bin'."
echo ""
