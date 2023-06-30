#!/usr/bin/env bash

pwd

yum install -y ./azd-*.rpm && azd version || exit 1

if ! grep -q "rpm" /opt/microsoft/azd/.installed-by.txt; then
    echo ".installed-by.txt file content incorrect" 
    exit 1
fi

yum remove -y azd || exit 1

if command azd; then
    echo "azd NOT UNINSTALLED"
    exit 1
else 
    echo "azd uninstall successful"
fi
