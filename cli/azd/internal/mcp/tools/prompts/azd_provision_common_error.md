# Azure Developer CLI - Common Provisioning Errors

This document provides solutions for common errors encountered during `azd provision` based on telemetry data.

## Azure Service Unavailable Error

**Error Pattern:** 
- Contains: `"code":"ServiceUnavailable"`
- Contains: `"Sorry, we are currently experiencing high demand in [region name] region for the zonal redundant (Availability Zones)"`
- May contain: `"cannot fulfill your request at this time"`
- May contain: `"To request region access for your subscription, please follow this link"`

**LLM instructions:**

When you encounter this error:

1. **Identify the Problem:**
   - Extract the Azure region from the error message (e.g., "East US")
   - Confirm it's a provisioning failure due to regional capacity

2. **Immediate Solutions:**
   - Run `azd env set AZURE_LOCATION <alternative-region>` 
   - Recommended alternative regions based on user location

**Example Commands:**
```bash
# Change to a different region
azd env set AZURE_LOCATION westus

# Check current environment settings
azd env get-values
```

## Role Assignment Authorization Error

**Error Pattern:**
- Contains: `"Authorization failed for template resource"`
- Contains: `"of type 'Microsoft.Authorization/roleAssignments'"`
- Contains: `"does not have permission to perform action 'Microsoft.Authorization/roleAssignments/write' at scope"`
- May contain: `"The client"` followed by email or `"with object id"` followed by GUID
- Scope path may contain: `/subscriptions/[id]/resourceGroups/[name]/providers/[resource-provider]`

**LLM Instructions:**

When you encounter this error:

1. **Check Current Permissions:**
   ```bash
   # Check your current role assignments
   az role assignment list --assignee <your-email-or-service-principal-or-object-id> --output table
   
   # Check role assignments at subscription level
   az role assignment list --scope /subscriptions/<subscription-id> --assignee <your-email> --output table
   ```

2. **Immediate Solutions:**

   **Request Owner or User Access Administrator Role:**
   - Contact your Azure subscription administrator
   - Request either:
     - **Owner** role at the resource group or subscription level (full access including role assignments)
     - **User Access Administrator** role (specifically for managing role assignments)
   - Administrator can grant this using:
     ```bash
     # Grant Owner role at subscription level
     az role assignment create --assignee <your-email> --role Owner --scope /subscriptions/<subscription-id>
     
     # Or grant User Access Administrator role at subscription level
     az role assignment create --assignee <your-email> --role "User Access Administrator" --scope /subscriptions/<subscription-id>
     ```

3. **Long-term Recommendations:**
   - Document required permissions in your project README
   - Consider using Azure Managed Identities where possible to reduce manual role assignments

4. **Verify Permissions Before Deployment:**
   ```bash
   # Check Owner role at subscription level
   az role assignment list --role Owner --assignee <your-email> --scope /subscriptions/<subscription-id>

   # Or check User Access Administrator at subscription level
   az role assignment list --role "User Access Administrator" --assignee <your-email> --scope /subscriptions/<subscription-id>
   ```
   If the returned result is not an empty array, then permissions are added successfully. 

**Example Commands:**
```bash
# Example 1: Admin grants Owner role at subscription level
az role assignment create \
  --assignee <user-email> \
  --role Owner \
  --scope /subscriptions/<subscription-id>

# Example 2: Admin grants User Access Administrator at subscription level  
az role assignment create \
  --assignee <user-email> \
  --role "User Access Administrator" \
  --scope /subscriptions/<subscription-id>

# Example 3: Verify Owner role was granted successfully
az role assignment list \
  --role Owner \
  --assignee <user-email> \
  --scope /subscriptions/<subscription-id>
# If result is not empty [], the role is assigned

# Example 4: Verify User Access Administrator role was granted
az role assignment list \
  --role "User Access Administrator" \
  --assignee <user-email> \
  --scope /subscriptions/<subscription-id>
# If result is not empty [], the role is assigned

# Example 5: Check role assignments by object ID
az role assignment list \
  --assignee <user-email> \
  --output table
```

## Role Assignment Already Exists Error

**Error Pattern:**
- Contains: `"RoleAssignmentExists"` or `"The role assignment already exists"`
- Occurs during deployment when attempting to create a duplicate role assignment

**LLM Instructions:**

When you encounter this error:

1. **Common Causes:**
   - Manual role assignments created before running the template
   - Infrastructure template generates duplicate role assignment GUIDs

2. **Immediate Solutions:**

   **Step 1: Check Infrastructure Files for Duplicate Role Assignments**
   
   Search your `infra/` folder for duplicate role assignment definitions:
   
   ```bash
   # Check Bicep files for role assignments
   grep -r "Microsoft.Authorization/roleAssignments" infra/*.bicep infra/**/*.bicep
   
   # Look for duplicate guid() calls with same parameters
   grep -r "guid(" infra/*.bicep infra/**/*.bicep | grep roleAssignment
   ```
   
   **Step 2: Fix Infrastructure Code (if duplicates found)**
   
   If you found duplicate role assignments in Step 1, remove one of the duplicated role assignments:
   
   ```bicep
   // ❌ AVOID: Creating the same role assignment in multiple files
   // File: infra/main.bicep
   resource apiCosmosRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2024-05-15' = {
    name: '${cosmosAccountName}/${guid(apiPrincipalId, cosmosAccountName, '00000000-0000-0000-0000-000000000002')}'
  
     // ...
   }
   
   // File: infra/app/container.bicep (DUPLICATE!)
   resource apiCosmosRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2024-05-15' = {
    name: '${cosmosAccountName}/${guid(apiPrincipalId, cosmosAccountName, '00000000-0000-0000-0000-000000000002')}' // Same GUID!
     // ...
   }
   ```

