#!/usr/bin/env bash

symlink_location="/usr/local/bin/azd"
install_root="/opt/microsoft/azd"

if [ ! -f "$symlink_location" ]; then
    echo "azd is not installed at $symlink_location. To install run 'curl -fsSL https://aka.ms/install-azd.sh | bash'"
    exit 1
fi

if ! rm "$symlink_location"; then
    echo "Writing to $symlink_location requires elevated permission. You may be prompted to enter credentials."
    if ! sudo rm "$symlink_location"; then 
        echo "Could not remove $symlink_location" 
        exit 1
    fi
fi

if [ ! -d "$install_root" ]; then 
    echo "azd could not be found at $install_root. To install run 'curl -fsSL https://aka.ms/install-azd.sh | bash'"
    exit 1
fi 

if ! rm -rf "$install_root"; then
    echo "Writing to $install_root requires elevated permission. You may be prompted to enter credentials."
    if ! sudo rm -rf "$install_root"; then 
        echo "Could not remove files from $install_root" 
        exit 1
    fi
fi 

if [ -w "$HOME/.azd/bin" ]; then
    if ! rm -rf "$HOME/.azd/bin"; then
        echo "Could not remove files in $HOME/.azd/bin. These will need to be removed manually"
    fi
fi

echo ""
echo "azd may have downloaded binaries to ~/.azd/bin and, depending on how azd was used on this machine,"
echo "may have downloaded binaries to other users' home directories in their .azd/bin directory."
echo "These binaries will need to be removed manually."
echo "To remove such binaries from your home directory, run 'rm -rf ~/.azd/bin'."

still_installed_location="$(command -v azd)";
if [ "$still_installed_location" ]; then
    echo "Uninstallation may not be complete: azd was still found at an unmanaged location ($still_installed_location). Please remove manually."
    exit 1
fi
