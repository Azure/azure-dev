# AZD Project Validation Instructions

**TASK:** Execute comprehensive validation of AZD project components including azure.yaml schema, Bicep templates, environment configuration, packaging, and deployment preview to ensure deployment readiness.

**SUCCESS CRITERIA:**

- Complete validation report with all checks passed
- All identified issues resolved with zero remaining errors
- Confirmation that project is ready for deployment
- Validation results documented in application architecture plan
- Clear next steps provided for deployment

**VALIDATION REQUIRED:**

- `azure.yaml` passes schema validation without anyerrors or warnings
- AZD environment exists and is properly configured
- All Bicep templates compile without errors or warnings
- `azd provision --preview --no-prompt` completes successfully with all resources validating
- `azd package --no-prompt` completes without errors with all services packaging successfully
- All service configurations are valid with no missing or incorrect settings

**COMPLETION CHECKLIST:**

- [ ] Load existing application architecture plan for project context
- [ ] `azure.yaml` schema validation passes without any errors or warnings
- [ ] Compile and validate all Bicep templates in ./infra directory
- [ ] Verify AZD environment exists and is properly configured
- [ ] Run `azd package --no-prompt` to validate service packaging
- [ ] Execute `azd provision --preview --no-prompt` to test infrastructure deployment
- [ ] Resolve ALL issues found in each validation step
- [ ] Update application architecture plan with validation results

## Critical Validation Requirements

**Mandatory Resolution Requirement:**

- **CRITICAL**: Resolve ALL issues found during validation before proceeding
- No validation step is successful until all errors, warnings, and issues are fully addressed
- Each validation step must pass completely before moving to the next

**Validation Execution Order:**

1. **Architecture Plan Review**: Load and understand current project context
2. **Schema Validation**: Validate azure.yaml against AZD schema
3. **Environment Validation**: Ensure AZD environment is configured with required variables. Prompt user for environment name, location, Azure Subscription and other required values
4. **Package Validation**: Verify all services can be packaged successfully
5. **Deployment Preview**: Test infrastructure deployment without actual provisioning

**Error Resolution Requirements:**

- **Azure.yaml Errors**: Fix schema violations, verify service paths, validate configurations
- **Environment Issues**: Configure authentication, set required variables, verify permissions
- **Bicep Errors**: Resolve compilation issues, verify module paths, check resource naming
- **Package Errors**: Fix service paths, resolve Docker build issues, verify dependencies
- **Provision Errors**: Address permission issues, resolve resource conflicts, fix configuration problems
