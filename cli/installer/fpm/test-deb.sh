#!/usr/bin/env bash

apt install ./azd_*.deb -f -y && azd version || exit 1
dpkg -r azd || exit 1

if command azd; then
    echo "azd NOT UNINSTALLED"
    exit 1
else 
    echo "azd uninstall successful"
fi
