#!/usr/bin/env bash

# Stop script on non-zero exit code
set -e
# Stop script if unbound variable found (use ${var:-} if intentional)
set -u
set pipefail

temp_dir=$(mktemp -d /tmp/tool-preinstall-XXXXXXXX)

# Install PowerShell
# https://learn.microsoft.com/en-us/powershell/scripting/install/install-other-linux?view=powershell-7.3#binary-archives
sudo apt update
sudo apt install -y curl
curl -L -o "$temp_dir/powershell.tar.gz" https://github.com/PowerShell/PowerShell/releases/download/v7.3.0/powershell-7.3.0-linux-arm64.tar.gz
sudo mkdir -p /opt/microsoft/powershell/7
sudo tar zxf "$temp_dir/powershell.tar.gz" -C /opt/microsoft/powershell/7
sudo chmod +x /opt/microsoft/powershell/7/pwsh
sudo ln -s /opt/microsoft/powershell/7/pwsh /usr/bin/pwsh

echo "PowerShell install complete:"
pwsh --version

# Install Go (work around GoTool ADO task)
# https://go.dev/doc/install
go_file="go1.19.3.linux-arm64.tar.gz"
curl -L https://golang.google.cn/dl/$go_file -o "$temp_dir/$go_file"
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf "$temp_dir/$go_file"
echo "##vso[task.prependpath]/usr/local/go/bin"

echo "Go install complete:"
/usr/local/go/bin/go version

# Install Terraform (workaround ms-devlabs.custom-terraform-tasks.custom-terraform-installer-task.TerraformInstaller@0)
# Tool issue: https://github.com/microsoft/azure-pipelines-terraform/issues/116
# Instructions: https://developer.hashicorp.com/terraform/downloads
# Research: 
# Hashicorp does not support packaging for ARM64. Use zip release instead
# https://developer.hashicorp.com/terraform/cli/install/apt#supported-architectures
# https://github.com/hashicorp/terraform/issues/27378
sudo apt update && sudo apt install -y zip
terraform_archive="terraform_1.3.6_linux_arm64.zip"
terraform_url="https://releases.hashicorp.com/terraform/1.3.6/$terraform_archive"
curl $terraform_url -o "$temp_dir/$terraform_archive"

# Unzip terraform directly to /usr/local/bin
sudo unzip "$temp_dir/$terraform_archive" -d /usr/local/bin

echo "Terraform install complete"
terraform version 

sudo apt update && sudo apt install -y gcc 

echo "GCC installed"
gcc --version

echo "Install NodeJS"
curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - &&\
sudo apt-get install -y nodejs

echo "NodeJS installed"
node --version

echo "Install docker"
sudo apt-get update
sudo apt-get install -y \
    ca-certificates \
    curl \
    gnupg \
    lsb-release
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli 

echo "Docker installed"
sudo systemctl start docker

wait_count=1
while true; do
    if [ $wait_count -gt 30 ]; then
        echo "Wait count expired"
        exit 1
    fi
    wait_count=$wait_count+1
    if [ "$(systemctl is-active docker)" == "active" ]; then\
        echo "Docker is active"
        break
    fi
    echo "Docker is not active. Waiting ($wait_count)..."

    sleep 1
done
sudo docker version 

sudo apt update
sudo apt install -y software-properties-common 
sudo add-apt-repository -y ppa:deadsnakes/ppa
sudo apt update
sudo apt install -y python3 python3-distutils python3-dev

python3 --version

sudo apt update && sudo apt install -y curl
curl -L https://azurecliprod.blob.core.windows.net/install.py -o $temp_dir/install-az.py 
printf "\n\n" | python3 $temp_dir/install-az.py 
echo "##vso[task.prependpath]/$HOME/bin"

echo "az installed"
"$HOME/bin/az" version

echo "Pre-reqs installed"
