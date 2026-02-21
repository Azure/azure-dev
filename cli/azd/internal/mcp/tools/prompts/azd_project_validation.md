# azd project Validation Instructions

✅ **Agent Task List**  

1. Load existing `azd-arch-plan.md` to understand current project state and context
2. Execute azure.yaml against azd schema using available tool
3. Compile and validate all Bicep templates in ./infra directory
4. Verify azd environment exists and is properly configured
5. Run `azd package --no-prompt` to validate service packaging
6. Execute `azd provision --preview --no-prompt` to test infrastructure deployment
7. Resolve ALL issues found in each validation step before proceeding
8. Update existing `azd-arch-plan.md` with validation results by adding/updating validation section while preserving existing content

📄 **Required Outputs**  

- Complete validation report with all checks passed
- All identified issues resolved with zero remaining errors
- Confirmation that project is ready for deployment
- Update existing `azd-arch-plan.md` with validation results while preserving existing content
- Validation checklist added to or updated in architecture plan
- Clear next steps for deployment

🧠 **Execution Guidelines**  

**CRITICAL REQUIREMENT:** Resolve ALL issues found during validation before proceeding to the next step.
No validation step should be considered successful until all errors, warnings, and issues have been fully addressed.

**Validation Execution Steps:**

**1. Load Architecture Plan:**

- Read existing `azd-arch-plan.md` to understand current project architecture and context
- Review any previous validation results or known issues
- Understand the project structure and service configurations from the plan
- **MANDATORY:** Must load and review architecture plan before starting validation

**2. Azure.yaml Schema Validation:**

- Check if `azure.yaml` exists in current directory
- Validate `azure.yaml` against azd schema using available tools
- Parse and report any schema violations or missing fields
- Verify service definitions and configurations are correct
- **MANDATORY:** Fix ALL schema violations before proceeding

**3. azd Environment Validation:**

- Execute `azd env list` to check available environments
- If no environments exist, create one: `azd env new <directory-name>-dev`
- Ensure environment is selected and configured
- Ensure `AZURE_LOCATION` azd environment variable is set to a valid Azure location value
- Ensure `AZURE_SUBSCRIPTION_ID` azd environment variable is set to the users current Azure subscription
- **MANDATORY:** Fix environment issues before proceeding

**4. Bicep Template Validation:**

- Scan `./infra` directory for `.bicep` files using file search
- Review azd IaC generation rules and guidelines and resolve any all issues
- Execute `azd provision --preview --no-prompt` to validate infrastructure templates
- **MANDATORY:** Fix ALL compilation errors before proceeding
- Clean up any generated `<module.json>` files generated during bicep validation

**5. Package Validation:**

- Execute `azd package --no-prompt` command and monitor output
- Verify all service source paths are valid
- Check Docker builds complete successfully for containerized services
- Ensure all build artifacts are created correctly
- **MANDATORY:** Fix ALL packaging errors before proceeding

**Error Resolution Requirements:**

- **Azure.yaml Schema Errors:** Validate azure.yaml using available tools
- **Bicep Compilation Errors:** Parse error output, check module paths and parameter names, verify resource naming
- **Environment Issues:** Run `azd auth login` if needed, check subscription access, verify location parameter
- **Package Errors:** Check service source paths, verify Docker builds work locally, ensure dependencies available
- **Provision Preview Errors:** Verify subscription permissions, check resource quotas, ensure resource names are unique

📌 **Completion Checklist**  

- [ ] `azd-arch-plan.md` loaded and reviewed for project context
- [ ] `azure.yaml` passes schema validation with NO errors or warnings
- [ ] azd environment exists and is properly configured with NO issues
- [ ] ALL Bicep templates compile without errors or warnings
- [ ] `azd provision --preview` completes without errors or warnings with ALL resources validating correctly
- [ ] `azd package` completes without errors or warnings with ALL services packaging successfully
- [ ] ALL service configurations are valid with NO missing or incorrect settings
- [ ] NO missing dependencies or configuration issues remain
- [ ] Validation results added to existing `azd-arch-plan.md` while preserving existing content
- [ ] Project confirmed ready for deployment with `azd up`
