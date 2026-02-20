# Azure Developer CLI - Common Provisioning Errors

This document provides solutions for common errors encountered during `azd provision` based on telemetry data.

## Azure Service Unavailable Error

**Error Pattern:** 
- May contain: `ServiceUnavailable`
- Contains: `"Sorry, we are currently experiencing high demand in [region name] region for the zonal redundant (Availability Zones)"`
- May contain: `"cannot fulfill your request at this time"`
- May contain: `"To request region access for your subscription, please follow this link"`

**LLM Instructions:**

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Identify the Problem:**
   - Extract the Azure region from the error message (e.g., "East US")
   - Confirm it's a provisioning failure due to regional capacity

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   USE YOUR TERMINAL TOOL to change to a different region:
   
   `azd env set AZURE_LOCATION <location>`
   
   USE YOUR TERMINAL TOOL to verify the change:
   
   `azd env get-values`

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

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following az commands - do not just display them to the user.

1. **Check Current Permissions (USE TERMINAL TOOL TO EXECUTE):**
   
   First, extract the user email/object-id and subscription-id from the error message.
   Then USE YOUR TERMINAL TOOL to execute this command:
   
   `az role assignment list --assignee <extracted-email-or-object-id> --output table`

2. **Attempt to Grant Permissions (USE TERMINAL TOOL TO EXECUTE):**

   USE YOUR TERMINAL TOOL to execute this command to grant Owner role:
   
   `az role assignment create --assignee <extracted-email-or-object-id> --role Owner --scope /subscriptions/<extracted-subscription-id>`
   
   If the above command fails with permission error, USE YOUR TERMINAL TOOL to try User Access Administrator:
   
   `az role assignment create --assignee <extracted-email-or-object-id> --role "User Access Administrator" --scope /subscriptions/<extracted-subscription-id>`
   
   If both commands fail with permission denied, inform the user they need to contact their Azure subscription administrator.

3. **Verify Permissions (USE TERMINAL TOOL TO EXECUTE):**
   
   After granting, USE YOUR TERMINAL TOOL to verify:
   
   `az role assignment list --role Owner --assignee <extracted-email-or-object-id> --scope /subscriptions/<extracted-subscription-id>`
   
   If result is not empty [], permissions were granted successfully.

4. **Long-term Recommendations:**
   - Document required permissions in your project README
   - Consider using Azure Managed Identities where possible to reduce manual role assignments

5. **Verify Permissions Before Deployment:**
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

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Common Causes:**
   - Manual role assignments created before running the template
   - Infrastructure template generates duplicate role assignment GUIDs

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   **Step 1: Check Infrastructure Files for Duplicate Role Assignments**
   
   USE YOUR TERMINAL TOOL to search your `infra/` folder for duplicate role assignment definitions:
   
   `grep -r "Microsoft.Authorization/roleAssignments" infra/*.bicep infra/**/*.bicep`
   
   USE YOUR TERMINAL TOOL to look for duplicate guid() calls with same parameters:
   
   `grep -r "guid(" infra/*.bicep infra/**/*.bicep | grep roleAssignment`
   
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
- May contain: `ResourceOperationFailure`
- Contains: `"Subscriptions are restricted from provisioning in location"`
- May contain: `"Try again in a different location"`
- May contain: `"https://aka.ms/postgres-request-quota-increase"` or similar quota increase links
- May contain: `postgres`

**LLM Instructions:**

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Identify the Problem:**
   - Extract the restricted Azure region from the error message (e.g., "eastus")
   - Extract the resource type that is restricted (e.g., PostgreSQL Flexible Server)
   - Confirm it's a subscription-level regional restriction

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   **Option 1: Use a Different Region if Available Region is Known**
   
   USE YOUR TERMINAL TOOL to change to an unrestricted region:
   
   `azd env set AZURE_LOCATION westus3`
   
   USE YOUR TERMINAL TOOL to verify the change:
   
   `azd env get-values`

   **Option 2: Check Available Regions for the Resource Type (USE TERMINAL TOOL TO EXECUTE)**
   
   USE YOUR TERMINAL TOOL to verify if regions support the resource in your subscription:
   
   `az postgres flexible-server list-skus --location westus3`
   
   USE YOUR TERMINAL TOOL to check on offer restriction (Disabled means location can be used):
   
   `az postgres flexible-server list-skus --location westus3 --query "[0].supportedFeatures[?name=='OfferRestricted'].status" -o tsv`

   USE YOUR TERMINAL TOOL to check other available locations:
   
   `az account list-locations --output table`

