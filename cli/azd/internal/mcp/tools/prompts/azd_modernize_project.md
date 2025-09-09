# AZD Project Modernization and Migration Instructions

✅ **Agent Task List**  

1. Analyze existing application architecture and codebase
2. Conduct user intent discovery to understand requirements and constraints
3. Determine optimal technology stack based on existing code and user preferences
4. Perform discovery analysis for comprehensive component identification
5. Conduct architecture planning for Azure service selection and infrastructure design
6. Generate files to create AZD-compatible project structure
7. Run full azd project validation and address all errors
8. Document complete modernization in Application specification

📄 **Required Outputs**  

- Complete AZD-compatible project structure for existing application
- Valid `azure.yaml` configuration file mapping existing components
- Bicep infrastructure templates in `./infra` directory
- Dockerfiles for containerizable services (where applicable)
- Comprehensive Application specification documentation preserving existing project context
- Validated project ready for `azd up` deployment

🧠 **Execution Guidelines**  

**CRITICAL:** This tool modernizes existing applications to be AZD-compatible. Always preserve existing application functionality while adding Azure deployment capabilities. Check for existing Application specification and preserve all user modifications.

## Workspace Analysis and Initial Assessment

**Existing Project Evaluation:**

- Scan workspace for application components, frameworks, and technologies
- Identify programming languages, entry points, and dependencies
- Assess existing containerization (Docker files, docker-compose)
- Review existing infrastructure or deployment configurations
- Note any existing Azure resources or configuration

**Technology Stack Detection:**

Based on discovered components, attempt to automatically determine appropriate stack:

- **Containers Stack**: If Docker files exist, complex dependencies, or traditional web/API applications
- **Serverless Stack**: If function-based architecture, event handlers, or simple stateless APIs detected
- **Logic Apps Stack**: If workflow files, extensive API integrations, or business process automation detected

**Ambiguity Handling:**

If stack selection is unclear from code analysis, proceed to stack selection process to gather user preferences.

## Requirements Discovery and Intent Understanding

**Conduct User Intent Discovery Process:**

Even with existing code, gather requirements to understand:

- Current application purpose and target audience
- Performance and scalability expectations for Azure deployment
- Budget and cost considerations for cloud migration
- Compliance and security requirements
- Timeline and migration urgency

**Migration-Specific Questions:**

- "What are your main goals for migrating this application to Azure?"
- "Are there any existing pain points or limitations you want to address?"
- "Do you need to maintain backward compatibility during the migration?"
- "Are there specific Azure services you want to use or avoid?"

**Documentation Target:**

Create or update Application specification with "Migration Requirements" section including existing project context and migration goals.

## Stack Selection and Validation

**Automatic Stack Determination:**

If technology stack was clearly identified from code analysis, document the decision with rationale.

**Manual Stack Selection:**

If ambiguous, conduct stack selection process focusing on:

- Team comfort with existing architecture patterns
- Desire to modernize vs maintain current approach
- Performance and operational requirements
- Integration complexity and existing dependencies

**Stack Decision Documentation:**

Update Application specification with "Technology Stack Selection" section explaining chosen approach and migration strategy.

## Comprehensive Discovery and Analysis

**Perform Discovery Analysis Process:**

Use discovery analysis capability to:

- Create comprehensive inventory of all application components
- Map existing dependencies and communication patterns
- Identify database and external service connections
- Document current architecture and data flows
- Assess containerization readiness for each component

**Migration-Aware Analysis:**

- Identify components that need minimal changes vs significant refactoring
- Note existing infrastructure that can be preserved or needs replacement
- Document any legacy dependencies that require special handling
- Assess cloud-readiness of each component

**Documentation Target:**

Update Application specification with complete "Application Discovery" section including existing architecture analysis and migration considerations.

## Azure Architecture Planning and Service Mapping

**Conduct Architecture Planning Process:**

Use architecture planning capability to:

- Map each existing component to optimal Azure services
- Design migration-friendly infrastructure organization
- Plan phased migration approach if needed
- Select appropriate database and messaging services
- Design containerization strategy respecting existing patterns

**Migration Strategy Design:**

- Plan for minimal disruption deployment approach
- Design rollback strategies and blue-green deployment options
- Consider data migration requirements and strategies
- Plan for environment parity (dev, staging, production)

**Documentation Target:**

Update Application specification with "Azure Service Mapping" and "Migration Architecture" sections.

## AZD Project Structure Creation

**Artifact Generation Orchestration:**

Perform comprehensive artifact generation process:

1. **Application Integration**: Preserve existing application structure while adding AZD compatibility
2. **Docker Configuration Generation**: Create or update Docker files for containerizable services
3. **Infrastructure Template Generation**: Create Bicep templates for Azure resources
4. **Azure.yaml Generation**: Create AZD configuration mapping existing and new components

**Migration-Specific Considerations:**

- Preserve existing build processes and scripts where possible
- Maintain existing environment variable and configuration patterns
- Ensure generated Docker files work with existing application structure
- Create infrastructure that supports existing application requirements

**Configuration Preservation:**

- Maintain existing database schemas and connection patterns
- Preserve API contracts and service interfaces
- Keep existing authentication and authorization patterns
- Maintain current logging and monitoring approaches where compatible

## Project Validation and Deployment Readiness

**Perform Project Validation Process:**

Use project validation capability to ensure:

- Azure.yaml configuration correctly maps all application components
- Bicep templates deploy successfully in test environment
- All application components start and function correctly
- Database connections and external dependencies work properly
- Environment configuration is complete and secure

**Migration Validation:**

- Verify existing functionality is preserved
- Test performance matches or improves upon current deployment
- Confirm security and compliance requirements are met
- Validate monitoring and logging capabilities

## Implementation Workflow Guide

**Complete Modernization Workflow:**

```text
1. Workspace Analysis → 2. User Intent Discovery → 3. Stack Selection (if needed) → 
4. Discovery Analysis → 5. Architecture Planning → 6. Artifact Generation → 
7. Project Validation
```

**Iterative Refinement Workflow:**

```text
1. Review Application specification → 2. Update specific components → 3. Regenerate affected artifacts → 
4. Project Validation
```

**Incremental Migration Workflow:**

```text
1. Select component subset → 2. Focused discovery → 3. Component-specific generation → 
4. Validation → 5. Repeat for remaining components
```

## Success Criteria and Completion Checklist

**Modernization Completion Requirements:**

- [ ] All existing application components identified and mapped to Azure services
- [ ] User intent and migration requirements documented and addressed
- [ ] Technology stack selection completed with clear rationale
- [ ] Complete artifact generation executed (application code, Docker configs, infrastructure, azure.yaml)
- [ ] All existing application functionality preserved and verified
- [ ] Performance and security requirements met or exceeded
- [ ] Application specification comprehensive documentation created while preserving existing content
- [ ] AZD environment initialized and tested
- [ ] Project validation confirms deployment readiness
- [ ] Application confirmed working end-to-end with `azd up`

**Migration Success Indicators:**

- Existing application runs correctly in Azure environment
- All data connections and external integrations function properly
- Performance meets or exceeds current deployment
- Security and compliance requirements are satisfied
- Team can deploy and manage using AZD workflows
- Rollback procedures tested and documented
