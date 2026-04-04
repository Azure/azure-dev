---
format: azd-infra-gen/v1
description: Infrastructure resources for the demo application
---

# Resource Group

Creates the main resource group for the application.

- type: Microsoft.Resources/resourceGroups
- location: ${AZURE_LOCATION}
- name: rg-${AZURE_ENV_NAME}

# Static Web App

Hosts the frontend application. The azd-service-name tag links this
resource to the "app" service defined in azure.yaml.

- type: Microsoft.Web/staticSites
- location: ${AZURE_LOCATION}
- name: swa-${AZURE_ENV_NAME}
- sku: Free
- tags:
  - azd-service-name: app
  - environment: ${AZURE_ENV_NAME}
