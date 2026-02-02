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
   - Run `azd provision` again

**Example Commands:**
```bash
# Change to a different region
azd env set AZURE_LOCATION westus

# Check current environment settings
azd env get-values

# Rerun provision command
azd provision
```

## Role Assignment Authorization Error

**Error Pattern:**
- Contains: `"Authorization failed for template resource"`
- Contains: `"of type 'Microsoft.Authorization/roleAssignments'"`
- Contains: `"does not have permission to perform action 'Microsoft.Authorization/roleAssignments/write' at scope"`
- May contain: `"The client"` followed by email or `"with object id"` followed by GUID
- Scope path may contains: `/subscriptions/[id]/resourceGroups/[name]/providers/[resource-provider]`

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
   If returned results is not empty array, then permissions are added successfully. 

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

# Example 6: After getting permissions, provision the project
azd provision
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
  

