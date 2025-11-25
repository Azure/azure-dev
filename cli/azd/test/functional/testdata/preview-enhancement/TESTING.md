# Manual Testing Guide for Property-Level Preview Changes

This guide explains how to manually test the enhanced `azd provision --preview` functionality.

## Test Setup

1. Navigate to this test directory:
   ```bash
   cd /tmp/test-azd-preview
   ```

2. Ensure you have the modified azd binary:
   ```bash
   cd /home/runner/work/azure-dev/azure-dev/cli/azd
   go build -o /tmp/azd
   ```

3. Use the test azd binary:
   ```bash
   export PATH=/tmp:$PATH
   ```

## Test Scenarios

### Scenario 1: Initial Deployment (Create Resources)

1. Initialize the environment:
   ```bash
   azd env new test-env
   ```

2. Set required environment variables (you'll be prompted if not set):
   ```bash
   azd env set AZURE_LOCATION eastus
   ```

3. Run preview (should show CREATE operations with property details):
   ```bash
   azd provision --preview
   ```

   **Expected Output:**
   - Should show the storage account being created
   - Should display property values that will be set (e.g., sku.name, minimumTlsVersion)
   - Property changes should be prefixed with `+` symbol in gray/white color

### Scenario 2: Modify Existing Resources

1. After first deployment, modify main.bicep to change the SKU:
   ```bicep
   sku: {
     name: 'Standard_GRS'  // Changed from Standard_LRS
   }
   ```

2. Run preview again:
   ```bash
   azd provision --preview
   ```

   **Expected Output:**
   - Should show MODIFY operation for the storage account
   - Should display property changes with:
     - `~` symbol in yellow for modified properties
     - Before and after values: `"Standard_LRS" => "Standard_GRS"`

### Scenario 3: Add New Properties

1. Modify main.bicep to add a new property:
   ```bicep
   properties: {
     minimumTlsVersion: 'TLS1_2'
     supportsHttpsTrafficOnly: true
     allowBlobPublicAccess: false  // New property
   }
   ```

2. Run preview:
   ```bash
   azd provision --preview
   ```

   **Expected Output:**
   - Should show MODIFY operation
   - New property should appear with `+` symbol
   - Existing properties that changed should show `~` symbol

## Verification Checklist

- [ ] Property changes are displayed under each resource
- [ ] Create operations show `+` symbol with property values
- [ ] Modify operations show `~` symbol with before/after values
- [ ] Delete operations show `-` symbol (if applicable)
- [ ] Colors are correctly applied (gray for create, yellow for modify, red for delete)
- [ ] Complex values (objects, arrays) are formatted as JSON
- [ ] Multiple property changes per resource are all displayed
- [ ] Output is properly indented and aligned

## Comparison with Azure Bicep what-if

To compare the output with native Bicep what-if:

```bash
cd infra
az deployment group what-if --resource-group <your-rg> --template-file main.bicep --parameters main.parameters.json
```

The azd output should now provide similar detail to the native what-if command.

## Notes

- Property-level details are only available with Bicep provider
- Terraform provider already shows plan details via `terraform plan`
- This feature requires Azure credentials and an active subscription
