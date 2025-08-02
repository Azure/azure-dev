# AZD Docker Generation Tool

This specialized tool generates Dockerfiles and container configurations for Azure Developer CLI (AZD) projects.

## Overview

Generate optimized Dockerfiles for different programming languages and frameworks with Azure Container Apps best practices.

**IMPORTANT:** Before starting, check if `azd-arch-plan.md` exists in your current working directory. If it exists, review it to understand discovered services and containerization requirements.

## Success Criteria

- [ ] Dockerfiles created for all containerizable services
- [ ] .dockerignore files generated for build optimization
- [ ] Health checks and security configurations implemented
- [ ] Multi-stage builds used where appropriate
- [ ] Azure Container Apps best practices followed

## Containerization Requirements Analysis

**REQUIRED ACTIONS:**

1. **Identify Containerization Candidates:**
   - Microservices and APIs (REST, GraphQL, gRPC)
   - Web applications needing runtime flexibility
   - Background services and workers
   - Custom applications with specific runtime requirements

2. **Services That Don't Need Containerization:**
   - Static websites (use Azure Static Web Apps)
   - Azure Functions (serverless, managed runtime)
   - Database services (use managed Azure databases)

3. **Language and Framework Detection:**
   - Programming language (Node.js, Python, .NET, Java, Go, etc.)
   - Framework type (Express, FastAPI, ASP.NET Core, Spring Boot)
   - Build requirements (npm, pip, dotnet, maven, gradle)
   - Runtime dependencies and port configurations
- **Programming language** (Node.js, Python, .NET, Java, Go, etc.)

## Dockerfile Generation Requirements

**REQUIRED ACTIONS:**

For each containerizable service, generate optimized Dockerfiles following these patterns:

### Language-Specific Requirements

**Node.js Applications:**
- Use `node:18-alpine` base image
- Implement multi-stage build (build + runtime)
- Copy package*.json first for layer caching
- Use `npm ci --only=production`
- Create non-root user (`nodejs`)
- Expose appropriate port (typically 3000)
- Include health check endpoint
- Use `CMD ["npm", "start"]`

**Python Applications:**
- Use `python:3.11-slim` base image
- Set environment variables: `PYTHONDONTWRITEBYTECODE=1`, `PYTHONUNBUFFERED=1`
- Copy requirements.txt first for caching
- Use `pip install --no-cache-dir`
- Create non-root user (`appuser`)
- Expose appropriate port (typically 8000)
- Include health check endpoint
- Use appropriate startup command (uvicorn, gunicorn, etc.)

**.NET Applications:**
- Use `mcr.microsoft.com/dotnet/sdk:8.0` for build stage
- Use `mcr.microsoft.com/dotnet/aspnet:8.0` for runtime
- Multi-stage build: restore → build → publish → runtime
- Copy .csproj first for layer caching
- Create non-root user (`appuser`)
- Expose port 8080 (standard for .NET in containers)
- Include health check endpoint
- Use `ENTRYPOINT ["dotnet", "AppName.dll"]`

**Java/Spring Boot Applications:**
- Use `openjdk:17-jdk-slim` for build, `openjdk:17-jre-slim` for runtime
- Copy pom.xml/build.gradle first for dependency caching
- Multi-stage build pattern
- Create non-root user (`appuser`)
- Expose port 8080
- Include actuator health check
- Use `CMD ["java", "-jar", "app.jar"]`

## Security and Best Practices

**CRITICAL REQUIREMENTS:**

- **Always use non-root users** in production stage
- **Use minimal base images** (alpine, slim variants)
- **Implement multi-stage builds** to reduce image size
- **Include health check endpoints** for Container Apps
- **Set proper working directories** and file permissions
- **Use layer caching** by copying dependency files first
- **Never include secrets** in container images

## .dockerignore Requirements

**REQUIRED ACTIONS:**

Create .dockerignore files with these patterns:

**Universal Exclusions:**
- Version control: `.git`, `.gitignore`
- Documentation: `README.md`, `*.md`
- IDE files: `.vscode/`, `.idea/`, `*.swp`
- OS files: `.DS_Store`, `Thumbs.db`
- Docker files: `Dockerfile*`, `.dockerignore`, `docker-compose*.yml`
- Build artifacts and logs

**Language-Specific Exclusions:**
- **Node.js:** `node_modules/`, `npm-debug.log*`, `coverage/`, `dist/`
- **Python:** `__pycache__/`, `*.pyc`, `venv/`, `.pytest_cache/`, `dist/`
- **.NET:** `bin/`, `obj/`, `*.user`, `packages/`, `.vs/`
- **Java:** `target/`, `*.class`, `.mvn/repository`

## Health Check Implementation

**REQUIRED ACTIONS:**

Each containerized service must include a health check endpoint:

- **Endpoint:** `/health` (standard convention)
- **Response:** JSON with status and timestamp
- **HTTP Status:** 200 for healthy, 503 for unhealthy
- **Timeout:** 3 seconds maximum response time
- **Content:** `{"status": "healthy", "timestamp": "ISO-8601"}`

## Container Optimization

**REQUIRED OPTIMIZATIONS:**

- Use multi-stage builds to exclude build tools from production images
- Copy package/dependency files before source code for better caching
- Combine RUN commands to reduce layers
- Clean package manager caches in same RUN command
- Use specific versions for base images (avoid `latest`)
- Set resource limits appropriate for Azure Container Apps

## Validation and Testing

**VALIDATION REQUIREMENTS:**

- All Dockerfiles must build successfully: `docker build -t test-image .`
- Containers must run with non-root users
- Health checks must respond correctly
- Images should be optimized for size (use `docker images` to verify)
- Container startup time should be reasonable (<30 seconds)

## Update Documentation

**REQUIRED ACTIONS:**

Update `azd-arch-plan.md` with:

- List of generated Dockerfiles and their languages
- Container configurations (ports, health checks, users)
- Security implementations (non-root users, minimal images)
- Build optimizations applied
- Local testing commands

## Next Steps

After Docker generation is complete:

1. Test all containers build successfully locally
2. Integrate Dockerfile paths into `azure.yaml` service definitions
3. Configure Container Apps infrastructure to use these images
4. Set up Azure Container Registry for image storage

**IMPORTANT:** Reference existing tools for schema validation. For azure.yaml updates, use the `azd_azure_yaml_generation` tool. For infrastructure setup, use the `azd_infrastructure_generation` tool.
