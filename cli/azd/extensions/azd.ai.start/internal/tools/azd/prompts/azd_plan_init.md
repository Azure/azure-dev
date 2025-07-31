# AZD Application Initialization and Migration Plan

This document provides a comprehensive, step-by-step plan for initializing or migrating applications to use Azure Developer CLI (AZD). Follow these steps sequentially to ensure successful AZD adoption.

## Executive Summary

Transform any application into an AZD-compatible project by:

1. Analyzing the current codebase and architecture
2. Identifying all application components and dependencies
3. Generating required configuration and infrastructure files
4. Establishing the AZD environment structure

## Phase 1: Discovery and Analysis

### Step 1: Deep File System Analysis

**REQUIRED ACTIONS:**

- Scan all files in the current working directory recursively
- Document file structure, programming languages, and frameworks detected
- Identify configuration files (package.json, requirements.txt, pom.xml, etc.)
- Locate any existing Docker files, docker-compose files, or containerization configs
- Find database configuration files and connection strings
- Identify API endpoints, service definitions, and application entry points

**OUTPUT:** Complete inventory of all discoverable application artifacts

### Step 2: Component Classification

**REQUIRED ACTIONS:**

- Categorize each discovered component into one of these types:
  - **Web Applications** (frontend, SPA, static sites)
  - **API Services** (REST APIs, GraphQL, gRPC services)
  - **Background Services** (workers, processors, scheduled jobs)
  - **Databases** (relational, NoSQL, caching)
  - **Messaging Systems** (queues, topics, event streams)
  - **AI/ML Components** (models, inference endpoints, training jobs)
  - **Supporting Services** (authentication, logging, monitoring)

**OUTPUT:** Structured component inventory with classifications

### Step 3: Dependency Mapping

**REQUIRED ACTIONS:**

- Map inter-component dependencies and communication patterns
- Identify external service dependencies (third-party APIs, SaaS services)
- Document data flow between components
- Identify shared resources and configuration

**OUTPUT:** Component dependency graph and communication matrix

## Phase 2: Architecture Planning and Azure Service Selection

### Application Component Planning

For each identified application component, execute the following analysis:

**REQUIRED ANALYSIS:**

- **Hosting Platform Selection:**
  - **Azure Container Apps** (PREFERRED for microservices and containerized apps)
  - **Azure App Service** (for web apps and APIs with specific runtime requirements)
  - **Azure Functions** (for serverless and event-driven components)
  - **Azure Static Web Apps** (for frontend applications and SPAs)
  - **Azure Kubernetes Service** (for complex orchestration requirements)

- **Containerization Assessment:**
  - Determine if component can run in Docker container
  - If Dockerfile doesn't exist, plan Docker container strategy
  - Identify base images and runtime requirements
  - Document port mappings and environment variables

- **Configuration Requirements:**
  - Identify environment-specific settings
  - Map secrets and sensitive configuration
  - Document connection strings and service endpoints
  - Plan configuration injection strategy

**OUTPUT:** Hosting strategy and containerization plan for each component

### Database Component Planning

For components using persistent data storage:

**REQUIRED ANALYSIS:**

- **Azure Database Service Selection:**
  - **Azure SQL Database** (for relational data with SQL Server compatibility)
  - **Azure Database for PostgreSQL** (for PostgreSQL workloads)
  - **Azure Database for MySQL** (for MySQL workloads)
  - **Azure Cosmos DB** (for NoSQL, multi-model data)
  - **Azure Cache for Redis** (for caching and session storage)

- **Migration Strategy:**
  - Assess current database schema and data
  - Plan data migration approach
  - Identify backup and recovery requirements
  - Document connection string patterns

**OUTPUT:** Database hosting plan and migration strategy

### Messaging Component Planning

For components using asynchronous communication:

**REQUIRED ANALYSIS:**

- **Azure Messaging Service Selection:**
  - **Azure Service Bus** (for reliable enterprise messaging)
  - **Azure Event Hubs** (for high-throughput event streaming)
  - **Azure Event Grid** (for event-driven architectures)
  - **Azure Storage Queues** (for simple queue scenarios)

