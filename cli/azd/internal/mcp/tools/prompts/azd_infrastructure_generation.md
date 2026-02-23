# azd infrastructure Generation Instructions

✅ **Agent Task List**  

1. Strictly follow Azure and Bicep best practices in all code generation
2. Strictly follow azd IaC generation rules during all code generation
3. **Inventory existing IaC files** - scan current working directory for all `.bicep` files
4. Read `azd-arch-plan.md` to get the **IaC File Generation Checklist**
5. Create directory structure in `./infra` following IaC rules
6. During code generation always use the latest version for each resource type using the bicep schema tool
7. For each file in the IaC checklist:
   - **If file exists**: Intelligently update to match requirements, preserve user customizations where possible
   - **If file missing**: Generate new file following templates and best practices
   - **Flag conflicts**: Note any incompatible configurations but proceed with updates
8. Validate all generated bicep templates compile without errors or warnings
9. Update the IaC checklist section in existing `azd-arch-plan.md` by marking completed files as [x] while preserving existing content

📄 **Required Outputs**  

- **Existing IaC inventory** documenting all current `.bicep` files found
- Complete Bicep template structure in `./infra` directory based on the IaC checklist
- All files listed in the IaC File Generation Checklist from `azd-arch-plan.md` (created or updated)
- Main.bicep file with subscription scope and modular deployment
- Service-specific modules for each Azure service from the checklist
- Parameter files with sensible defaults
- **Conflict report** highlighting any incompatible configurations that were updated
- All templates validated and error-free
- Update existing `azd-arch-plan.md` IaC checklist by marking completed files as [x] while preserving existing content

🧠 **Execution Guidelines**  

**Use Tools:**

- Use azd IaC generation rules tool first to get complete file structure, naming conventions, and compliance requirements.
- Use Bicep Schema tool get get the latest API version and bicep schema for each resource type

**Inventory Existing IaC Files:**

- Scan current working directory recursively for all `.bicep` files
- Document existing files, their locations, and basic structure
- Note any existing modules, parameters, and resource definitions
- Identify which checklist files already exist vs. need to be created

**Read IaC Checklist:**

- Read the "Infrastructure as Code File Checklist" section from `azd-arch-plan.md`
- This checklist specifies exactly which Bicep files need to be generated
- Cross-reference with existing file inventory to determine update vs. create strategy

**Smart File Generation Strategy:**

**For Existing Files:**

- **Preserve user customizations**: Keep existing resource configurations, naming, and parameters where compatible
- **Add missing components**: Inject required modules, resources, or configurations that are missing
- **Update outdated patterns**: Modernize to use current best practices
- **Maintain functionality**: Ensure existing deployments continue to work

**For New Files:**

- Create from templates following IaC generation rules
- Follow standard naming conventions and patterns

**Conflict Resolution:**

- **Document conflicts**: Log when existing configurations conflict with requirements
- **Prioritize functionality**: Make changes needed for azd compatibility
- **Preserve intent**: Keep user's architectural decisions when possible
- **Flag major changes**: Clearly indicate significant modifications made

**Generate Files in Order:**

- Create `./infra/main.bicep` first (always required)
- Create `./infra/main.parameters.json` second (always required)
- Generate each module file listed in the checklist
- Follow the exact file paths specified in the checklist

**Main Parameters File Requirements:**

The `./infra/main.parameters.json` file is critical for azd integration and must follow this exact structure:

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

- **Environment Variable Substitution**: Uses `${VARIABLE_NAME}` syntax for dynamic values
- **Standard Parameters**: Always include `environmentName`, `location`, and `principalId`
- **azd integration**: These variables are automatically populated by azd during deployment
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
├── main.bicep              # Primary deployment template (subscription scope)
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

**Main Template Requirements:**

- Use `targetScope = 'subscription'`
- Accept standardized parameters: `environmentName`, `location`, `principalId`
- Include feature flags for conditional deployment
- Create resource group with proper tagging (`azd-env-name`)
- Call modules conditionally based on service requirements
- Output connection strings and service endpoints

📌 **Completion Checklist**  

- [ ] `azd_iac_generation_rules` tool referenced for complete compliance requirements
- [ ] **Existing IaC inventory completed** - all `.bicep` files in current directory catalogued
- [ ] **IaC File Generation Checklist read** from `azd-arch-plan.md`
- [ ] **Update vs. create strategy determined** for each file in checklist
- [ ] **All files from checklist generated or updated** in the correct locations
- [ ] **User customizations preserved** where compatible with requirements
- [ ] **Conflicts documented** and resolved with functional priority
- [ ] Infrastructure directory structure created following IaC rules
- [ ] Main.bicep template created/updated with subscription scope and resource group
- [ ] Module templates generated/updated for all services listed in checklist
- [ ] Parameter files created/updated with appropriate defaults
- [ ] Naming conventions and tagging implemented correctly
- [ ] Security best practices implemented (Key Vault, managed identities)
- [ ] **IaC checklist in `azd-arch-plan.md` updated** by marking completed files as [x] while preserving existing content

