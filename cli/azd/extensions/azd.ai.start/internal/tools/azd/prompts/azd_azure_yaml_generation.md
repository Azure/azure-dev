# AZD Azure.yaml Generation Tool

This specialized tool generates the `azure.yaml` configuration file for Azure Developer CLI (AZD) projects.

## Overview

Generate a valid `azure.yaml` configuration file with proper service hosting, build, and deployment settings.

**IMPORTANT:** Before starting, check if `azd-arch-plan.md` exists in your current working directory. If it exists, review it to understand previous analysis and architecture decisions. Use the existing `azd_yaml_schema` tool for schema validation.

## Success Criteria

- [ ] Valid `azure.yaml` file created in root directory
- [ ] All application services properly configured
- [ ] Service hosting configurations match Azure service selections
- [ ] Build and deployment instructions complete
- [ ] File validates against AZD schema (use `azd_yaml_schema` tool)

## Service Analysis Requirements

**REQUIRED ACTIONS:**

1. **Identify Application Services:**
   - Frontend applications (React, Angular, Vue.js, static sites)
   - Backend services (REST APIs, microservices, GraphQL, gRPC)
   - Function-based services (Azure Functions)
   - Background services and workers

2. **Determine Hosting Requirements:**
   - **Container Apps:** Microservices, APIs, containerized web apps
   - **App Service:** Traditional web applications, APIs
   - **Static Web Apps:** Frontend SPAs, static sites
   - **Functions:** Event-driven, serverless workloads

3. **Analyze Build Requirements:**
   - Programming language and framework
   - Package manager (npm, pip, dotnet, maven)
   - Build commands and output directories
   - Dependency management needs

## Azure.yaml Configuration Requirements

**REQUIRED ACTIONS:**

Create a complete `azure.yaml` file in the root directory following these patterns:

### Basic Structure Requirements

**IMPORTANT:** Use the `azd_yaml_schema` tool for complete schema definition, structure requirements, and validation rules.

Basic structure:

```yaml
name: [project-name]
services:
  # Service configurations
infra:
  provider: bicep
  path: infra
```

### Service Configuration Patterns

**Azure Container Apps (for microservices, APIs, containerized apps):**

```yaml
services:
  api:
    project: ./src/api
    language: js
    host: containerapp
    docker:
      path: ./src/api/Dockerfile
```

**Azure App Service (for traditional web apps):**

```yaml
services:
  webapp:
    project: ./src/webapp
    language: js
    host: appservice
```

**Azure Functions (for serverless workloads):**

```yaml
services:
  functions:
    project: ./src/functions
    language: js
    host: function
```

**Azure Static Web Apps (for SPAs, static sites):**

```yaml
services:
  frontend:
    project: ./src/frontend
    language: js
    host: staticwebapp
    dist: build
```

### Advanced Configuration Options

**Environment Variables:**

```yaml
services:
  api:
    env:
      - name: NODE_ENV
        value: production
      - name: DATABASE_URL
        value: "{{ .Env.DATABASE_URL }}"
```

**Custom Build Commands:**

```yaml
services:
  frontend:
    hooks:
      prebuild:
        posix: npm install
      build:
        posix: npm run build
```

## Configuration Requirements

**CRITICAL REQUIREMENTS:**

- Service names must be valid Azure resource names (alphanumeric, hyphens only)
- All `project` paths must point to existing directories
- All `docker.path` references must point to existing Dockerfiles
- Host types must be: `containerapp`, `appservice`, `function`, or `staticwebapp`
- Language must match detected programming language
- `dist` paths must match build output directories

## Validation Requirements

**VALIDATION STEPS:**

1. **Schema Validation:** Use `azd_yaml_schema` tool for authoritative schema validation
2. **Path Validation:** Ensure all referenced paths exist
3. **Configuration Testing:** Run `azd show` to test service discovery

**Validation Commands:**

```bash
# Validate configuration
azd config show

# Test service discovery
azd show
```

## Common Patterns

**Multi-Service Microservices:**

- Frontend: Static Web App
- APIs: Container Apps with Dockerfiles
- Background Services: Container Apps or Functions

**Full-Stack Application:**

- Frontend: Static Web App
- Backend: Container App or App Service

**Serverless Application:**

- Frontend: Static Web App
- APIs: Azure Functions

## Update Documentation

**REQUIRED ACTIONS:**

Update `azd-arch-plan.md` with:

- Generated azure.yaml location and schema version
- Service configuration table (service, type, host, language, path)
- Hosting strategy summary by Azure service type
- Build and deployment configuration decisions
- Docker configuration details
- Validation results

## Next Steps

After azure.yaml generation is complete:

1. Validate configuration using `azd_yaml_schema` tool
2. Test service discovery with `azd show`

**IMPORTANT:** Reference existing tools for specific functionality. Use `azd_yaml_schema` for schema validation.
