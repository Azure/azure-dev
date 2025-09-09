# AZD Artifact Generation Orchestration Instructions

✅ **Agent Task List**  

1. Review application spec for complete project architecture and service specifications
2. Read required resources from the Infrastructure as Code File Checklist in application spec
3. Analyze existing workspace to identify current artifacts and generation requirements
4. Get IaC generation rules for Azure infrastructure best practices
5. Generate infrastructure templates for all Azure resources using the rules and resource requirements
6. Generate application scaffolding for new project components
7. Generate Docker configuration for containerizable services
8. Generate azure.yaml configuration for AZD deployment
9. Validate all generated artifacts for consistency and deployment readiness
10. Update application spec with artifact generation status and completion tracking

📄 **Required Outputs**  

- Application scaffolding and starter code for new project components
- Docker configurations (Dockerfiles and .dockerignore) for containerizable services
- Complete Bicep infrastructure templates in `./infra` directory
- Valid `azure.yaml` configuration file mapping all services and resources
- All artifacts validated for syntax correctness and deployment compatibility
- Updated application spec with comprehensive artifact generation documentation

🧠 **Execution Guidelines**  

**CRITICAL:** This tool orchestrates the complete artifact generation process for AZD projects. Execute generation phases in the correct order to ensure dependencies are properly handled. Always preserve existing user customizations while adding required AZD capabilities.

## Pre-Generation Analysis and Planning

**Architecture Review:**

- Read complete application spec to understand project architecture
- Review service mappings, technology stack decisions, and infrastructure design
- Identify which artifacts need generation vs updates vs preservation
- Note any existing user customizations that must be maintained

**Existing Artifact Inventory:**

Scan workspace for existing artifacts:

- **Application Code**: Entry points, framework files, existing business logic
- **Docker Files**: Existing Dockerfiles, docker-compose configurations
- **Infrastructure Code**: Current Bicep templates, ARM templates, Terraform files
- **Configuration Files**: Existing azure.yaml, deployment scripts, CI/CD definitions

**Generation Requirements Assessment:**

Based on project type and architecture decisions:

- **New Projects**: Full scaffolding + all infrastructure + configuration
- **Existing Applications**: Minimal scaffolding + infrastructure + configuration preservation
- **Hybrid Scenarios**: Selective generation with integration considerations

## Application Scaffolding Generation

**Application Code Generation:**

Use application code generation capabilities to create:

**Framework-Appropriate Scaffolding:**

- Generate complete application structure in `src/<component>` directories
- Create language and framework-specific project files and configurations
- Include Azure SDK integrations and service connection templates
- Add health checks, logging, and monitoring infrastructure
- Generate sample implementation code demonstrating component functionality

**Component Type Support:**

- **API Services**: REST APIs, GraphQL endpoints, gRPC services with appropriate frameworks
- **SPA Applications**: React, Angular, Vue.js frontends with TypeScript/JavaScript
- **Message Processors**: Event-driven applications for Service Bus, Event Hubs integration
- **Function Applications**: Azure Functions with appropriate triggers and bindings
- **Web Applications**: Server-rendered applications with authentication and data access
- **Background Services**: Worker services and batch processing applications

**Technology Integration:**

- Ensure generated code aligns with programming language preferences from user requirements
- Include framework-specific best practices and development patterns
- Add Azure service integrations based on architecture planning decisions
- Create environment configuration and deployment-ready setup

## Docker Configuration Generation

**Containerization Strategy Execution:**

Use Docker generation capabilities to create:

**Service Dockerfiles:**

- Generate optimized Dockerfiles for each containerizable service
- Use multi-stage builds for build optimization and security
- Include language-specific best practices and dependency management
- Add health check configurations and security hardening

**Docker Optimization:**

- Create .dockerignore files for efficient build contexts
- Implement layer caching strategies for faster builds
- Use minimal base images and non-root users
- Add container security scanning and vulnerability management

**Container Orchestration Support:**

- Ensure Docker configurations work with Azure Container Apps
- Add necessary labels and metadata for service discovery
- Include resource limit and scaling configurations
- Create container networking and communication patterns

## Infrastructure Template Generation

**Azure Resource Provisioning:**

Use infrastructure generation capabilities to create:

**IaC Rules and Resource Requirements:**

- Get IaC generation rules for Azure infrastructure best practices and coding standards
- Read required resources from the Infrastructure as Code File Checklist in application spec
- Apply generation rules to each required resource type for consistent implementation

**Modular Infrastructure Templates:**

