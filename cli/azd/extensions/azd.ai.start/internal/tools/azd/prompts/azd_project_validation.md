# AZD Project Validation Tool

Validates an AZD project by running comprehensive checks on all components including azure.yaml schema validation, Bicep template validation, environment setup, packaging, and deployment preview.

## Purpose

This tool performs end-to-end validation of an AZD project to ensure all components are properly configured and the project is ready for deployment. It centralizes all validation logic to provide a single source of truth for project readiness.

## Validation Workflow

### 1. Azure.yaml Schema Validation

**REQUIRED ACTION:**
Use the `azd_yaml_schema` tool to validate the azure.yaml file against the official schema.

**Validation Steps:**

- Check if `azure.yaml` exists in current directory
- Validate schema compliance using `azd_yaml_schema` tool
- Report any schema violations or missing required fields
- Verify service definitions and configurations

### 2. Bicep Template Validation

**REQUIRED ACTIONS:**

1. **Find Bicep Files:** Scan `./infra` directory for `.bicep` files
2. **Compile Templates:** Run `az bicep build --file <file> --stdout` for each template
3. **Validate Syntax:** Ensure all templates compile without errors
4. **Check Dependencies:** Verify module references and parameter passing

**Commands to Run:**

```powershell
# Compile main template
az bicep build --file ./infra/main.bicep

# Validate deployment (requires Azure CLI login)
az deployment sub validate --template-file ./infra/main.bicep --parameters ./infra/main.parameters.json --location <location>
```

### 3. AZD Environment Validation

**REQUIRED ACTIONS:**

1. **Check Environment Exists:** Run `azd env list` to see available environments
2. **Create Environment if Missing:**
   - If no environments exist, run `azd env new <directory-name>`
   - Use current directory name as environment name (sanitized)
3. **Verify Environment Selection:** Ensure an environment is currently selected

**Commands to Run:**

```powershell
# List existing environments
azd env list

# Create new environment if none exist (replace <env-name> with directory name)
azd env new <env-name>

# Select environment if not already selected
azd env select <env-name>
```

### 4. Package Validation

**REQUIRED ACTION:**
Run `azd package` to validate all services can be packaged successfully.

**Validation Steps:**

- Verify all service source paths are valid
- Check Docker builds complete successfully (for containerized services)
- Ensure all build artifacts are created
- Validate package manifests

**Command to Run:**

```powershell
azd package
```

### 5. Deployment Preview Validation

**REQUIRED ACTION:**
Run `azd provision --preview` to validate infrastructure deployment without actually creating resources.

**Validation Steps:**

- Verify Azure authentication is working
- Check resource group creation plan
- Validate all Bicep modules deploy correctly
- Ensure parameter values are properly resolved
- Confirm no deployment conflicts

**Command to Run:**

```powershell
azd provision --preview
```

## Success Criteria

The project validation is successful when:

- [ ] `azure.yaml` passes schema validation
- [ ] All Bicep templates compile without errors or warnings
- [ ] AZD environment exists and is properly configured
- [ ] `azd package` completes  without errors or warnings
- [ ] `azd provision --preview` completes without errors or warnings
- [ ] All service configurations are valid
- [ ] No missing dependencies or configuration issues

## Error Handling

### Common Issues and Solutions

**Azure.yaml Schema Errors:**

- Use `azd_yaml_schema` tool to get correct schema format
- Check service names match directory structure
- Verify all required fields are present

**Bicep Compilation Errors:**

- Check module paths and parameter names
- Verify resource naming conventions follow Azure requirements
- Ensure all required parameters have values

**Environment Issues:**

- Run `azd auth login` if authentication fails
- Check Azure subscription access and permissions
- Verify location parameter is valid Azure region

**Package Errors:**

- Check service source paths in azure.yaml
- Verify Docker builds work locally for containerized services
- Ensure all build dependencies are available

**Provision Preview Errors:**

- Verify Azure subscription has sufficient permissions
- Check resource quotas and limits
- Ensure resource names are globally unique where required

## Update Documentation

**REQUIRED ACTIONS:**

Update `azd-arch-plan.md` with:

- Validation results for each component
- Any issues found and resolutions applied
- Environment configuration details
- Deployment preview summary
- Project readiness status

## Next Steps

After successful validation:

1. **Deploy Infrastructure:** Run `azd provision` to create Azure resources
2. **Deploy Applications:** Run `azd deploy` to deploy services
3. **Complete Deployment:** Run `azd up` to provision and deploy in one step
4. **Monitor Deployment:** Use `azd monitor` to check application health
5. **View Logs:** Use `azd logs` to view deployment and runtime logs

### Production Preparation

For production deployment:

- Create production environment: `azd env new <project>-prod`
- Configure production-specific settings and secrets
- Set up monitoring, alerting, and backup procedures
- Document operational procedures and runbooks

**DEPLOYMENT READY:** Your AZD migration is complete and ready for deployment with `azd up`.

**IMPORTANT:** This tool centralizes all validation logic. Other tools should reference this tool for validation rather than duplicating validation steps.
