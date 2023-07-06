ARG IMAGE=bullseye
FROM --platform=amd64 mcr.microsoft.com/devcontainers/${IMAGE}
RUN export DEBIAN_FRONTEND=noninteractive \
     && apt-get update && apt-get install -y xdg-utils \
     && apt-get update && apt-get install --yes --no-install-recommends postgresql-client \
     && apt-get clean -y && rm -rf /var/lib/apt/lists/*
