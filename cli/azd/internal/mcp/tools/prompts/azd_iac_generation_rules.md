# Infrastructure as Code (IaC) Generation Rules

✅ **Agent Task List**  

1. Reference these rules when generating any IaC files
2. Follow file structure and organization requirements
3. Implement naming conventions and tagging strategies
4. Apply security and compliance best practices
5. Validate all generated code against these requirements

📄 **Required Outputs**  

- IaC files following all specified rules and conventions
- Proper file structure in `./infra` directory
- Compliance with Azure Well-Architected Framework principles
- Security best practices implemented
- Validation passing without errors

🧠 **Execution Guidelines**  

**File Structure and Organization:**

- **REQUIRED:** Place all IaC files in `./infra` folder
- **REQUIRED:** Name main deployment file `main.bicep`
- **REQUIRED:** Create `main.parameters.json` with parameter defaults
- **REQUIRED:** Main.bicep must use `targetScope = 'subscription'`
- **REQUIRED:** Create resource group as primary container
- **REQUIRED:** Pass resource group scope to all child modules
- **REQUIRED:** Create modular, reusable Bicep files

**Naming Conventions:**

- **REQUIRED:** Use pattern `{resourcePrefix}-{name}-{uniqueHash}`
- **REQUIRED:** Generate unique hash from environment name, subscription ID, and resource group name
- **EXAMPLE:** `app-myservice-h3x9k2` where `h3x9k2` is generated
- **FORBIDDEN:** Hard-code tenant IDs, subscription IDs, or resource group names

**Module Parameters (All modules must accept):**

- `name` (string): Base name for the resource
- `location` (string): Azure region for deployment  
- `tags` (object): Resource tags for governance
- **REQUIRED:** Modules use `targetScope = 'resourceGroup'`
- **REQUIRED:** Provide intelligent defaults for optional parameters
- **REQUIRED:** Use parameter decorators for validation

**Tagging Strategy:**

- **REQUIRED:** Tag resource groups with `azd-env-name: {environment-name}`
- **REQUIRED:** Tag hosting resources with `azd-service-name: {service-name}`
- **RECOMMENDED:** Include governance tags (cost center, owner, etc.)

**Security and Compliance:**

- **FORBIDDEN:** Hard-code secrets, connection strings, or sensitive values
- **REQUIRED:** Use latest API versions and schema for all bicep resource types using available tools
- **REQUIRED:** Use Key Vault references for secrets
- **REQUIRED:** Enable diagnostic settings and logging where applicable
- **REQUIRED:** Follow principle of least privilege for managed identities
- **REQUIRED:** Follow Azure Well-Architected Framework principles

**Container Resource Specifications:**

- **REQUIRED:** Wrap partial CPU values in `json()` function (e.g., `json('0.5')` for 0.5 CPU cores)
- **REQUIRED:** Memory values should be strings with units (e.g., `'0.5Gi'`, `'1Gi'`, `'2Gi'`)
- **EXAMPLE:** Container Apps resource specification:

  ```bicep
  resources: {
    cpu: json('0.25')    // Correct: wrapped in json()
    memory: '0.5Gi'      // Correct: string with units
  }
  ```

**Supported Azure Services:**

**Primary Hosting Resources (Choose One):**

- **Azure Container Apps** (PREFERRED): Containerized applications, built-in scaling
- **Azure App Service:** Web applications and APIs, multiple runtime stacks
- **Azure Function Apps:** Serverless and event-driven workloads
- **Azure Static Web Apps:** Frontend applications, built-in CI/CD
- **Azure Kubernetes Service (AKS):** Complex containerized workloads

**Essential Supporting Resources (REQUIRED for most applications):**

- **Log Analytics Workspace:** Central logging and monitoring
- **Application Insights:** Application performance monitoring
- **Azure Key Vault:** Secure storage for secrets and certificates

**Conditional Resources (Include based on requirements):**

- Azure Container Registry (for container-based apps)
- Azure Service Bus (for messaging scenarios)
- Azure Cosmos DB (for NoSQL data storage)
- Azure SQL Database (for relational data storage)
- Azure Storage Account (for blob/file storage)
- Azure Cache for Redis (for caching scenarios)

**Main.bicep Structure Template:**

```bicep
targetScope = 'subscription'
@description('Name of the environment')
param environmentName string
@description('Location for all resources')
param location string
@description('Tags to apply to all resources')
param tags object = {}

// Generate unique suffix for resource names
var resourceSuffix = take(uniqueString(subscription().id, environmentName, location), 6)
var resourceGroupName = 'rg-${environmentName}-${resourceSuffix}'

// Create the resource group
resource resourceGroup 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: resourceGroupName
  location: location
  tags: union(tags, {
    'azd-env-name': environmentName
  })
}

// Example module deployment with resource group scope
module appService 'modules/app-service.bicep' = {
  name: 'app-service'
  scope: resourceGroup
  params: {
    name: 'myapp'
    location: location
    tags: tags
  }
}
```

**Child Module Structure Template:**

```bicep
targetScope = 'resourceGroup'
@description('Base name for all resources')
param name string
@description('Location for all resources')  
param location string = resourceGroup().location
@description('Tags to apply to all resources')
param tags object = {}

// Generate unique suffix for resource names
var resourceSuffix = take(uniqueString(subscription().id, resourceGroup().name, name), 6)
var resourceName = '${name}-${resourceSuffix}'

// Resource definitions here...
```

📌 **Completion Checklist**  

- [ ] All files placed in `./infra` folder with correct structure
- [ ] `main.bicep` exists with subscription scope and resource group creation
- [ ] `main.parameters.json` exists with parameter defaults
- [ ] All child modules use `targetScope = 'resourceGroup'` and receive resource group scope
- [ ] Consistent naming convention applied: `{resourcePrefix}-{name}-{uniqueHash}`
- [ ] Required tags applied: `azd-env-name` and `azd-service-name`
- [ ] No hard-coded secrets, tenant IDs, or subscription IDs
- [ ] Parameters have appropriate validation decorators
- [ ] Security best practices followed (Key Vault, managed identities, diagnostics)
