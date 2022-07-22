#!/usr/bin/env bash

install_location="/usr/local/bin/azd"

if [ ! -f "$install_location" ]; then
    echo "azd is not installed at $install_location. To install run 'curl -fsSL https://aka.ms/install-azd.sh | bash'"
    exit 1
fi

if [ -w "$install_location" ]; then
    rm "$install_location"
else
    echo "Writing to $install_location requires elevated permission. You may be prompted to enter credentials."
    sudo rm "$install_location"
fi

still_installed_location="$(command -v azd)";
if [ "$still_installed_location" ]; then
    echo "Uninstallation may not be complete: azd was still found at an unmanaged location ($still_installed_location). Please remove manually."
    exit 1
fi
