# Infrastructure as Code (IaC) Generation Rules

**TASK:** Provide authoritative rules and standards for generating Bicep infrastructure templates that follow Azure Well-Architected Framework principles and AZD conventions.

**SUCCESS CRITERIA:**

- IaC files follow all specified rules and conventions
- Proper file structure in `./infra` directory with modular organization
- Compliance with Azure Well-Architected Framework principles
- Security best practices implemented throughout
- Latest Bicep schema versions used for all resource types

**VALIDATION REQUIRED:**

- All generated Bicep templates compile without errors or warnings
- Naming conventions follow required patterns with unique hash generation
- Security requirements are met (no hardcoded secrets, proper permissions)
- Resource specifications follow container and service requirements
- Tagging strategy is properly implemented

**COMPLETION CHECKLIST:**

- [ ] Use these rules when generating any IaC files
- [ ] Follow file structure and organization requirements
- [ ] Implement naming conventions and tagging strategies
- [ ] Apply security and compliance best practices
- [ ] Validate all generated code against these requirements

## Critical IaC Standards

**File Structure Requirements:**

- **REQUIRED**: All IaC files in `./infra` folder
- **REQUIRED**: Modular, reusable Bicep files with `targetScope = 'resourceGroup'`
- **REQUIRED**: Main deployment file named `main.bicep` with `targetScope = 'subscription'` and references all required modules
- **REQUIRED**: `main.parameters.json` referencing environment variables

**Naming Conventions:**

- **REQUIRED**: Pattern `{resourcePrefix}-{name}-{uniqueHash}`
- **REQUIRED**: Use naming conventions compatible for each Azure resource type
- **REQUIRED**: Generate unique hash from environment name, subscription ID, and resource group name
- **FORBIDDEN**: Hard-code tenant IDs, subscription IDs, or resource group names

**Security and Compliance:**

- **FORBIDDEN**: Hard-code secrets, connection strings, or sensitive values
- **REQUIRED**: Use latest API versions and schemas for all resource types
- **REQUIRED**: Key Vault references for secrets
- **REQUIRED**: Diagnostic settings and logging where applicable
- **REQUIRED**: Principle of least privilege for managed identities

**Resource Specifications:**

- **REQUIRED**: Wrap partial CPU values in `json()` function (e.g., `json('0.5')`)
- **REQUIRED**: Memory values as strings with units (e.g., `'0.5Gi'`, `'1Gi'`)
- **REQUIRED**: Proper tagging with `azd-env-name` and `azd-service-name`

**Supported Azure Services:**

- **Primary Hosting**: Container Apps (preferred), App Service, Function Apps, Static Web Apps, AKS
- **Essential Supporting**: Log Analytics Workspace, Application Insights, Key Vault
- **Conditional**: Container Registry, Service Bus, Cosmos DB, SQL Database, Storage Account, Cache for Redis

**Example main.parameters.json:**

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "environmentName": {
      "value": "${AZURE_ENV_NAME}"
    },
    "location": {
      "value": "${AZURE_LOCATION}"
    },
    "principalId": {
      "value": "${AZURE_PRINCIPAL_ID}"
    }
  }
}
```
