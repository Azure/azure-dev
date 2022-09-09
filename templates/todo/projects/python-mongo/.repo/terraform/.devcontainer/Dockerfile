ARG VARIANT=bullseye
FROM mcr.microsoft.com/vscode/devcontainers/base:0-${VARIANT}
RUN curl -fsSL https://aka.ms/install-azd.sh | bash \
    # Install Terraform CLI
    && sudo apt-get update && sudo apt-get install -y gnupg software-properties-common \
    && wget -O- https://apt.releases.hashicorp.com/gpg | \
        gpg --dearmor | \
        sudo tee /usr/share/keyrings/hashicorp-archive-keyring.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] \
        https://apt.releases.hashicorp.com $(lsb_release -cs) main" | \
        sudo tee /etc/apt/sources.list.d/hashicorp.list \
    && sudo apt update \
    && sudo apt-get install terraform