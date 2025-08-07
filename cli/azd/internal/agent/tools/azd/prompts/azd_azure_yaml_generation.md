# AZD Azure.yaml Generation Instructions

✅ **Agent Task List**  

1. Check if `azd-arch-plan.md` exists and review architecture decisions
2. Identify all application services (frontend, backend, functions, etc.)
3. Determine hosting requirements for each service based on Azure service selections
4. Analyze build requirements (language, package manager, build commands)
5. Create complete `azure.yaml` file in root directory following required patterns
6. Validate file against AZD schema using available tools
7. Update existing `azd-arch-plan.md` with generated configuration details while preserving existing content

📄 **Required Outputs**  

- Valid `azure.yaml` file created in root directory
- Service configurations matching Azure service selections from architecture planning
- Build and deployment instructions for all services
- Configuration validated against AZD schema
- Update existing `azd-arch-plan.md` with configuration details while preserving existing content

🧠 **Execution Guidelines**  

**Service Analysis Requirements:**

Identify and configure these service types:

- **Frontend applications:** React, Angular, Vue.js, static sites
- **Backend services:** REST APIs, microservices, GraphQL, gRPC
- **Function-based services:** Azure Functions for event-driven workloads
- **Background services:** Workers and long-running processes

**Hosting Configuration Patterns:**

**Azure Container Apps** (for microservices, APIs, containerized apps):

```yaml
services:
  api:
    project: ./src/api
    language: js
    host: containerapp
    docker:
      path: ./Dockerfile
```

**Azure App Service** (for traditional web apps):

```yaml
services:
  webapp:
    project: ./src/webapp
    language: js
    host: appservice
```

**Azure Functions** (for serverless workloads):

```yaml
services:
  functions:
    project: ./src/functions
    language: js
    host: function
```

**Azure Static Web Apps** (for SPAs, static sites):

```yaml
services:
  frontend:
    project: ./src/frontend
    language: js
    host: staticwebapp
    dist: build
```

**Critical Configuration Requirements:**

- Service names must be alphanumeric with hyphens only
- All `project` paths must point to existing directories
- All `docker.path` references must point to existing Dockerfiles **relative to the service project path**
- Host types must be: `containerapp`, `appservice`, `function`, or `staticwebapp`
- Language must match detected programming language
- `dist` paths must match build output directories

**Important Note:** For Container Apps with Docker configurations, the `docker.path` is relative to the service's `project` directory, not the repository root. For example, if your service project is `./src/api` and the Dockerfile is located at `./src/api/Dockerfile`, the `docker.path` should be `./Dockerfile`.

**Advanced Configuration Options:**

- Environment variables using `${VARIABLE_NAME}` syntax
- Custom commands using hooks (prebuild, postbuild, prepackage, postpackage, preprovision, postprovision)
- Service dependencies and startup order

📌 **Completion Checklist**

- [ ] Valid `azure.yaml` file created in root directory
- [ ] All discovered services properly configured with correct host types
- [ ] Service hosting configurations match Azure service selections from architecture planning
- [ ] Build and deployment instructions complete for all services
- [ ] File validates against any available AZD schema tools
- [ ] `azd-arch-plan.md` updated with configuration details while preserving existing content
