# AZD Docker Generation Instructions

**TASK:** Generate optimized Dockerfiles and .dockerignore files for all containerizable services based on the Docker File Generation Checklist from the application spec.

**SUCCESS CRITERIA:**

- All Docker files listed in the application spec checklist are generated
- Dockerfiles follow language-specific best practices with multi-stage builds
- .dockerignore files created for build optimization
- Health check endpoints implemented for Container Apps compatibility
- Security configurations and non-root users implemented

**VALIDATION REQUIRED:**

- All generated Dockerfiles build successfully without errors
- Health check endpoints respond correctly
- Docker images follow security best practices (non-root users, minimal base images)
- .dockerignore patterns exclude unnecessary files for efficient builds
- Application spec Docker checklist is updated with completion status

**COMPLETION CHECKLIST:**

- [ ] Read Docker File Generation Checklist from application spec
- [ ] Identify containerizable services and required Docker files
- [ ] Generate Dockerfiles following language-specific best practices
- [ ] Create .dockerignore files for build optimization
- [ ] Implement health checks and security configurations
- [ ] Update Docker checklist in application spec marking completed items

## Critical Docker Requirements

**Containerization Candidates:**

- **Include**: Microservices, REST APIs, GraphQL services, web applications, background workers
- **Exclude**: Static websites (use Static Web Apps), Azure Functions (serverless), databases (use managed services)

**Essential Dockerfile Elements:**

- Multi-stage builds (build + runtime stages)
- Minimal base images (alpine, slim variants)
- Non-root users in production stage
- Health check endpoints (`/health` for Container Apps)
- Proper layer caching (copy dependency files first)
- Security hardening and file permissions

**Language-Specific Patterns:**

- **Node.js**: `node:18-alpine` base, npm ci, port 3000
- **Python**: `python:3.11-slim` base, pip install, port 8000
- **.NET**: Multi-stage with SDK/runtime separation, port 8080
- **Java**: OpenJDK slim variants, dependency optimization

**.dockerignore Requirements:**

- Universal exclusions: `.git`, `README.md`, `.vscode/`, `Dockerfile*`
- Language-specific: `node_modules/`, `__pycache__/`, `bin/obj/`, `target/`
