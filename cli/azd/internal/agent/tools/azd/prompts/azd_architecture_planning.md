# AZD Architecture Planning Tool

This tool performs Azure service selection and architecture planning for Azure Developer CLI (AZD) initialization. This is Phase 2 of the AZD migration process.

## Overview

Use discovery results to select appropriate Azure services, plan hosting strategies, and design infrastructure architecture.

**IMPORTANT:** Before starting, review the `azd-arch-plan.md` file in your current working directory to understand discovered components and dependencies from the discovery phase.

## Success Criteria

- [ ] Azure service selections made for all components
- [ ] Hosting strategies defined for each service
- [ ] Containerization plans documented
- [ ] Infrastructure architecture designed
- [ ] Ready to proceed to file generation phase

## Azure Service Selection

**REQUIRED ANALYSIS:**

For each discovered application component, select the most appropriate Azure hosting platform:

### Azure Container Apps (PREFERRED)

**Use for:** Microservices, containerized applications, event-driven workloads
**Benefits:** Auto-scaling, managed Kubernetes, simplified deployment
**Consider when:** Component can be containerized, needs elastic scaling

### Azure App Service

**Use for:** Web applications, REST APIs with specific runtime needs
**Benefits:** Managed platform, built-in CI/CD, easy SSL/custom domains
**Consider when:** Need specific runtime versions, Windows-specific features

### Azure Functions

**Use for:** Event processing, scheduled tasks, lightweight APIs
**Benefits:** Serverless, automatic scaling, pay-per-execution
**Consider when:** Event-driven processing, stateless operations

### Azure Static Web Apps

**Use for:** Frontend SPAs, static sites, JAMstack applications
**Benefits:** Global CDN, built-in authentication, API integration
**Consider when:** Static content, minimal backend requirements

## Selection Criteria

**REQUIRED ANALYSIS:**

For each discovered component, consider:

- Scalability requirements and traffic patterns
- Runtime and platform needs
- Operational complexity preferences
- Cost considerations
- Team expertise and preferences

## Containerization Planning

**REQUIRED ASSESSMENT:**

For each component, determine:

- **Containerization Feasibility:** Can it run in Docker? Windows-specific dependencies?
- **Docker Strategy:** Base image selection, port mappings, environment variables
- **Resource Requirements:** CPU, memory, storage needs
- **Health Check Strategy:** Endpoint patterns for monitoring

## Data Storage Planning

**REQUIRED ANALYSIS:**

Select appropriate Azure database services:

### Azure SQL Database

**Use for:** SQL Server compatibility, complex queries, ACID compliance
**Consider when:** Relational data model, existing SQL Server applications

### Azure Database for PostgreSQL/MySQL

**Use for:** PostgreSQL/MySQL workloads, web applications
**Consider when:** Specific database engine compatibility required

### Azure Cosmos DB

**Use for:** NoSQL requirements, global scale, flexible schemas
**Consider when:** Multiple data models, global distribution needed

### Azure Cache for Redis

**Use for:** Application caching, session storage, real-time analytics
**Consider when:** Performance optimization, session management

## Messaging and Integration Planning

**REQUIRED ANALYSIS:**

Select messaging services based on patterns:

### Azure Service Bus

**Use for:** Enterprise messaging, guaranteed delivery, complex routing
**Consider when:** Reliable messaging, enterprise scenarios

### Azure Event Hubs

**Use for:** High-throughput event streaming, telemetry ingestion
**Consider when:** Big data scenarios, real-time analytics

### Azure Event Grid

**Use for:** Event-driven architectures, reactive programming
**Consider when:** Decoupled systems, serverless architectures

## Update Architecture Documentation

**REQUIRED ACTIONS:**

Update `azd-arch-plan.md` with:

### Azure Service Mapping Table

```markdown
| Component | Current Tech | Azure Service | Rationale |
|-----------|-------------|---------------|-----------|
| Web App | React | Static Web Apps | Frontend SPA |
| API Service | Node.js | Container Apps | Microservice architecture |
| Database | PostgreSQL | Azure Database for PostgreSQL | Existing dependency |
```

### Hosting Strategy Summary

- Document hosting decisions for each component
- Include containerization plans where applicable
- Note resource requirements and scaling strategies

### Infrastructure Architecture

- Resource group organization strategy
- Networking and security design approach
- Monitoring and logging strategy
- Integration patterns between services

### Next Steps Checklist

- [ ] Azure service selected for each component with rationale
- [ ] Hosting strategies defined
- [ ] Containerization plans documented
- [ ] Data storage strategies planned
- [ ] Ready to proceed to file generation phase

## Next Phase

After completing architecture planning, proceed to the appropriate file generation tool:

- Use `azd_azure_yaml_generation` tool for azure.yaml configuration
- Use `azd_infrastructure_generation` tool for Bicep templates
- Use `azd_docker_generation` tool for container configurations
- Use `azd_project_validation` tool for final project validation

**IMPORTANT:** Keep `azd-arch-plan.md` updated as the central reference for all architecture decisions. This document guides subsequent phases and serves as implementation documentation.
