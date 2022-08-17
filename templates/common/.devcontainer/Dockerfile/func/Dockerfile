ARG VARIANT=bullseye
FROM mcr.microsoft.com/vscode/devcontainers/base:0-${VARIANT}
RUN curl -fsSL https://aka.ms/install-azd.sh | bash \
    && curl https://packages.microsoft.com/keys/microsoft.asc | gpg --dearmor > microsoft.gpg \
    && mv microsoft.gpg /etc/apt/trusted.gpg.d/microsoft.gpg \
    && sh -c 'echo "deb [arch=amd64] https://packages.microsoft.com/debian/$(lsb_release -rs | cut -d'.' -f 1)/prod $(lsb_release -cs) main" > /etc/apt/sources.list.d/dotnetdev.list' \
    && apt-get update && apt-get install -y azure-functions-core-tools-4