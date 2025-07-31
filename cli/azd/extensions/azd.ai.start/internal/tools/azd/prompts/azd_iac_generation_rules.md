# Infrastructure as Code (IaC) Generation Rules for Azure Developer CLI (AZD)

This document provides comprehensive rules and guidelines for generating Bicep Infrastructure as Code files and modules for AZD projects. Follow these rules strictly when generating Azure infrastructure code.

## Core Generation Rules

### File Structure and Organization

- **REQUIRED**: Place all IaC files in the `./infra` folder within an AZD project
- **REQUIRED**: Name the main deployment file `main.bicep` - this is the primary deployment target
- **REQUIRED**: The root level `main.bicep` must be a subscription level deployment using `targetScope = 'subscription'`
- **REQUIRED**: The main.bicep file must create a resource group as the primary container for all resources
- **REQUIRED**: Pass the resource group scope to all child modules that deploy resources
- **REQUIRED**: Create modular, reusable Bicep files instead of monolithic templates
- **RECOMMENDED**: Organize modules by resource type or logical grouping

### Azure Best Practices Compliance

- **REQUIRED**: Follow Azure Well-Architected Framework principles
- **REQUIRED**: Use Bicep best practices including proper parameter validation and resource dependencies
- **REQUIRED**: Leverage Azure Verified Modules (AVM) when available - always check for existing AVM modules before creating custom ones
- **REQUIRED**: Implement least-privilege access principles

### Naming Conventions

- **REQUIRED**: Use consistent naming pattern: `{resourcePrefix}-{name}-{uniqueHash}`
- **REQUIRED**: Generate unique hash using combination of environment name, subscription ID, and resource group name
- **EXAMPLE**: `app-myservice-h3x9k2` where `h3x9k2` is generated from env/subscription/rg
- **FORBIDDEN**: Hard-code tenant IDs, subscription IDs, or resource group names

### Module Parameters

- **REQUIRED**: Every module must accept these standard parameters:
  - `name` (string): Base name for the resource
  - `location` (string): Azure region for deployment
  - `tags` (object): Resource tags for governance
- **REQUIRED**: Modules that deploy Azure resources must use `targetScope = 'resourceGroup'` and be called with the resource group scope from main.bicep
- **REQUIRED**: Provide intelligent defaults for optional parameters
- **REQUIRED**: Use parameter decorators for validation (e.g., `@minLength`, `@allowed`)
- **RECOMMENDED**: Group related parameters using objects when appropriate

### Tagging Strategy

- **REQUIRED**: Tag resource groups with `azd-env-name: {environment-name}`
- **REQUIRED**: Tag hosting resources with `azd-service-name: {service-name}`
- **RECOMMENDED**: Include additional governance tags (cost center, owner, etc.)

### Security and Compliance

- **FORBIDDEN**: Hard-code secrets, connection strings, or sensitive values
- **REQUIRED**: Use Key Vault references for secrets
- **REQUIRED**: Enable diagnostic settings and logging where applicable
- **REQUIRED**: Follow principle of least privilege for managed identities

### Quality Assurance

- **REQUIRED**: Validate all generated Bicep code using Bicep CLI
- **REQUIRED**: Address all warnings and errors before considering code complete
- **REQUIRED**: Test deployment in a sandbox environment when possible

## Supported Azure Services

### Primary Hosting Resources (Choose One)

1. **Azure Container Apps** ⭐ **(PREFERRED)**
   - Best for containerized applications
   - Built-in scaling and networking
   - Supports both HTTP and background services

2. **Azure App Service**
   - Best for web applications and APIs
   - Supports multiple runtime stacks
   - Built-in CI/CD integration

3. **Azure Function Apps**
   - Best for serverless and event-driven workloads
   - Multiple hosting plans available
   - Trigger-based execution model

4. **Azure Static Web Apps**
   - Best for frontend applications
   - Built-in GitHub/Azure DevOps integration
   - Free tier available

5. **Azure Kubernetes Service (AKS)**
   - Best for complex containerized workloads
   - Full Kubernetes capabilities
   - Requires advanced configuration

### Essential Supporting Resources

**REQUIRED** - Include these resources in most AZD applications:

- **Log Analytics Workspace**
  - Central logging and monitoring
  - Required for Application Insights
  - Enable diagnostic settings for all resources

- **Application Insights**
  - Application performance monitoring
  - Dependency tracking and telemetry
  - Link to Log Analytics workspace

- **Azure Key Vault**
  - Secure storage for secrets, keys, and certificates
  - Use managed identity for access
  - Enable soft delete and purge protection

**CONDITIONAL** - Include based on application requirements:

- **Azure Container Registry** (for container-based apps)
- **Azure Service Bus** (for messaging scenarios)
- **Azure Cosmos DB** (for NoSQL data storage)
- **Azure SQL Database** (for relational data storage)
- **Azure Storage Account** (for blob/file storage)
- **Azure Cache for Redis** (for caching scenarios)

## Code Generation Examples

### Main.bicep Structure Template

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

### Child Module Structure Template

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

## Validation Checklist

Before completing code generation, verify:

- [ ] All files are in `./infra` folder
- [ ] `main.bicep` exists as primary deployment file with subscription scope
- [ ] Resource group is created in `main.bicep` and properly tagged
- [ ] All child modules use `targetScope = 'resourceGroup'` and receive resource group scope
- [ ] All resources use consistent naming convention
- [ ] Required tags are applied correctly
- [ ] No hard-coded secrets or identifiers
- [ ] Parameters have appropriate validation
- [ ] Bicep CLI validation passes without errors
- [ ] AVM modules are used where available
- [ ] Supporting resources are included as needed
- [ ] Security best practices are followed
