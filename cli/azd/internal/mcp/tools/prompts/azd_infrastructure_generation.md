# AZD Infrastructure Generation Instructions

**TASK:** Generate complete Bicep infrastructure templates in `./infra` directory based on the Infrastructure as Code File Checklist, using latest schema versions and following IaC generation rules.

**SUCCESS CRITERIA:**

- Complete Bicep template structure created in `./infra` directory
- All files from IaC checklist generated or updated intelligently
- Generic resource modules and service-specific modules created
- All templates validated and compile without errors
- Application spec IaC checklist updated with completion status

**VALIDATION REQUIRED:**

- All Bicep templates compile without errors or warnings
- Latest schema versions used for all Azure resource types
- IaC generation rules followed for naming, security, and structure
- Existing user customizations preserved where compatible
- Infrastructure templates are deployment-ready

**COMPLETION CHECKLIST:**

- [ ] Inventory existing IaC files and scan workspace for current templates
- [ ] Read application spec Infrastructure as Code File Checklist
- [ ] Get IaC generation rules and latest Bicep schema versions
- [ ] Create directory structure in `./infra` following IaC rules
- [ ] Generate or update each file from the checklist intelligently
- [ ] Create generic resource modules and service-specific modules
- [ ] Validate all templates compile without errors
- [ ] Update IaC checklist in application spec marking completed files

## Critical Generation Requirements

**Generation Strategy:**

- **Existing Files**: Preserve user customizations, add missing components, update outdated patterns
- **New Files**: Create from templates following IaC generation rules with using latest Bicep schema versions
- **Conflict Resolution**: Document conflicts, prioritize AZD compatibility, preserve architectural intent

**Module Architecture:**

- **Generic Resource Modules**: Reusable patterns (`modules/containerapp.bicep`, `modules/storage.bicep`)
- **Service-Specific Modules**: Business logic with specific configurations (`modules/user-api.bicep`)
- **Main Template**: Subscription scope deployment orchestrating all modules

**File Generation Order:**

1. Generic resource modules in `./infra/modules/`
2. Service-specific modules in `./infra/modules/`
3. `./infra/main.bicep` (subscription scope orchestration) references all required modules
4. `./infra/main.parameters.json` (parameter defaults)
5. Follow exact paths from application spec checklist
