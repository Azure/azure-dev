#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------

AZD_VERSION=${VERSION:-"stable"}

check_packages() {
    if ! dpkg -s "$@" > /dev/null 2>&1; then
        if [ "$(find /var/lib/apt/lists/* | wc -l)" = "0" ]; then
            echo "Running apt-get update..."
            apt-get update -y
        fi
        apt-get -y install --no-install-recommends "$@"
    fi
}

echo "(*) Ensuring dependencies are installed"

check_packages apt-transport-https curl ca-certificates xdg-utils
check_packages $(apt-cache search '^libicu[0-9]+$' | cut -d' ' -f1)

echo "(*) Installing Azure Developer CLI"

curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --version $AZD_VERSION -a $(dpkg --print-architecture)