- **Integration Planning:**
  - Map message flows and routing
  - Identify message schemas and formats
  - Plan dead letter handling and error scenarios
  - Document scaling and throughput requirements

**OUTPUT:** Messaging architecture and integration plan

### AI Component Planning

For components using artificial intelligence or machine learning:

**REQUIRED ANALYSIS:**

- **Azure AI Service Selection:**
  - **Azure OpenAI Service** (for GPT models and cognitive services)
  - **Azure AI Services** (for vision, speech, language processing)
  - **Azure Machine Learning** (for custom ML models and training)
  - **Azure Cognitive Search** (for intelligent search capabilities)

- **Model and Data Requirements:**
  - Identify required AI models and versions
  - Document input/output data formats
  - Plan model deployment and scaling strategy
  - Assess training data and pipeline requirements

**OUTPUT:** AI service architecture and deployment plan

## Phase 3: File Generation and Configuration

### Step 1: Generate azure.yaml Configuration

**REQUIRED ACTIONS:**

- Create `azure.yaml` file in the root directory
- Define all services with appropriate hosting configurations
- Specify build and deployment instructions for each service
- Configure environment variable mappings
- Reference infrastructure templates correctly

**TEMPLATE STRUCTURE:**

```yaml
name: {project-name}
services:
  {service-name}:
    project: ./path/to/service
    host: {hosting-type}
    # Additional service-specific configuration
```

### Step 2: Generate Infrastructure as Code Files

**REQUIRED ACTIONS:**

- Create `./infra` directory structure
- Generate `main.bicep` as primary deployment template
- Create modular Bicep files for each resource type
- **CRITICAL:** Follow all rules from AZD IaC Generation Rules document
- Implement proper naming conventions and tagging strategies
- Include supporting resources (Log Analytics, Application Insights, Key Vault)

### Step 3: Generate Container Configurations

**REQUIRED ACTIONS:**

- Create Dockerfile for each containerizable component
- Use appropriate base images for detected programming languages
- Configure health checks and startup commands
- Set proper working directories and file permissions
- Optimize for production deployment

### Step 4: Generate Architecture Documentation

**REQUIRED ACTIONS:**

- Create `azd-arch-plan.md` with comprehensive analysis
- Document all discovered components and their relationships
- Include architecture diagrams (text-based or mermaid)
- Explain Azure service selections and rationale
- Provide deployment and operational guidance

**DOCUMENT STRUCTURE:**

- Executive Summary
- Application Architecture Overview
- Component Analysis
- Azure Service Mapping
- Infrastructure Design
- Deployment Strategy
- Operational Considerations

## Phase 4: Environment Initialization

### Step 1: Create AZD Environment

**REQUIRED ACTIONS:**

- Execute: `azd env new {directory-name}-dev`
- Use current working directory name as environment name base
- Configure environment-specific settings
- Validate environment configuration

### Step 2: Validation and Testing

**REQUIRED ACTIONS:**

- Run `azd package` to validate service configurations
- Execute `azd provision --dry-run` to test infrastructure templates
- Verify all Bicep files compile without errors
- Check all referenced files and paths exist
- Validate environment variable configurations

## Success Criteria

The migration is successful when:

- [ ] All application components are identified and classified
- [ ] `azure.yaml` file is valid and complete
- [ ] All infrastructure files are generated and error-free
- [ ] Required Dockerfiles are created for containerizable components
- [ ] `azd-arch-plan.md` provides comprehensive documentation
- [ ] AZD environment is initialized and validated
- [ ] `azd package` completes without errors
- [ ] `azd provision --dry-run` validates successfully

## Common Patterns and Best Practices

### For Multi-Service Applications

- Use Azure Container Apps for microservices architecture
- Implement shared infrastructure (networking, monitoring)
- Configure service-to-service communication properly

### For Data-Intensive Applications

- Co-locate compute and data services in same region
- Implement proper connection pooling and caching
- Configure backup and disaster recovery

### For AI-Enabled Applications

- Separate AI services from main application logic
- Implement proper error handling for AI service calls
- Plan for model updates and versioning

### For High-Availability Applications

- Configure multiple availability zones
- Implement health checks and auto-scaling
- Plan for disaster recovery scenarios