## Location Offer Restricted Error for Postgres

**Error Pattern:**
- Contains: `"code":"ResourceOperationFailure"`
- Contains: `"Subscriptions are restricted from provisioning in location"`
- May contain: `"Try again in a different location"`
- May contain: `"https://aka.ms/postgres-request-quota-increase"` or similar quota increase links
- May contain: `postgres`

**LLM Instructions:**

When you encounter this error:

1. **Identify the Problem:**
   - Extract the restricted Azure region from the error message (e.g., "eastus")
   - Extract the resource type that is restricted (e.g., PostgreSQL Flexible Server)
   - Confirm it's a subscription-level regional restriction

2. **Immediate Solutions:**

   **Option 1: Use a Different Region if Available Region is Known**
   
   Change to an unrestricted region:
   ```bash
   # Set a different region
   azd env set AZURE_LOCATION westus3
   
   # Verify the change
   azd env get-values
   ```

   **Option 2: Check Available Regions for the Resource Type**
   
   Verify if regions support the resource in your subscription:
   ```bash
   # For PostgreSQL Flexible Server
   az postgres flexible-server list-skus --location westus3
   
   # Check on offer restriction. Disabled means location can be used
   az postgres flexible-server list-skus --location westus3 --query "[0].supportedFeatures[?name=='OfferRestricted'].status" -o tsv

   # Check other locations
   az account list-locations --output table
   ```

3. **Long-term Solution (If you need the specific region):**

   **Request Quota Increase:**
   - Open the link provided in the error message (e.g., https://aka.ms/postgres-request-quota-increase)
   - Let user submit a support request to enable the region for your subscription

4. **Verify Region Access Before Deployment:**
   ```bash
   # Check whether resource provider is registered
   az provider show --namespace Microsoft.DBforPostgreSQL --query "registrationState"
   
   # List available regions of the provider
   az provider show --namespace Microsoft.DBforPostgreSQL --query "resourceTypes[?resourceType=='flexibleServers'].locations"

   # Check whether location is available or not
   az postgres flexible-server list-skus --location <location> --query "[0].supportedFeatures[?name=='OfferRestricted'].status" -o tsv
   ```

**Example Commands:**
```bash
# Example 1: Change region
azd env set AZURE_LOCATION westus3

# Example 2: Check available skus in PostgreSQL regions
az postgres flexible-server list-skus --location westus3 --output table

# Example 3: Check multiple regions for availability
az postgres flexible-server list-skus --location westus3
az postgres flexible-server list-skus --location centralus
az postgres flexible-server list-skus --location westus

# Example 4: View current environment configuration
azd env get-values

# Example 5: Check resource provider registration
az provider show --namespace Microsoft.DBforPostgreSQL --query "registrationState"
az provider show --namespace Microsoft.DBforPostgreSQL --query "resourceTypes[?resourceType=='flexibleServers'].locations"
az postgres flexible-server list-skus --location westus3 --query "[0].supportedFeatures[?name=='OfferRestricted'].status" -o tsv
```

## VM Quota Exceeded Error

**Error Pattern:**
- Contains: `"code":"Unauthorized"`
- Contains: `"Operation cannot be completed without additional quota"`
- Contains: `"Current Limit"` and quota details for VM families (e.g., "Basic VMs", "Standard DSv3 Family vCPUs")
- May contain: `"Current Usage:"` and `"Amount required for this deployment"`
- May contain: `"New Limit that you should request to enable this deployment"`

**LLM Instructions:**

When you encounter this error:

1. **Identify the Problem:**
   - Extract the VM family/tier from the error message (e.g., "Basic VMs", "Standard DSv3 Family")
   - Extract the region if available
   - Note the current limit and required quota
   - Confirm it's a VM quota limitation

2. **Immediate Solutions:**

   **Option 1: Check Current Quota Usage and Use New Location**
   
   View your current quota limits and usage:
   ```bash
   # Check VM quota of a specific region
   az vm list-usage --location <region> --output table
   
   # Look for the specific VM family mentioned in the error
   az vm list-usage --location <region> --output table | grep -i <VM-family-mention-in-error>

   # Set location to available regions
   azd env set AZURE_LOCATION westus

   # Check current environment settings
   azd env get-values
   ```

   **Option 2: Use a Different VM SKU (Quick Fix)**
   
   If the error is for Basic VMs or a restricted tier, modify your infrastructure to use available VM families:
   
   - Locate the VM/resource definition in your `infra/` files
   - Change the SKU to an available tier (e.g., Standard_B2s, Standard_D2s_v3)

   ```bash
   # Search for VM size/SKU definitions in your infrastructure
   grep -r "sku\|vmSize" infra/*.bicep
   ```

   **Option 3: Request Quota Increase**
   
   If you need the specific VM family:
   
   - Open [Azure Portal](https://ms.portal.azure.com/) in browser 
   - Go to Azure Portal → Subscriptions → Usage + quotas
   - Search for the VM family from the error message
   - Click "Request increase" for the specific region

3. **Verify Quota Before Deployment:**
   ```bash
   # Check specific VM family quota - standardDSv3Family is VM family and can be replaced
   az vm list-usage --location <region> --query "[?contains(name.value, 'standardDSv3Family')]" --output table
   ```

**Example Commands:**
```bash
# Example 1: Check VM quota in a region (e.g eastus)
az vm list-usage --location eastus --output table

# Example 2: Check available VM sizes in a region (e.g eastus)
az vm list-sizes --location eastus --output table

# Example 3: Search for VM SKU in infrastructure files
grep -r "vmSize\|sku" infra/*.bicep infra/**/*.bicep

# Example 4: Check quota for specific VM family (e.g standardDSv3Family)
az vm list-usage --location eastus --query "[?contains(name.value, 'standardDSv3Family')]"
```
