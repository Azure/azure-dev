# AZD Error Troubleshooting and Resolution Instructions

✅ **Agent Task List**  

1. **Error Classification:** Identify the specific error type (Azure REST API, ARM Deployment, Authentication, Local Tool Installation or General)
2. **Error Analysis:** Explain and diagnose what the error means and its root causes. Note that this error occurs when running Azure Developer CLI
3. **Troubleshooting Steps:** Based on error type (Azure REST API Response Errors, Azure ARM Deployment Errors, Azure Authentication Errors, Local Tool Installation Errors, and General AZD Errors), find the Troubleshooting Approach below and provide troubleshooting approach
4. **Resolution Confirmation:** Ensure the issue is fully resolved. If issue still exists, retry the task list to fix the error

📄 **Required Outputs**  

- Clear error explanation and root cause analysis where error explanation will be under a section of "What's happening" and root cause analysis under a section of  "Why it's happening"
- Provide troubleshooting steps under a section of "How to fix it":
   - Step-by-step troubleshooting instructions 
   - Specific infrastructure code fixes for Bicep or Terraform files based on user usage if needed
   - Azure Portal navigation instructions for verification
   - Azure CLI commands for validation and testing if needed when user installed Azure CLI
   - Actionable next steps for resolution

🧠 **Execution Guidelines**  

## Azure REST API Response Errors

**Error Pattern:** HTTP status codes (400, 401, 403, 404, 429, 500, etc.) with Azure error codes

**Troubleshooting Approach:**

1. **Error Analysis**
   - Decode the HTTP status code meaning
   - Interpret the Azure-specific error code
   - Identify affected Azure resource or service

2. **Manual Troubleshooting Steps**
   - Provide manual Troubleshooting Steps for Azure Portal
   - Check Azure Portal for resource status
   - Verify resource quotas and limits
   - Review subscription and resource group permissions if error related
   - Validate resource naming conventions and conflicts if error related

3. **If user installed Azure CLI, Azure CLI Troubleshooting Steps. Otherwise use azure portal instructions**
   - Generate Azure CLI related commands if needed
   - Consider using following commands if fits:
   ```bash
   # Check subscription and tenant
   az account show
   az account list
   
   # Verify resource group permissions
   az role assignment list --resource-group <rg-name>
   
   # Check quota usage
   az vm list-usage --location <location>
   az network list-usages --location <location>
   ```

4. **Infrastructure Code Fixes**
   - **Bicep Files:** Correct bicep files based on error root cause
   - **Terraform Files:** Correct terraform files based on error root cause
   - Update parameter files with valid values

5. **Verification Commands if user installed Azure CLI. Otherwise skip this part**
   - Consider using following commands if fits:
   ```bash
   # Validate Bicep templates
   az bicep build --file main.bicep
   az deployment group validate --resource-group <rg> --template-file main.bicep
   
   # Validate Terraform configurations
   terraform validate
   terraform plan
   ```

## Azure ARM Deployment Errors

**Error Pattern:** Deployment validation failures, resource provisioning errors, template errors, etc

**Troubleshooting Approach:**

1. **Error Analysis**
   - Identify failing deployment operation
   - Locate specific resource causing failure
   - Review deployment validation messages

2. **Manual Troubleshooting Steps**
   - Navigate to Azure Portal → Resource Groups → Deployments
   - Review failed deployment details and error messages
   - Check resource dependencies and prerequisite resources
   - Verify template parameter values

3. **If user installed Azure CLI, Azure CLI Troubleshooting Steps. Otherwise use azure portal instructions**
   - Consider using following commands if fits:
   ```bash
   # List recent deployments
   az deployment group list --resource-group <rg-name>
   
   # Get deployment details
   az deployment group show --name <deployment-name> --resource-group <rg-name>
   
   # Check deployment operations
   az deployment operation group list --name <deployment-name> --resource-group <rg-name>
   ```

4. **Infrastructure Code Fixes**
   - **Bicep Files:** Correct bicep files based on error root cause
   - **Terraform Files:** Correct terraform files based on error root cause
   - Update parameter files with valid values

5. **Verification Commands if user installed Azure CLI. Otherwise skip this part**
   - Consider using following commands if fits:
   ```bash
   # Test deployment in validate-only mode
   az deployment group validate --resource-group <rg> --template-file main.bicep --parameters @parameters.json
   
   # Deploy with what-if analysis
   az deployment group what-if --resource-group <rg> --template-file main.bicep --parameters @parameters.json
   ```

