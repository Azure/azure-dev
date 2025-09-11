# AZD Azure.yaml Generation Instructions

**TASK:** Create a complete and valid `azure.yaml` file in the root directory that maps all application services to their Azure hosting services with proper build and deployment configurations.

**SUCCESS CRITERIA:**

- Valid `azure.yaml` file created in root directory
- Service configurations match Azure service selections from architecture planning
- Build and deployment instructions complete for all services
- All service paths and Docker configurations reference existing files correctly

**VALIDATION REQUIRED:**

- File validates against AZD schema using available tools
- All `project` paths point to existing directories
- All `docker.path` references point to existing Dockerfiles relative to service project path
- Host types match architecture decisions (containerapp, appservice, function, staticwebapp)
- Service names are alphanumeric with hyphens only

**COMPLETION CHECKLIST:**

- [ ] Check if application spec exists and review architecture decisions
- [ ] Identify all application services and their hosting requirements
- [ ] Create complete `azure.yaml` file with service configurations
- [ ] Validate file against AZD schema
- [ ] Update application spec with configuration details

## Critical Configuration Requirements

**Service Type Mappings:**

- **Azure Container Apps**: `host: containerapp` (for APIs, microservices, containerized apps)
- **Azure App Service**: `host: appservice` (for traditional web apps)
- **Azure Functions**: `host: function` (for serverless workloads)
- **Azure Static Web Apps**: `host: staticwebapp` (for SPAs, static sites)
- **Azure Kubernetes Service**: `host: aks` (for Kubernetes)

**Path Configuration Rules:**

- Service `project` paths must point to existing directories
- Docker `path` is relative to the service's project directory, not repository root
- Language must match detected programming language
- `dist` paths must match build output directories for static apps

**Required Configuration Elements:**

- Service names (alphanumeric with hyphens only)
- Project paths (relative to repository root)
- Language specification
- Host type selection
- Build and deployment configurations