3. **Long-term Solution (If you need the specific region):**

   **Request Quota Increase:**
   - Open the link provided in the error message (e.g., https://aka.ms/postgres-request-quota-increase)
   - Let user submit a support request to enable the region for your subscription

4. **Verify Region Access Before Deployment (USE TERMINAL TOOL TO EXECUTE):**

   USE YOUR TERMINAL TOOL to check whether resource provider is registered:
   
   `az provider show --namespace Microsoft.DBforPostgreSQL --query "registrationState"`
   
   USE YOUR TERMINAL TOOL to list available regions of the provider:
   
   `az provider show --namespace Microsoft.DBforPostgreSQL --query "resourceTypes[?resourceType=='flexibleServers'].locations"`

   USE YOUR TERMINAL TOOL to check whether location is available or not:
   
   `az postgres flexible-server list-skus --location <location> --query "[0].supportedFeatures[?name=='OfferRestricted'].status" -o tsv`

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
- May contain: `Unauthorized`
- Contains: `"Operation cannot be completed without additional quota"`
- Contains: `"Current Limit"` and quota details for VM families (e.g., "Basic VMs", "Standard DSv3 Family vCPUs")
- May contain: `"Current Usage:"` and `"Amount required for this deployment"`
- May contain: `"New Limit that you should request to enable this deployment"`

**LLM Instructions:**

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Identify the Problem:**
   - Extract the VM family/tier from the error message (e.g., "Basic VMs", "Standard DSv3 Family")
   - Extract the region if available
   - Note the current limit and required quota
   - Confirm it's a VM quota limitation

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   **Option 1: Check Current Quota Usage and Use New Location**
   
   USE YOUR TERMINAL TOOL to check VM quota of a specific region:
   
   `az vm list-usage --location <region> --output table`
   
   USE YOUR TERMINAL TOOL to look for the specific VM family mentioned in the error:
   
   `az vm list-usage --location <region> --output table | grep -i <VM-family-mention-in-error>`

   USE YOUR TERMINAL TOOL to set location to available regions:
   
   `azd env set AZURE_LOCATION westus`

   USE YOUR TERMINAL TOOL to check current environment settings:
   
   `azd env get-values`

   **Option 2: Use a Different VM SKU (Quick Fix)**
   
   If the error is for Basic VMs or a restricted tier, modify your infrastructure to use available VM families:
   
   - Locate the VM/resource definition in your `infra/` files
   - Change the SKU to an available tier (e.g., Standard_B2s, Standard_D2s_v3)

   USE YOUR TERMINAL TOOL to search for VM size/SKU definitions in your infrastructure:
   
   `grep -r "sku\|vmSize" infra/*.bicep`

   **Option 3: Request Quota Increase**
   
   If you need the specific VM family:
   
   - Open [Azure Portal](https://ms.portal.azure.com/) in browser 
   - Go to Azure Portal → Subscriptions → Usage + quotas
   - Search for the VM family from the error message
   - Click "Request increase" for the specific region

3. **Verify Quota Before Deployment (USE TERMINAL TOOL TO EXECUTE):**

   USE YOUR TERMINAL TOOL to check specific VM family quota (standardDSv3Family is VM family and can be replaced):
   
   `az vm list-usage --location <region> --query "[?contains(name.value, 'standardDSv3Family')]" --output table`

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
## Cognitive Services Account Provisioning State Invalid Error

**Error Pattern:**
- May contain: `AccountProvisioningStateInvalid`
- Contains: `"Call to Microsoft.CognitiveServices/accounts failed"`
- May contain: `"Account"` followed by resource path `"/subscriptions/.../resourceGroups/.../providers/Microsoft.CognitiveServices/accounts/..."`
- May contain: `"in state Accepted"`

**LLM Instructions:**

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Identify the Problem:**
   - Extract the Cognitive Services account name from the error message
   - Extract the resource group name from the error message
   - Note the current provisioning state (e.g., "Accepted")
   - This error occurs when a dependent operation is attempted before the Cognitive Services account finishes provisioning

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   **Step 1: Check Current Provisioning State**
   
   USE YOUR TERMINAL TOOL to check the current provisioning state of the account:
   
   `az cognitiveservices account show --name <account-name> --resource-group <resource-group> --query "properties.provisioningState" -o tsv`
   
   **Step 2: Wait for Provisioning to Complete**
   
   If the state is "Accepted", "Creating", or "Updating", the account is still being provisioned. 
   
   USE YOUR TERMINAL TOOL to wait and poll for the provisioning state to become "Succeeded":
   
   `az cognitiveservices account show --name <account-name> --resource-group <resource-group> --query "properties.provisioningState" -o tsv`
   
   Repeat this command every 60 seconds until the state changes to "Succeeded". Max retry is 3 times. 
   After 3 times, if the state is the same, tell users to retry after a couple minutes. 

3. **Infrastructure Fix for Future Deployments:**

   If this error occurs repeatedly, consider adding explicit dependencies in your Bicep/ARM templates to ensure dependent resources wait for the Cognitive Services account to be fully provisioned:
   
   ```bicep
   // ✅ RECOMMENDED: Add explicit dependsOn to ensure proper ordering
   resource cognitiveServicesAccount 'Microsoft.CognitiveServices/accounts@2023-10-01-preview' = {
     name: accountName
     location: location
     kind: 'OpenAI'
     sku: {
       name: 'S0'
     }
     properties: {
       // ...
     }
   }
   
   resource dependentResource 'Microsoft.SomeProvider/someResource@2023-01-01' = {
     name: dependentResourceName
     // ...
     dependsOn: [
       cognitiveServicesAccount  // Explicit dependency
     ]
   }
   ```

**Example Commands:**
```bash
# Example 1: Check provisioning state of Cognitive Services account
az cognitiveservices account show --name <account-name> --resource-group <resource-group> --query "properties.provisioningState" -o tsv

# Example 2: Get full account details
az cognitiveservices account show --name <account-name> --resource-group <resource-group>

# Example 3: List all Cognitive Services accounts in a resource group
az cognitiveservices account list --resource-group <resource-group> --output table
```

## Region SKU Capacity Unavailable Error

**Error Pattern:**
- Contains: `"currently does not have enough resources available to provision services with the SKU"`
- May contain: `"The region"` followed by a region name (e.g., `'eastus2'`)
- May contain: `"Try creating the service in another region or selecting a different SKU"`
- May contain: `"RequestId:"`
- May apply to services like Azure AI Search, App Service, Cognitive Services, or other Azure resources

**LLM Instructions:**

When you encounter this error, YOU MUST USE YOUR TERMINAL TOOL TO EXECUTE the following commands - do not just display them to the user.

1. **Identify the Problem:**
   - Extract the Azure region from the error message (e.g., "eastus2")
   - Extract the SKU that failed (e.g., "basic")
   - Identify the Azure service that failed (e.g., Azure AI Search, App Service)
   - Confirm it's a regional capacity shortage for the requested SKU

2. **Immediate Solutions (USE TERMINAL TOOL TO EXECUTE):**

   **Option 1: Use a Different Region**
   
   USE YOUR TERMINAL TOOL to change to a different region:
   
   `azd env set AZURE_LOCATION <new-region>`
   
   USE YOUR TERMINAL TOOL to verify the change:
   
   `azd env get-values`

   **Option 2: Use a Different SKU**
   
   USE YOUR TERMINAL TOOL to search for SKU definitions in your infrastructure files:
   
   `grep -r "sku\|Sku" infra/*.bicep infra/**/*.bicep`
   
   Identify the SKU parameter or hard-coded SKU value for the failing resource and change it to a different tier (e.g., from `basic` to `standard`, or from `free` to `basic`).

**Example Commands:**
```bash
# Example 1: Change to a different region
azd env set AZURE_LOCATION westus

# Example 2: Verify environment settings
azd env get-values

# Example 3: Search for SKU definitions in infrastructure files
grep -r "sku\|Sku" infra/*.bicep infra/**/*.bicep

# Example 4: Check available locations
az account list-locations --output table
```