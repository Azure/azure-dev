# AZD Infrastructure Generation Instructions

✅ **Agent Task List**

1. **Inventory existing IaC files** - scan current working directory for all `.bicep` files
2. **Read application spec** to get the Infrastructure as Code File Checklist with required resources
3. **Get IaC generation rules** for Azure infrastructure best practices and coding standards
4. **Get latest bicep schema versions** for each Azure resource type from the required resources
5. Create directory structure in `./infra` following IaC rules
6. For each file in the IaC checklist:
   - **If file exists**: Intelligently update to match requirements, preserve user customizations where possible
   - **If file missing**: Generate new file following templates and best practices
   - **Flag conflicts**: Note any incompatible configurations but proceed with updates
7. **Generate modular infrastructure**: Create generic resource modules and service-specific modules that reference them
8. Validate all generated bicep templates compile without errors or warnings
9. Update the IaC checklist section in existing application spec by marking completed files as [x] while preserving existing content

📄 **Required Outputs**

- **Existing IaC inventory** documenting all current `.bicep` files found
- Complete Bicep template structure in `./infra` directory based on the IaC checklist
- All files listed in the Infrastructure as Code File Checklist from application spec (created or updated)
- Main.bicep file with subscription scope and modular deployment
- **Generic resource modules** for reusable Azure service patterns (e.g., `modules/containerapp.bicep`, `modules/storage.bicep`)
- **Service-specific modules** that reference generic modules with customized configurations (e.g., `modules/user-api.bicep`)
- Parameter files with sensible defaults using latest bicep schema versions
- **Conflict report** highlighting any incompatible configurations that were updated
- All templates validated and error-free
- Update existing application spec IaC checklist by marking completed files as [x] while preserving existing content

🧠 **Execution Guidelines**

**Inventory Existing IaC Files:**

- Scan current working directory recursively for all `.bicep` files
- Document existing files, their locations, and basic structure
- Note any existing modules, parameters, and resource definitions
- Identify which checklist files already exist vs. need to be created

**Read IaC Checklist and Get Generation Rules:**

- Read the "Infrastructure as Code File Checklist" section from application spec
- This checklist specifies exactly which Bicep files need to be generated and their purpose
- Get IaC generation rules for Azure infrastructure best practices and coding standards
- Get latest bicep schema versions for each Azure resource type identified in the checklist
- Cross-reference with existing file inventory to determine update vs. create strategy

**Modular Infrastructure Generation Strategy:**

**Generic Resource Modules:**

- Create reusable modules for common Azure resource patterns
- Examples: `modules/containerapp.bicep`, `modules/storage.bicep`, `modules/database.bicep`
- These modules accept parameters for customization but contain standard resource configurations
- Follow IaC generation rules for naming, security, and architectural patterns

**Service-Specific Modules:**

- Create modules that reference generic modules with service-specific configurations
- Examples: `modules/user-api.bicep` (calls `containerapp.bicep` with user-api settings)
- These modules provide the business logic and specific parameter values
- Map each service from architecture planning to its corresponding module

**Smart File Generation Strategy:**

**For Existing Files:**

- **Preserve user customizations**: Keep existing resource configurations, naming, and parameters where compatible
- **Add missing components**: Inject required modules, resources, or configurations that are missing
- **Update outdated patterns**: Modernize to use current best practices
- **Maintain functionality**: Ensure existing deployments continue to work

**For New Files:**

- Always identify and use the latest bicep schema version for all Azure resource types
- Create from templates following IaC generation rules
- Follow standard naming conventions and patterns

**Conflict Resolution:**

- **Document conflicts**: Log when existing configurations conflict with requirements
- **Prioritize functionality**: Make changes needed for AZD compatibility
- **Preserve intent**: Keep user's architectural decisions when possible
- **Flag major changes**: Clearly indicate significant modifications made

**Generate Files in Order:**

- Create `./infra/main.bicep` first (always required)
- Create `./infra/main.parameters.json` second (always required)
- Generate generic resource modules in `./infra/modules/` (e.g., `containerapp.bicep`, `storage.bicep`)
- Generate service-specific modules in `./infra/modules/` (e.g., `user-api.bicep`, `order-service.bicep`)
- Follow the exact file paths specified in the checklist from application spec

