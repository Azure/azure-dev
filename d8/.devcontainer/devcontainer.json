{
  "image": "mcr.microsoft.com/vscode/devcontainers/typescript-node:dev-20-bullseye",
  "features": {
    "ghcr.io/devcontainers-contrib/features/turborepo-npm:1": {},
    "ghcr.io/azure/azure-dev/azd:latest": {},
    "ghcr.io/devcontainers/features/docker-in-docker:1": {
      "version": "20.10.23",
      "moby": "false",
      "dockerDashComposeVersion": "v2"
    },
    "ghcr.io/devcontainers/features/azure-cli:1": {
      "version": "latest",
      "installBicep": true
    },
    "ghcr.io/devcontainers/features/github-cli:1": {
      "version": "latest"
    },
    "ghcr.io/devcontainers-contrib/features/typescript:2": {}
  },
  "customizations": {
    "codespaces": {
      "openFiles": [
        "README.md"
      ]
    },
    "vscode": {
      "extensions": [
        "ms-vscode.typescript-language-features",
        "esbenp.prettier-vscode",
        "ms-azuretools.vscode-bicep",
        "ms-azuretools.vscode-azurecontainerapps",
        "ms-azuretools.vscode-docker",
        "dbaeumer.vscode-eslint",
        "esbenp.prettier-vscode",
        "ms-azuretools.azure-dev",
        "GitHub.vscode-pull-request-github",
        "EditorConfig.EditorConfig",
        "GitHub.copilot"
      ]
    }
  },
  "portsAttributes": {
    "3000": {
      "label": "Next.js",
      "onAutoForward": "notify"
    }
  },
  "forwardPorts": [
    3000
  ],
  "postCreateCommand": "npm install"
}