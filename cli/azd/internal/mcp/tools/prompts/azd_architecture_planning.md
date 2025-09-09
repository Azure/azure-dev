# AZD Architecture Planning Instructions

✅ **Agent Task List**  

1. Review application spec to gather all previously collected context (requirements, stack selection, discovered components)
2. Populate the "Application Architecture" section with component details and relationships
3. Complete the "Azure Service Mapping" section with specific service assignments and rationale
4. Update the "Implementation Plan" section with concrete next steps
5. Ensure all gathered context is properly documented in the appropriate existing sections

📄 **Required Outputs**  

- Fully populated "Application Architecture" section with component details, data architecture, and integration patterns
- Complete "Azure Service Mapping" section showing all service assignments with rationale
- Updated "Implementation Plan" with infrastructure and deployment strategy
- All previously gathered context properly integrated into the existing application spec structure

🧠 **Execution Guidelines**  

**CRITICAL:** This tool focuses on consolidating all previously gathered context into the existing application spec sections. Do not create new sections - populate the template sections with the collected information.

## Context Consolidation Process

**Review All Gathered Context:**

- Read the complete application spec to understand all previously collected information
- Review project requirements, technology stack selection, and discovered components
- Identify any gaps in information that need to be addressed
- Note the selected technology stack and baseline Azure resources

**Populate Application Architecture Section:**

Use the existing "Application Architecture" section structure to document:
**Component Overview Table:**
Fill in the existing component table with discovered application components:

```markdown
| Component Name | Type | Technology | Purpose | Dependencies |
|---------------|------|------------|---------|--------------|
| [discovered-component-1] | [API/SPA/Worker/Function] | [Current technology] | [Component purpose] | [Other components or services] |
```

**Component Details:**
For each discovered component, populate the detailed sections with:

- Component type and technology choices
- Detailed purpose and responsibilities
- Data access patterns and requirements
- External integrations and dependencies
- Scaling and performance characteristics

**Data Architecture:**
Document the data strategy based on discovered requirements:

- Primary database technology and rationale
- Caching requirements and approach
- Data flow patterns between components
- Backup and recovery considerations

**Integration Architecture:**
Define how components will communicate:

- Internal service communication patterns
- Event-driven architecture if applicable
- External API integrations
- Authentication and security patterns

## Azure Service Mapping

**Populate Service Architecture Tables:**

Fill the existing service mapping tables based on the selected technology stack:

**Hosting Services Table:**
Map each application component to its Azure hosting service with specific configuration details:

```markdown
| Component | Azure Service | Configuration | Rationale |
|-----------|---------------|---------------|-----------|
| [component-name] | [Container Apps/Functions/Logic Apps] | [Scaling, networking, runtime details] | [Why this service fits the requirements] |
```

Example populated entries:

- **Web API Component**: Container Apps | Auto-scaling 1-10 instances, HTTP ingress | Containerized service needing variable scaling
- **Frontend SPA**: Static Web Apps | Global CDN, custom domain | Static React app with global distribution needs  
- **Background Processor**: Azure Functions | Consumption plan, Event Hub trigger | Event-driven processing with cost optimization
- **Workflow Orchestrator**: Logic Apps | Standard tier, HTTP triggers | Business process automation with visual design needs

**Supporting Services:**
Document the supporting Azure services based on the selected stack:

- **Data Services**: Map data requirements to Azure SQL, Cosmos DB, PostgreSQL, etc.
- **Integration Services**: Select messaging services (Service Bus, Event Hubs, Event Grid)
- **Infrastructure Services**: Configure monitoring, security, and networking services

Use the baseline resources for all stacks:

- Log Analytics Workspace for centralized logging
- Application Insights for performance monitoring
- Key Vault for secrets management
- App Configuration for configuration management
- Storage Account for file storage and queues

**Resource Organization:**
Define the resource organization strategy:

- Resource group structure and naming
- Environment separation strategy
- Naming conventions for all resources

## Implementation Plan Updates

**Update Development Approach:**

Populate the project structure section with the actual discovered components:

```text
src/
├── [actual-component-1]/     # [Actual component description]
├── [actual-component-2]/     # [Actual component description]
├── shared/                   # Shared libraries and utilities
└── docs/                    # Additional documentation
```

**Update Deployment Strategy:**

Based on the selected technology stack, document:

- Infrastructure as Code approach (Bicep templates for selected services)
- Container strategy (if containers stack selected)
- Configuration management approach
- CI/CD pipeline requirements

## Documentation Requirements

Ensure all sections are populated with the gathered context:

1. **Application Architecture** - Complete component details, data architecture, and integration patterns
2. **Azure Service Mapping** - Full service assignments with rationale
3. **Implementation Plan** - Concrete development and deployment approach
4. **Project Status** - Update status based on completed discovery and planning phases

The tool should transform this template structure:

```markdown
### Selected Stack: [CONTAINERS/SERVERLESS/LOGIC APPS]
```

Into populated content like:

```markdown
### Selected Stack: CONTAINERS

#### Selection Rationale
- **Team Expertise**: Team has Docker experience and wants infrastructure control
- **Application Characteristics**: Traditional web API and frontend requiring consistent performance
- **Performance Requirements**: Need minimal cold starts and predictable response times
- **Integration Needs**: Multiple external API integrations with custom authentication
- **Cost Considerations**: Predictable costs preferred over pay-per-execution
```

📌 **Completion Checklist**  

- [ ] Application spec reviewed for discovered components and selected technology stack
- [ ] Azure service mapping completed for all discovered components with documented rationale
- [ ] Component relationships and data flow patterns documented
- [ ] Resource organization strategy defined (resource groups, naming, environments)
- [ ] Implementation checklists created for infrastructure and containerization generation
- [ ] Application spec updated with "Azure Service Mapping" section while preserving existing content
- [ ] Architecture documentation complete and ready for implementation phases