**Module Generation Workflow:**

1. **Identify required resource types** from the Infrastructure as Code File Checklist
2. **Create generic modules first** for each unique Azure resource type
3. **Create service-specific modules** that reference generic modules with specific configurations
4. **Ensure proper dependencies** between modules and main deployment template
5. **Apply IaC generation rules** consistently across all generated files

**Main Parameters File Requirements:**

The `./infra/main.parameters.json` file is critical for AZD integration and must follow this exact structure:

- All parameter values in main.parameters.json must use AZD environment variable expansion syntax (e.g.,  ${AZURE_LOCATION} ).
- No hard-coded values are allowed for parameters that can be set via environment variables.

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

**Key Features:**

- **Environment Variable Expansion**: Use `${VARIABLE_NAME}` syntax for dynamic values
- **Standard Parameters**: Always include `environmentName`, `location`, and `principalId`
- **AZD Integration**: These variables are automatically populated by AZD during deployment
- **Additional Parameters**: Add service-specific parameters as needed, using the same substitution pattern

**Service Infrastructure Mapping:**

- **Container Apps:** Environment, Log Analytics, Container Registry, App Insights, Managed Identity
- **App Service:** Service Plan, App Service, App Insights, Managed Identity
- **Functions:** Function App, Storage Account, App Insights, Managed Identity
- **Static Web Apps:** Static Web App resource with configuration
- **Database:** SQL/CosmosDB/PostgreSQL with appropriate SKUs and security

**Module Template Requirements:**

- Use `targetScope = 'resourceGroup'` for all modules
- Accept resource group scope from main template
- Use standardized parameters (name, location, tags)
- Follow naming convention: `{resourcePrefix}-{name}-{uniqueHash}`
- Output connection information for applications
- Include security best practices and monitoring

**Required Directory Structure:**

```text
./infra/
├── main.bicep                    # Primary deployment template (subscription scope)
├── main.parameters.json          # Default parameters
├── modules/
│   ├── containerapp.bicep        # Generic Container Apps module
│   ├── app-service.bicep         # Generic App Service module
│   ├── functions.bicep           # Generic Azure Functions module
│   ├── database.bicep            # Generic database module
│   ├── storage.bicep             # Generic storage module
│   ├── keyvault.bicep            # Generic Key Vault module
│   ├── monitoring.bicep          # Generic monitoring module
│   ├── user-api.bicep            # Service-specific module (references containerapp.bicep)
│   ├── order-service.bicep       # Service-specific module (references containerapp.bicep)
│   └── shared-storage.bicep      # Service-specific module (references storage.bicep)
└── resources.bicep               # Shared resources
```

**Main Template Requirements:**

- Use `targetScope = 'subscription'`
- Accept standardized parameters: `environmentName`, `location`, `principalId`
- Include feature flags for conditional deployment
- Create resource group with proper tagging (`azd-env-name`)
- Call modules conditionally based on service requirements
- Output connection strings and service endpoints

📌 **Completion Checklist**

- [ ] **Existing IaC inventory completed** - all `.bicep` files in current directory catalogued
- [ ] **Infrastructure as Code File Checklist read** from application spec
- [ ] **IaC generation rules retrieved** and applied to all generated files
- [ ] **Latest bicep schema versions obtained** for each Azure resource type
- [ ] **Update vs. create strategy determined** for each file in checklist
- [ ] **Generic resource modules created** for each unique Azure resource type
- [ ] **Service-specific modules generated** that reference generic modules appropriately
- [ ] **All files from checklist generated or updated** in the correct locations
- [ ] **User customizations preserved** where compatible with requirements
- [ ] **Conflicts documented** and resolved with functional priority
- [ ] Infrastructure directory structure created following IaC rules
- [ ] Main.bicep template created/updated with subscription scope and resource group
- [ ] Module templates generated/updated for all services listed in checklist
- [ ] Parameter files created/updated with appropriate defaults
- [ ] Naming conventions and tagging implemented correctly
- [ ] Security best practices implemented (Key Vault, managed identities)
- [ ] All Bicep modules are created with latest bicep schema version and meet Azure and Bicep best practices
- [ ] **Infrastructure as Code File Checklist in application spec updated** by marking completed files as [x] while preserving existing content
