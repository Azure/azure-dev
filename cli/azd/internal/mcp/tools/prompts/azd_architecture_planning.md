# azd architecture Planning Instructions

✅ **Agent Task List**  

1. Read `azd-arch-plan.md` to understand discovered components
2. For each component, select optimal Azure service using selection criteria below
3. Plan containerization strategy for applicable services
4. Select appropriate database and messaging services
5. Design resource group organization and networking approach
6. Generate IaC file checklist based on selected Azure services
7. Generate Docker file checklist based on containerization strategy
8. Create `azd-arch-plan.md` if it doesn't exist, or update existing file with service mapping table, architecture decisions, IaC checklist, and Docker checklist while preserving existing content

📄 **Required Outputs**  

- Create `azd-arch-plan.md` if missing, or update existing file with Azure Service Mapping Table showing Component | Current Tech | Azure Service | Rationale
- Hosting strategy summary documenting decisions for each component (preserve existing content)
- Containerization plans for applicable services (preserve existing content)
- Infrastructure architecture design including resource organization and networking (preserve existing content)
- **IaC File Generation Checklist** listing all Bicep files that need to be created based on selected services (add to existing file)
- **Docker File Generation Checklist** listing all Docker files needed for containerized services (add to existing file)

🧠 **Execution Guidelines**  

**Azure Service Selection Criteria:**

**Azure Container Apps (PREFERRED)** - Use for microservices, containerized applications, event-driven workloads with auto-scaling needs

**Azure Kubernetes Service (AKS)** - Use for complex containerized applications requiring full Kubernetes control, advanced networking, custom operators

**Azure App Service** - Use for web applications, REST APIs needing specific runtime versions or Windows-specific features

**Azure Functions** - Use for event processing, scheduled tasks, lightweight APIs with pay-per-execution model

**Azure Static Web Apps** - Use for frontend SPAs, static sites, JAMstack applications with minimal backend needs

**Database Service Selection:**

- Azure SQL Database: SQL Server compatibility, complex queries, ACID compliance
- Azure Database for PostgreSQL/MySQL: Specific engine compatibility required
- Azure Cosmos DB: NoSQL requirements, global scale, flexible schemas
- Azure Cache for Redis: Application caching, session storage, real-time analytics

**Messaging Service Selection:**

- Azure Service Bus: Enterprise messaging, guaranteed delivery, complex routing
- Azure Event Hubs: High-throughput event streaming, telemetry ingestion
- Azure Event Grid: Event-driven architectures, reactive programming

**IaC File Checklist Generation:**

Based on selected Azure services, generate a checklist of required Bicep files to be created:

**Always Required:**

- [ ] `./infra/main.bicep` - Primary deployment template (subscription scope)
- [ ] `./infra/main.parameters.json` - Parameter defaults
- [ ] `./infra/modules/monitoring.bicep` - Log Analytics and Application Insights

**Service-Specific Modules (include based on service selection):**

- [ ] `./infra/modules/container-apps.bicep` - If Container Apps selected
- [ ] `./infra/modules/app-service.bicep` - If App Service selected  
- [ ] `./infra/modules/functions.bicep` - If Azure Functions selected
- [ ] `./infra/modules/static-web-app.bicep` - If Static Web Apps selected
- [ ] `./infra/modules/aks.bicep` - If AKS selected
- [ ] `./infra/modules/database.bicep` - If SQL/PostgreSQL/MySQL selected
- [ ] `./infra/modules/cosmosdb.bicep` - If Cosmos DB selected
- [ ] `./infra/modules/storage.bicep` - If Storage Account needed
- [ ] `./infra/modules/keyvault.bicep` - If Key Vault needed (recommended)
- [ ] `./infra/modules/servicebus.bicep` - If Service Bus selected
- [ ] `./infra/modules/eventhub.bicep` - If Event Hubs selected
- [ ] `./infra/modules/redis.bicep` - If Redis Cache selected
- [ ] `./infra/modules/container-registry.bicep` - If container services selected

**Example IaC Checklist Output:**

```markdown
## Infrastructure as Code File Checklist

Based on the selected Azure services, the following Bicep files need to be generated:

### Core Files (Always Required)
- [ ] `./infra/main.bicep` - Primary deployment template
- [ ] `./infra/main.parameters.json` - Parameter defaults
- [ ] `./infra/modules/monitoring.bicep` - Observability stack

### Service-Specific Modules
- [ ] `./infra/modules/container-apps.bicep` - For web API hosting
- [ ] `./infra/modules/database.bicep` - For PostgreSQL database
- [ ] `./infra/modules/keyvault.bicep` - For secrets management
- [ ] `./infra/modules/container-registry.bicep` - For container image storage

Total files to generate: 7
```

**Docker File Checklist Generation:**

Based on selected Azure services and containerization strategy, generate a checklist of required Docker files:

**Container-Based Services (include based on service selection):**

- [ ] `{service-path}/Dockerfile` - If Container Apps, AKS, or containerized App Service selected
- [ ] `{service-path}/.dockerignore` - For each containerized service

**Example Docker Checklist Output:**

```markdown
## Docker File Generation Checklist

Based on the containerization strategy, the following Docker files need to be generated:

### Service Dockerfiles
- [ ] `./api/Dockerfile` - For Node.js API service (Container Apps)
- [ ] `./api/.dockerignore` - Exclude unnecessary files from API container
- [ ] `./frontend/Dockerfile` - For React frontend (containerized App Service)
- [ ] `./frontend/.dockerignore` - Exclude unnecessary files from frontend container

Total Docker files to generate: 4
```

📌 **Completion Checklist**  

- [ ] Azure service selected for each discovered component with documented rationale
- [ ] Hosting strategies defined and documented in `azd-arch-plan.md`
- [ ] Containerization plans documented for applicable services
- [ ] Data storage strategies planned and documented
- [ ] Resource group organization strategy defined
- [ ] Integration patterns between services documented
- [ ] **IaC file checklist generated** and added to `azd-arch-plan.md` based on selected services
- [ ] **Docker file checklist generated** and added to `azd-arch-plan.md` based on containerization strategy
- [ ] `azd-arch-plan.md` created or updated while preserving existing content
- [ ] Ready to proceed to infrastructure generation phase