- Generate main.bicep with subscription scope deployment
- Create generic resource modules for reusable Azure service patterns (e.g., `modules/containerapp.bicep`, `modules/storage.bicep`)
- Generate service-specific modules that reference generic modules with customized configurations
- Create resource group organization and management structures

**Core Infrastructure Components:**

- Add monitoring and logging infrastructure (Log Analytics, Application Insights)
- Include security and compliance configurations (Key Vault, managed identities)
- Generate database configurations (SQL, Cosmos DB, PostgreSQL, etc.)
- Add messaging infrastructure (Service Bus, Event Hubs, Event Grid)
- Create storage and caching configurations (Storage Accounts, Redis)

**Networking and Security:**

- Generate virtual network configurations for multi-service applications
- Add security group rules and access control
- Create private endpoint configurations where appropriate
- Include SSL/TLS certificate management

**Environment Management:**

- Create parameter files for different environments (dev, staging, production)
- Add configuration for scaling and performance optimization
- Include backup and disaster recovery configurations
- Generate monitoring and alerting rule definitions

## Azure.yaml Configuration Generation

**AZD Deployment Configuration:**

Use azure.yaml generation capabilities to create:

**Service Registration:**

- Map all application services to appropriate Azure hosting services
- Configure build and deployment instructions for each service
- Add dependency relationships and deployment ordering
- Include environment variable and configuration management

**Infrastructure Integration:**

- Reference generated Bicep templates and parameters
- Configure resource provisioning and dependency management
- Add post-deployment scripts and validation steps
- Include environment-specific overrides and configurations

**Development Workflow Integration:**

- Configure local development and testing scenarios
- Add debugging and troubleshooting capabilities
- Include CI/CD pipeline integration points
- Create deployment validation and rollback procedures

## Artifact Validation and Quality Assurance

**Syntax and Schema Validation:**

- Validate all Docker files for syntax correctness and best practices
- Compile and test all Bicep templates for Azure compatibility
- Validate azure.yaml against AZD schema and configuration requirements
- Test application scaffolding for build and runtime correctness

**Integration Testing:**

- Verify Docker containers build and run successfully
- Test infrastructure templates deploy without errors
- Validate azure.yaml configuration deploys all services correctly
- Ensure generated application code integrates with Azure services

**Security and Compliance Validation:**

- Scan Docker images for security vulnerabilities
- Review infrastructure templates for security best practices
- Validate access control and authentication configurations
- Ensure compliance with organizational security policies

## Artifact Generation Workflow

**Sequential Generation Process:**

```text
1. Architecture Review → 2. Existing Artifact Inventory → 3. Application Code Generation → 
4. Docker Configuration → 5. Infrastructure Templates → 6. Azure.yaml Configuration → 
7. Validation and Testing → 8. Documentation Update
```

**Iterative Refinement Process:**

```text
1. Identify specific artifact needs → 2. Generate targeted artifacts → 
3. Validate integration → 4. Refine and optimize → 5. Update documentation
```

**Error Recovery Process:**

```text
1. Identify generation failures → 2. Analyze dependency conflicts → 
3. Resolve compatibility issues → 4. Regenerate affected artifacts → 5. Revalidate
```

## Documentation and Completion Tracking

**Progress Documentation:**

Update application spec with artifact generation status:

```markdown
## Artifact Generation Status

### Application Scaffolding
- [x] Web frontend scaffolding created
- [x] API service template generated
- [x] Database integration configured
- [x] Authentication framework added

### Docker Configurations
- [x] Frontend service Dockerfile created
- [x] API service Dockerfile generated
- [x] .dockerignore files optimized
- [x] Health checks implemented

### Infrastructure Templates
- [x] Main deployment template created
- [x] Container Apps module generated
- [x] Database infrastructure configured
- [x] Monitoring and logging added

### Azure.yaml Configuration
- [x] Service definitions completed
- [x] Build configurations verified
- [x] Infrastructure references validated
- [x] Deployment workflow tested

### Validation Results
- [x] All Docker images build successfully
- [x] Infrastructure templates deploy without errors
- [x] Azure.yaml configuration validates
- [x] End-to-end deployment verified
```

**Success Criteria and Completion Checklist:**

- [ ] All required application code generated and functional
- [ ] Docker configurations created for all containerizable services
- [ ] Complete infrastructure templates generated and validated
- [ ] Azure.yaml configuration created and schema-compliant
- [ ] All artifacts integrate correctly with each other
- [ ] Generated code follows language and framework best practices
- [ ] Security and compliance requirements met
- [ ] Documentation updated with generation details
- [ ] End-to-end deployment tested and verified
- [ ] Project ready for development and deployment workflows
