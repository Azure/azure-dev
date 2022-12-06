#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u

set pipefail

# Install PowerShell
# https://learn.microsoft.com/en-us/powershell/scripting/install/install-other-linux?view=powershell-7.3#binary-archives
sudo apt update
sudo apt install -y curl
curl -L -o /tmp/powershell.tar.gz https://github.com/PowerShell/PowerShell/releases/download/v7.3.0/powershell-7.3.0-linux-arm64.tar.gz
sudo mkdir -p /opt/microsoft/powershell/7
sudo tar zxf /tmp/powershell.tar.gz -C /opt/microsoft/powershell/7
sudo chmod +x /opt/microsoft/powershell/7/pwsh
sudo ln -s /opt/microsoft/powershell/7/pwsh /usr/bin/pwsh

echo "PowerShell install complete:"
pwsh --version

# Install Go (work around GoTool ADO task)
go_file="go1.19.3.linux-arm64.tar.gz"
curl -L https://golang.google.cn/dl/$go_file -o $go_file
rm -rf /usr/local/go && tar -C /usr/local -xzf $go_file
echo "##vso[task.prependpath]/usr/local/go/bin"

echo "Go install complete:"
/usr/local/go/bin/go version

echo "Pre-reqs installed"
