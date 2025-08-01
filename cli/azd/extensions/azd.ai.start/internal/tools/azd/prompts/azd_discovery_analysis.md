# AZD Application Discovery and Analysis Tool

This tool performs comprehensive discovery and analysis of applications to prepare them for Azure Developer CLI (AZD) initialization. This is Phase 1 of the AZD migration process.

Always use Azure best practices with intelligent defaults.

## Overview

This tool analyzes your current codebase and architecture to:
1. Identify all application components and dependencies
2. Classify components by type and hosting requirements
3. Map dependencies and communication patterns
4. Provide foundation for architecture planning

**IMPORTANT:** Before starting, check if `azd-arch-plan.md` exists in your current working directory. If it exists, review it to understand what analysis has already been completed and build upon that work.

## Success Criteria

The discovery and analysis is successful when:

- [ ] Complete file system inventory is documented
- [ ] All application components are identified and classified
- [ ] Component dependencies are mapped
- [ ] Results are documented in `azd-arch-plan.md`
- [ ] Ready to proceed to architecture planning phase

## Step 1: Deep File System Analysis

**REQUIRED ACTIONS:**

- Scan all files in the current working directory recursively
- Document file structure, programming languages, and frameworks detected
- Identify configuration files (package.json, requirements.txt, pom.xml, etc.)
- Locate any existing Docker files, docker-compose files, or containerization configs
- Find database configuration files and connection strings
- Identify API endpoints, service definitions, and application entry points
- Look for existing CI/CD pipeline files (.github/workflows, azure-pipelines.yml, etc.)
- Identify documentation files (README.md, API docs, architecture docs)

**ANALYSIS QUESTIONS TO ANSWER:**

- What programming languages and frameworks are used?
- What build systems and package managers are in use?
- Are there existing containerization configurations?
- What ports and endpoints are exposed?
- What external dependencies are required?
- Are there existing deployment or infrastructure configurations?

**OUTPUT:** Complete inventory of all discoverable application artifacts

## Step 2: Component Classification

**REQUIRED ACTIONS:**

Categorize each discovered component into one of these types:

- **Web Applications** (frontend, SPA, static sites)
  - React, Angular, Vue.js applications
  - Static HTML/CSS/JavaScript sites
  - Server-rendered web applications

- **API Services** (REST APIs, GraphQL, gRPC services)
  - RESTful web APIs
  - GraphQL endpoints
  - gRPC services
  - Microservices

- **Background Services** (workers, processors, scheduled jobs)
  - Message queue processors
  - Scheduled task runners
  - Data processing pipelines
  - Event handlers

- **Databases** (relational, NoSQL, caching)
  - SQL Server, PostgreSQL, MySQL databases
  - NoSQL databases (MongoDB, CosmosDB)
  - Caching layers (Redis, Memcached)
  - Database migration scripts

- **Messaging Systems** (queues, topics, event streams)
  - Message queues
  - Event streaming platforms
  - Pub/sub systems

- **AI/ML Components** (models, inference endpoints, training jobs)
  - Machine learning models
  - AI inference endpoints
  - Training pipelines
  - Data preprocessing services

- **Supporting Services** (authentication, logging, monitoring)
  - Authentication services
  - Logging aggregators
  - Monitoring and metrics
  - Configuration services

**CLASSIFICATION CRITERIA:**

For each component, determine:
- Primary function and responsibility
- Runtime requirements
- Scalability needs
- Security considerations
- Integration points

**OUTPUT:** Structured component inventory with classifications

## Step 3: Dependency Mapping

**REQUIRED ACTIONS:**

- Map inter-component dependencies and communication patterns
- Identify external service dependencies (third-party APIs, SaaS services)
- Document data flow between components
- Identify shared resources and configuration
- Analyze network communication requirements
- Document authentication and authorization flows

**DEPENDENCY ANALYSIS:**

- **Internal Dependencies:** How components communicate with each other
- **External Dependencies:** Third-party services, APIs, databases
- **Data Dependencies:** Shared databases, file systems, caches
- **Configuration Dependencies:** Shared settings, secrets, environment variables
- **Runtime Dependencies:** Required services for startup and operation

**COMMUNICATION PATTERNS TO IDENTIFY:**

- Synchronous HTTP/HTTPS calls
- Asynchronous messaging
- Database connections
- File system access
- Caching patterns
- Authentication flows

**OUTPUT:** Component dependency graph and communication matrix

## Step 4: Generate Discovery Report

**REQUIRED ACTIONS:**

Create or update `azd-arch-plan.md` with the following sections:

```markdown
# AZD Architecture Plan

## Discovery and Analysis Results

### Application Overview
- [Summary of application type and purpose]
- [Key technologies and frameworks identified]
- [Overall architecture pattern (monolith, microservices, etc.)]

### Component Inventory
[For each component discovered:]
- **Component Name:** [name]
- **Type:** [classification]
- **Technology:** [language/framework]
- **Location:** [file path/directory]
- **Purpose:** [brief description]
- **Entry Points:** [how component is accessed]
- **Configuration:** [key config files]

### Dependency Map
[Visual or text representation of dependencies]
- **Component A** → **Component B** (HTTP API)
- **Component B** → **Database** (SQL connection)
- **Component A** → **External API** (REST calls)

### External Dependencies
- [List of third-party services]
- [Required environment variables]
- [External configuration requirements]

### Next Steps
- [ ] Review discovery results
- [ ] Proceed to architecture planning phase
- [ ] Use `azd_architecture_planning` tool
```

## Validation and Next Steps

**VALIDATION CHECKLIST:**

- [ ] All major application components identified
- [ ] Component types and technologies documented
- [ ] Dependencies mapped and understood
- [ ] External services and APIs catalogued
- [ ] `azd-arch-plan.md` created or updated with findings

**NEXT PHASE:**

After completing this discovery phase, proceed to the **Architecture Planning** phase using the `azd_architecture_planning` tool. This next phase will use your discovery results to:

- Select appropriate Azure services for each component
- Plan hosting strategies and containerization
- Design infrastructure architecture
- Prepare for configuration file generation

**IMPORTANT:** Keep the `azd-arch-plan.md` file updated throughout the process as it serves as the central planning document for your AZD migration.
