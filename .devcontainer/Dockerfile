ARG VARIANT=bullseye
FROM mcr.microsoft.com/devcontainers/go

RUN export DEBIAN_FRONTEND=noninteractive \
     && apt-get update && apt-get install -y xdg-utils \
     && apt-get clean -y && rm -rf /var/lib/apt/lists/*
