# azd application Initialization and Migration Instructions

✅ **Agent Task List**  

1. **Check Progress:** Review existing `azd-arch-plan.md` to understand completed work
2. **Phase 1:** Execute `azd_discovery_analysis` for component identification
3. **Phase 2:** Execute `azd_architecture_planning` for Azure service selection
4. **Phase 3:** Execute file generation tools (`azd_azure_yaml_generation`, `azd_infrastructure_generation`, `azd_docker_generation`)
5. **Phase 4:** Execute `azd_project_validation` for complete validation
6. **Final:** Confirm project readiness for deployment

📄 **Required Outputs**  

- Complete azd-compatible project structure
- Valid `azure.yaml` configuration file
- Bicep infrastructure templates in `./infra` directory
- Dockerfiles for containerizable services
- Comprehensive `azd-arch-plan.md` documentation (created or updated while preserving existing content)
- Validated project ready for `azd up` deployment

🧠 **Execution Guidelines**  

**CRITICAL:** Always check if `azd-arch-plan.md` exists first to understand current progress and avoid duplicate work. If the file exists, preserve all existing content and user modifications while updating relevant sections.

**Complete Workflow Phases:**

**Phase 1: Review Existing Progress**

- Check if `azd-arch-plan.md` exists in current directory
- If exists: Review thoroughly and skip completed phases
- If doesn't exist: Proceed to Phase 2

**Phase 2: Discovery and Analysis**

- Tool: `azd_discovery_analysis`
- Scans files recursively, documents structure/languages/frameworks
- Identifies entry points, maps dependencies, creates component inventory
- Updates `azd-arch-plan.md` with findings

**Phase 3: Architecture Planning and Azure Service Selection**

- Tool: `azd_architecture_planning`
- Maps components to Azure services, plans hosting strategies
- Designs database/messaging architecture, creates containerization strategies
- Updates `azd-arch-plan.md` with service selections

**Phase 4: File Generation (Execute in Sequence)**

Using available tools - Generate the following files:

1. **Docker Configurations:** Generate docker files (Required for containerizable services)
2. **Infrastructure Templates:** Generate IaC infrastructure templates (Required for all projects)
3. **Azure.yaml Configuration:** Generate `azure.yaml` file (Required for all projects)

**Phase 5: Project Validation and Environment Setup**

Using available tools - Perform and end-to-end azd project validation

- Validates azure.yaml against schema
- Validate azd environment exists
- Validate infrastructure templates
- Ensures azd environment exists, tests packaging, validates deployment preview
- Provides readiness confirmation

**Usage Patterns:**

**Complete New Project Migration:**

```text
1. Review azd-arch-plan.md → 2. azd_discovery_analysis → 3. azd_architecture_planning → 
4. azd_azure_yaml_generation → 5. azd_infrastructure_generation → 6. azd_docker_generation → 
7. azd_project_validation
```

**Update Existing azd project:**

```text
1. Review azd-arch-plan.md → 2. File generation tools → 3. azd_project_validation
```

**Quick Service Addition:**

```text
1. Review azd-arch-plan.md → 2. azd_discovery_analysis → 3. azd_azure_yaml_generation → 
4. azd_docker_generation → 5. azd_project_validation
```

📌 **Completion Checklist**  

- [ ] All application components identified and classified in discovery phase
- [ ] Azure service selections made for each component with rationale
- [ ] `azure.yaml` file generated and validates against schema
- [ ] Infrastructure files generated and compile without errors
- [ ] Dockerfiles created for containerizable components
- [ ] `azd-arch-plan.md` created or updated to provide comprehensive project documentation while preserving existing content
- [ ] azd environment initialized and configured
- [ ] All validation checks pass using `azd_project_validation` tool
- [ ] Project confirmed ready for deployment with `azd up`
