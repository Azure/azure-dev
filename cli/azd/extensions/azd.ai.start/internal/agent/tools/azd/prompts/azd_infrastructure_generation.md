# AZD Infrastructure Generation Tool

This specialized tool generates Bicep infrastructure templates for Azure Developer CLI (AZD) projects.

## Overview

Generate modular Bicep templates following Azure security and operational best practices.

**IMPORTANT:** 
- Before starting, check if `azd-arch-plan.md` exists to understand architecture decisions
- **Use the `azd_iac_generation_rules` tool for complete IaC rules, naming conventions, and best practices**

## Success Criteria

- [ ] Complete Bicep template structure created in `./infra` directory
- [ ] All templates compile without errors (`az bicep build --file infra/main.bicep`)
- [ ] Infrastructure supports all services defined in `azure.yaml`
- [ ] Follows all rules from `azd_iac_generation_rules` tool
- [ ] Parameter files configured appropriately

## Requirements Analysis

**REQUIRED ACTIONS:**

1. **Review IaC Rules:** Use `azd_iac_generation_rules` tool to get complete file structure, naming conventions, and compliance requirements

2. **Analyze Infrastructure Needs:**
   - Map services from `azure.yaml` to required Azure resources
   - Identify shared resources (Log Analytics, Container Registry, Key Vault)
   - Determine connectivity and security requirements

3. **Service Infrastructure Mapping:**
   - **Container Apps:** Environment, Log Analytics, Container Registry, App Insights, Managed Identity
   - **App Service:** Service Plan, App Service, App Insights
   - **Functions:** Function App, Storage Account, App Insights
   - **Static Web Apps:** Static Web App resource
   - **Database:** SQL/CosmosDB/PostgreSQL with appropriate SKUs

## Generation Workflow

**REQUIRED ACTIONS:**

1. **Create Directory Structure:**
   Follow structure from `azd_iac_generation_rules` tool:
   ```
   ./infra/
   ├── main.bicep
   ├── main.parameters.json
   ├── modules/
   └── [additional files per rules]
   ```

2. **Generate Main Template:**
   - Use subscription-level scope (`targetScope = 'subscription'`)
   - Create resource group with proper tagging
   - Deploy modules conditionally based on service requirements
   - Follow naming conventions from IaC rules tool

3. **Generate Module Templates:**
   - Create focused modules for each service type
   - Use resource group scope for all modules
   - Accept standardized parameters (environmentName, location, tags)
   - Output connection information for applications

4. **Generate Parameter Files:**
   - Provide sensible defaults for all parameters
   - Use parameter references for environment-specific values
   - Include all required parameters from IaC rules

```
./infra/
├── main.bicep              # Primary deployment template
├── main.parameters.json    # Default parameters
├── modules/
│   ├── container-apps.bicep
│   ├── app-service.bicep
│   ├── functions.bicep
│   ├── database.bicep
│   ├── storage.bicep
│   ├── keyvault.bicep
│   └── monitoring.bicep
└── resources.bicep         # Shared resources
```

## Template Requirements

### Main Template (main.bicep)

**CRITICAL REQUIREMENTS:**

- Use `targetScope = 'subscription'`
- Accept standardized parameters: `environmentName`, `location`, `principalId`
- Include feature flags for conditional deployment (e.g., `deployDatabase`)
- Create resource group with proper tagging (`azd-env-name`, `azd-provisioned`)
- Call modules conditionally based on feature flags
- Output connection strings and service endpoints

### Module Templates

## Generate Infrastructure Files

**WORKFLOW REQUIREMENTS:**

1. **Create Directory Structure:**

   ```text
   ./infra/
   ├── main.bicep
   ├── main.parameters.json
   ├── modules/
   └── [service-specific modules]
   ```

2. **Generate Main Template (main.bicep):**
   - Use `targetScope = 'subscription'`
   - Create resource group with proper tagging
   - Deploy modules conditionally based on service requirements

3. **Generate Module Templates:**
   - Create focused modules for each service type
   - Use standardized parameters (`environmentName`, `location`, `tags`)
   - Output connection information for applications

4. **Generate Parameter Files:**
   - Provide sensible defaults for all parameters
   - Use parameter references for environment-specific values

## Validation and Testing

**VALIDATION REQUIREMENTS:**

- All Bicep templates must compile without errors: `az bicep build --file infra/main.bicep`
- Validate deployment: `az deployment sub validate --template-file infra/main.bicep`
- Test with AZD: `azd provision --dry-run`
- Use existing tools for schema validation (reference `azd_yaml_schema` tool for azure.yaml validation)

## Update Documentation

**REQUIRED ACTIONS:**

Update `azd-arch-plan.md` with:

- List of generated infrastructure files
- Resource naming conventions used
- Security configurations implemented
- Parameter requirements
- Output variables available
- Validation results

## Next Steps

After infrastructure generation is complete:

1. Validate all templates compile successfully
2. Test deployment with `azd provision --dry-run`
3. Deploy with `azd provision` (creates resources)
4. Proceed to application deployment with `azd deploy`

**IMPORTANT:** Reference existing tools instead of duplicating functionality. For azure.yaml validation, use the `azd_yaml_schema` tool. For Bicep best practices, follow the AZD IaC Generation Rules document.
