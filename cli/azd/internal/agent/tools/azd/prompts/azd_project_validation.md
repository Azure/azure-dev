# AZD Project Validation Tool

This tool validates an AZD project by programmatically running comprehensive checks on all components including azure.yaml schema validation, Bicep template validation, environment setup, packaging, and deployment preview.

## Purpose

This tool performs automated end-to-end validation of an AZD project to ensure all components are properly configured and the project is ready for deployment. The LLM should execute all validation steps directly using available tools and terminal commands, not just provide instructions to the user.

## Validation Workflow

The LLM must execute these validation steps programmatically using terminal commands and available tools:

### 1. Azure.yaml Schema Validation

**EXECUTE:** Use the `azd_yaml_schema` tool to validate the azure.yaml file against the official schema.

**Steps to Execute:**

- Check if `azure.yaml` exists in current directory using file system tools
- Run `azd_yaml_schema` tool to validate schema compliance
- Parse and report any schema violations or missing required fields
- Verify service definitions and configurations are correct

### 2. Bicep Template Validation

**EXECUTE:** Run the following commands to validate Bicep templates:

1. **Find Bicep Files:** Use file search to scan `./infra` directory for `.bicep` files
2. **Compile Templates:** Execute `az bicep build --file <file> --stdout` for each template
3. **Validate Syntax:** Ensure all templates compile without errors
4. **Check Dependencies:** Verify module references and parameter passing

**Commands to Execute:**

```powershell
# Compile main template
az bicep build --file ./infra/main.bicep

# Validate deployment (requires Azure CLI login)
az deployment sub validate --template-file ./infra/main.bicep --parameters ./infra/main.parameters.json --location <location>
```

### 3. AZD Environment Validation

**EXECUTE:** Run these commands to validate AZD environment setup:

1. **Check Environment Exists:** Execute `azd env list` to see available environments
2. **Create Environment if Missing:**
   - If no environments exist, execute `azd env new <directory-name>`
   - Use current directory name as environment name (sanitized)
3. **Verify Environment Selection:** Ensure an environment is currently selected

**Commands to Execute:**

```powershell
# List existing environments
azd env list

# Create new environment if none exist (replace <env-name> with directory name)
azd env new <env-name>

# Select environment if not already selected
azd env select <env-name>
```

### 4. Package Validation

**EXECUTE:** Run `azd package` to validate all services can be packaged successfully.

**Steps to Execute:**

- Execute `azd package` command
- Monitor output for errors or warnings
- Verify all service source paths are valid
- Check Docker builds complete successfully (for containerized services)
- Ensure all build artifacts are created
- Validate package manifests

**Command to Execute:**

```powershell
azd package
```

### 5. Deployment Preview Validation

**EXECUTE:** Run `azd provision --preview` to validate infrastructure deployment without actually creating resources.

**Steps to Execute:**

- Execute `azd provision --preview` command
- Monitor output for errors or warnings
- Verify Azure authentication is working
- Check resource group creation plan
- Validate all Bicep modules deploy correctly
- Ensure parameter values are properly resolved
- Confirm no deployment conflicts

**Command to Execute:**

```powershell
azd provision --preview
```

## Success Criteria

The LLM must verify that project validation is successful when all of the following are true:

- [ ] `azure.yaml` passes schema validation (executed via `azd_yaml_schema` tool)
- [ ] All Bicep templates compile without errors or warnings (verified via `az bicep build`)
- [ ] AZD environment exists and is properly configured (verified via `azd env list`)
- [ ] `azd package` completes without errors or warnings
- [ ] `azd provision --preview` completes without errors or warnings
- [ ] All service configurations are valid
- [ ] No missing dependencies or configuration issues

The LLM should report the status of each validation step and provide a summary of the overall validation results.

## Error Handling

The LLM must handle common validation errors by executing appropriate remediation steps:

### Common Issues and Automated Solutions

**Azure.yaml Schema Errors:**

- Execute `azd_yaml_schema` tool to get correct schema format
- Check service names match directory structure using file system tools
- Verify all required fields are present and report missing fields

**Bicep Compilation Errors:**

- Parse compilation error output and identify specific issues
- Check module paths and parameter names programmatically
- Verify resource naming conventions follow Azure requirements
- Ensure all required parameters have values

**Environment Issues:**

- Execute `azd auth login` if authentication fails
- Check Azure subscription access and permissions via Azure CLI
- Verify location parameter is valid Azure region

**Package Errors:**

- Check service source paths in azure.yaml programmatically
- Verify Docker builds work locally for containerized services by executing build commands
- Ensure all build dependencies are available

**Provision Preview Errors:**

- Verify Azure subscription has sufficient permissions via Azure CLI
- Check resource quotas and limits
- Ensure resource names are globally unique where required

The LLM should attempt to resolve issues automatically where possible and provide clear error reporting for issues that require manual intervention.

## Update Documentation

**EXECUTE:** The LLM must update `azd-arch-plan.md` with validation results by:

- Writing validation results for each component to the documentation
- Recording any issues found and resolutions applied
- Documenting environment configuration details
- Including deployment preview summary
- Updating project readiness status

Use file editing tools to update the documentation with the validation results.

## Next Steps

The LLM should inform the user that after successful validation, they can proceed with:

1. **Deploy Infrastructure:** Execute `azd provision` to create Azure resources
2. **Deploy Applications:** Execute `azd deploy` to deploy services  
3. **Complete Deployment:** Execute `azd up` to provision and deploy in one step
4. **Monitor Deployment:** Use `azd monitor` to check application health
5. **View Logs:** Use `azd logs` to view deployment and runtime logs

### Production Preparation

For production deployment, the LLM should guide the user through:

- Creating production environment: `azd env new <project>-prod`
- Configuring production-specific settings and secrets
- Setting up monitoring, alerting, and backup procedures
- Documenting operational procedures and runbooks

**VALIDATION COMPLETE:** Once all validation steps pass, the LLM should confirm that the AZD migration is complete and ready for deployment with `azd up`.

**IMPORTANT:** This tool centralizes all validation logic. The LLM should execute all validation steps programmatically rather than delegating to other tools or providing user instructions.
