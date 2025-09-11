# AZD Project Modernization and Migration Instructions

**TASK:** Modernize existing applications to be AZD compatible by conducting analysis, requirements discovery, stack selection, architecture planning, and artifact generation while preserving existing functionality.

**SUCCESS CRITERIA:**

- Comprehensive application specification preserving existing project context
- Complete AZD compatible project structure created for existing application
- Bicep infrastructure templates generated in `./infra` directory
- Dockerfiles created for containerizable services where applicable
- Valid `azure.yaml` configuration file mapping existing components
- Validated project ready for `azd up` deployment

**VALIDATION REQUIRED:**

- Existing application functionality is preserved throughout modernization
- Generated artifacts are compatible with existing code and architecture
- Migration strategy aligns with user requirements and constraints
- All components are properly mapped to Azure services
- AZD project validation passes with all errors resolved

**COMPLETION CHECKLIST:**

- [ ] Analyze existing application architecture and codebase
- [ ] Conduct user intent discovery for requirements and constraints
- [ ] Determine optimal technology stack based on existing code and preferences
- [ ] Perform discovery analysis for comprehensive component identification
- [ ] Conduct architecture planning for Azure service selection
- [ ] Generate artifacts to create AZD compatible project structure
- [ ] Run full AZD project validation and address all errors
- [ ] Document complete modernization in application specification

## Critical Modernization Requirements

**Existing Project Evaluation:**

- Scan workspace for application components, frameworks, and technologies
- Assess existing containerization and deployment configurations
- Review existing Azure resources or configuration
- Note any existing application architecture documentation

**Migration-Specific Considerations:**

- Preserve existing application functionality and architecture decisions
- Maintain backward compatibility during migration process
- Address current pain points or limitations identified by user
- Consider team comfort with existing vs new architectural patterns

**Technology Stack Alignment:**

- **Automatic Determination**: Based on clear code analysis (Docker files, architecture patterns)
- **Manual Selection**: When ambiguous, focus on team comfort and modernization goals
- **Migration Strategy**: Document chosen approach and rationale

**Artifact Generation Strategy:**

- **Preserve Existing**: Keep user customizations and working configurations
- **Add Missing**: Inject AZD required components and configurations
- **Modernize Patterns**: Update to current best practices where beneficial
- **Ensure Compatibility**: Maintain existing deployment and operational patterns