## Azure Authentication Errors

**Error Pattern:** Authentication failures, token expiration, permission denied, tenant/subscription issues, etc

**Troubleshooting Approach:**

1. **Error Analysis**
   - Identify authentication method in use (device code, service principal, managed identity, interactive)
   - Determine if issue is token expiration, insufficient permissions, or configuration

2. **Manual Troubleshooting Steps**
   - Check Azure Portal → Azure Active Directory → Users/Service Principals
   - Verify subscription access and role assignments
   - Review tenant and subscription IDs

3. **AZD Authentication Commands**
   - Consider using following commands if fits:
   ```bash
   # Clear current authentication
   azd auth logout
   
   # Re-authenticate with device code
   azd auth login
   
   # Login with specific tenant
   azd auth login --tenant-id <tenant-id>
   
   # Check current authentication status
   azd auth login --check-status
   ```

4. **Environment Variable Verification**
   - Check Azure-related environment variables in .azure folder

## Local Tool Installation Errors

**Error Pattern:** Missing or incorrectly installed local development tools (Docker, Node.js, Python, .NET, etc.)

**Troubleshooting Approach:**

1. **Error Analysis**
   - Identify which local tool is missing or misconfigured
   - Determine if it's a PATH issue, version incompatibility, or complete absence
   - Check if tool is required for specific service in azure.yaml

2. **Manual Troubleshooting Steps**
   - Verify tool installation by checking system PATH
   - Check installed version against azd requirements
   - Review azure.yaml for specific tool version requirements
   - Validate tool configuration and permissions

3. **Tool-Specific Installation and Verification**
   
   **Docker:**
   ```bash
   # Check Docker installation
   docker --version
   docker info
   
   # Verify Docker daemon is running
   docker ps
   ```
   - Windows: Install Docker Desktop from docker.com
   - macOS: Install Docker Desktop from docker.com  
   - Linux: Follow distribution-specific Docker installation guide

   **Node.js and npm:**
   ```bash
   # Check Node.js installation
   node --version
   npm --version
   ```
   - Download from nodejs.org (LTS version recommended)
   - Verify npm is included with Node.js installation

   **Python:**
   ```bash
   # Check Python installation
   python --version
   python3 --version
   pip --version
   ```
   - Download from python.org (3.8+ recommended)
   - Ensure pip is installed and updated

   **.NET:**
   ```bash
   # Check .NET installation
   dotnet --version
   dotnet --list-sdks
   ```
   - Download from dotnet.microsoft.com
   - Install appropriate SDK version for your project

   **Git:**
   ```bash
   # Check Git installation
   git --version
   ```
   - Download from git-scm.com
   - Configure user name and email after installation

4. **PATH and Environment Configuration**
   ```bash
   # Check PATH environment variable
   echo $PATH  # Linux/macOS
   echo %PATH% # Windows
   ```

5. **Tool Version Compatibility Verification**
   - Check azd documentation for minimum supported versions
   - Update tools to compatible versions if needed
   - Verify tool integration with azd project requirements

6. **Post-Installation Verification**
   - If the error occurs after running command `azd provision`: 
   ```bash
   # Test azd provision with preview
   azd provision --preview
   ```
## General AZD Errors

**Error Pattern:** Miscellaneous errors not falling into above categories

**Troubleshooting Approach:**

1. **Error Analysis**
   - Review error message for specific component failure
   - Identify and diagnose the error
   - Provide solution based on error analysis

2. **Common Resolution Patterns**

- **Quota Exceeded:** Request quota increase in Azure Portal
- **Permission Denied:** Add required role assignments through Azure Portal or through Azure CLI if needed when user installed Azure CLI
- **Resource Name Conflicts:** Update names in Bicep or Terraform files with unique suffixes
- **API Version Issues:** Update to latest stable API versions in templates
- **Location Constraints:** Verify service availability in target Azure region
- **Other errors:** Call related tool to fix the error

📌 **Completion Checklist**  

- [ ] Error message clearly understood and root cause identified
- [ ] Appropriate troubleshooting steps executed successfully  
- [ ] Infrastructure code corrections implemented and validated if needed
- [ ] For Azure REST API Response Errors or Azure ARM Deployment Errors, Azure Portal verification completed for affected resources if needed
- [ ] For Azure REST API Response Errors or Azure ARM Deployment Errors, Azure CLI commands confirm successful resolution if needed when user installed Azure CLI. Otherwise, skip this step
- [ ] Ensure the issue is fully resolved