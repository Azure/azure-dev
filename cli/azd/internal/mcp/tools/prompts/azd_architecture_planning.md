# AZD Architecture Planning Instructions

**TASK:** Consolidate all previously gathered context (requirements, stack selection, discovered components) into a complete application architecture design with Azure service mappings and implementation strategy.

**SUCCESS CRITERIA:**

- Application architecture section populated with all component details and relationships
- Azure service mapping completed with specific service assignments and rationale
- Implementation plan updated with concrete infrastructure and deployment strategy
- All previously gathered context properly integrated into existing application spec structure

**VALIDATION REQUIRED:**

- All discovered components are properly mapped to Azure services
- Component dependencies and communication patterns are documented
- Azure service selections align with chosen technology stack and requirements
- Implementation plan provides clear next steps for infrastructure generation

**COMPLETION CHECKLIST:**

- [ ] Read complete application spec to understand previously collected information
- [ ] Populate "Application Architecture" section with component details and relationships
- [ ] Complete "Azure Service Mapping" section with service assignments and rationale
- [ ] Update "Implementation Plan" section with infrastructure and deployment strategy
- [ ] Ensure all gathered context is documented in appropriate existing sections
- [ ] User has confirmed application specification

## Critical Planning Requirements

**Component Architecture Documentation:**

- Map each discovered component to its type (API/SPA/Worker/Function)
- Document component dependencies and communication patterns
- Define data architecture and access patterns
- Identify external integrations and requirements

**Azure Service Mapping:**

- Assign appropriate Azure hosting services based on component types and stack selection
- Map data requirements to Azure data services (SQL, Cosmos DB, etc.)
- Select integration services for messaging and events
- Configure monitoring and security services

**Implementation Strategy:**

- Define infrastructure generation requirements
- Plan deployment sequencing and dependencies
- Identify configuration and environment management needs
- Document next steps for artifact generation
