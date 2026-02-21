# azd Docker Generation Instructions

✅ **Agent Task List**  

1. Read the **Docker File Generation Checklist** from `azd-arch-plan.md`
2. Identify containerizable services and required Docker files from the checklist
3. Detect programming language and framework for each containerizable service
4. Generate each Docker file specified in the checklist following language-specific best practices
5. Create .dockerignore files for build optimization
6. Implement health checks and security configurations
7. Update the Docker checklist section in existing `azd-arch-plan.md` by marking completed items as [x] while preserving existing content

📄 **Required Outputs**  

- All Docker files listed in the Docker File Generation Checklist from `azd-arch-plan.md`
- Dockerfiles created for all containerizable services
- .dockerignore files generated for each service
- Health check endpoints implemented
- Multi-stage builds with security best practices
- Update existing `azd-arch-plan.md` Docker checklist by marking completed items as [x] while preserving existing content

🧠 **Execution Guidelines**  

**Read Docker Checklist:**

- Read the "Docker File Generation Checklist" section from `azd-arch-plan.md`
- This checklist specifies exactly which Docker files need to be generated
- Use this as the authoritative source for what to create
- Follow the exact file paths specified in the checklist

**Generate Files in Order:**

- Create service Dockerfiles first (e.g., `{service-path}/Dockerfile`)
- Create corresponding .dockerignore files for each service (e.g., `{service-path}/.dockerignore`)
- Follow the exact file paths specified in the checklist from `azd-arch-plan.md`

**Containerization Candidates:**

- **Include:** Microservices, REST APIs, GraphQL services, web applications, background workers
- **Exclude:** Static websites (use Static Web Apps), Azure Functions (serverless), databases (use managed services)

**Language-Specific Dockerfile Patterns:**

**Node.js Applications:**

- Base image: `node:18-alpine`
- Multi-stage build (build + runtime)
- Copy package*.json first for layer caching
- Use `npm ci --only=production`
- Non-root user: `nodejs`
- Expose port 3000, health check `/health`

**Python Applications:**

- Base image: `python:3.11-slim`
- Environment: `PYTHONDONTWRITEBYTECODE=1`, `PYTHONUNBUFFERED=1`
- Copy requirements.txt first
- Use `pip install --no-cache-dir`
- Non-root user: `appuser`
- Expose port 8000, health check `/health`

**.NET Applications:**

- Build: `mcr.microsoft.com/dotnet/sdk:8.0`
- Runtime: `mcr.microsoft.com/dotnet/aspnet:8.0`
- Multi-stage: restore → build → publish → runtime
- Non-root user: `appuser`
- Expose port 8080, health check `/health`

**Java/Spring Boot:**

- Build: `openjdk:17-jdk-slim`, Runtime: `openjdk:17-jre-slim`
- Copy dependency files first for caching
- Non-root user: `appuser`
- Expose port 8080, actuator health check

**Security and Optimization Requirements:**

- Always use non-root users in production stage
- Use minimal base images (alpine, slim variants)
- Implement multi-stage builds to reduce size
- Include health check endpoints for Container Apps
- Set proper working directories and file permissions
- Use layer caching by copying dependency files first
- Never include secrets in container images

**.dockerignore Patterns:**

- Universal: `.git`, `README.md`, `.vscode/`, `.DS_Store`, `Dockerfile*`
- Node.js: `node_modules/`, `npm-debug.log*`, `coverage/`
- Python: `__pycache__/`, `*.pyc`, `venv/`, `.pytest_cache/`
- .NET: `bin/`, `obj/`, `*.user`, `packages/`
- Java: `target/`, `*.class`, `.mvn/repository`

**Health Check Implementation:**

- Endpoint: `/health` (standard convention)
- Response: JSON with status and timestamp
- HTTP Status: 200 for healthy, 503 for unhealthy
- Timeout: 3 seconds maximum
- Content: `{"status": "healthy", "timestamp": "ISO-8601"}`

📌 **Completion Checklist**  

- [ ] **Docker File Generation Checklist read** from `azd-arch-plan.md`
- [ ] **All files from Docker checklist generated** in the correct locations
- [ ] Dockerfiles created for all containerizable services identified in architecture planning
- [ ] .dockerignore files generated with appropriate exclusions for each language
- [ ] Multi-stage builds implemented to reduce image size
- [ ] Non-root users configured for security
- [ ] Health check endpoints implemented for all services
- [ ] Container startup optimization applied (dependency file caching)
- [ ] All Dockerfiles build successfully (`docker build` test)
- [ ] Security best practices followed (minimal images, no secrets)
- [ ] **Docker checklist in `azd-arch-plan.md` updated** by marking completed items as [x] while preserving existing content
