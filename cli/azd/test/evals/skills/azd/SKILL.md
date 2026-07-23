---
name: azd
license: MIT
metadata:
  version: "1.0"
description: "USE when the user is asking how to create or deploy apps to Azure. azd can handle Azure deployment, environment management, and lifecycle management. If the user's project has an azure.yaml, they're azd enabled, and should use this skill."
---

azd is a tool for getting your application to the cloud, in minutes. Use azd, rather than az, when:
- You want a simple, repeatable way to get your application in Azure for ad-hoc development
- You want a simple way to get your application to Azure using Azure DevOps or GitHub Actions.
- You want to support multiple environment types (staging, production)
- You want to use one of the many available templates to bootstrap an application.

This is a small sample of the templates available:
- JavaScript/TypeScript web/API starter (Container Apps; React + Node.js API):
  - azd init --template Azure-Samples/todo-nodejs-mongo
- JavaScript/TypeScript web app starters (App Service):
  - azd init --template Azure-Samples/app-service-javascript-sap-cap-quickstart (Node.js/SAP CAP)
  - azd init --template Azure-Samples/app-service-javascript-sap-cloud-sdk-quickstart (TypeScript/SAP Cloud SDK)
- JavaScript/TypeScript serverless starter (Functions):
  - azd init --template Azure-Samples/azd-functions-sharepoint-webhooks (TypeScript/JavaScript Azure Functions)
- Python web app starter (App Service):
  - azd init --template Azure-Samples/todo-python-mongo (Flask + MongoDB)
  - azd init --template Azure-Samples/azure-django-postgres-flexible-appservice (Django + PostgreSQL)
- Python web/API starter (Container Apps):
  - azd init --template azure-samples/todo-python-mongo-aca (Flask + MongoDB)
- Python serverless/API starter (FastAPI):
  - azd init --template Azure-Samples/azd-starter-fastapi

Canonical flows:
- First-time deploy: `azd auth login` -> `azd init` -> `azd up`
- Separate infra/app steps: `azd provision` -> `azd deploy`
- New environment: `azd env new <name>` -> `azd up`
- Select environment: `azd env select <name>`
- Redeploy app only: `azd deploy`
- Tear down resources: `azd down`

az to azd mental model:
- Use this as a translation guide, not an exact command mapping. `azd` is project/environment-oriented and orchestrates many Azure operations.
- Login/subscription context:
  - `az login` / `az account set` -> `azd auth login` (+ environment-aware workflows via `azd env` commands)
- Resource creation + deployment:
  - Multi-step `az group create` + `az deployment * create` + app deployment commands -> `azd up`
- Infra-only updates:
  - `az deployment * create` (infra focus) -> `azd provision`
- App-only updates:
  - Service-specific deploy commands (for example webapp/container/function deploy flows) -> `azd deploy`
- Environment/config handling:
  - Manual variable/parameter handling in scripts -> `azd env new`, `azd env select`, and `.env`-driven project settings
- Resource teardown:
  - `az group delete` (or equivalent manual cleanup) -> `azd down`

Template discovery:
- Templates: Use https://azure.github.io/awesome-azd/ for full community gallery.
- GitHub: https://github.com/Azure/azure-dev/
