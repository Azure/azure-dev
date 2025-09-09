# AZD Application Code Generation Instructions

✅ **Agent Task List**  

1. Review application specification for application components and technology stack decisions
2. Identify preferred programming language and framework choices from user requirements
3. Analyze component types and generate appropriate application scaffolding for each
4. Create structured project layout in `src/<component>` directories
5. Generate framework-specific code with Azure SDK integrations
6. Include configuration management, logging, and health monitoring setup
7. Create build and deployment scripts for each component
8. Update application spec with generated code structure and next steps

📄 **Required Outputs**  

- Complete application scaffolding for all identified components in `src/<component>` structure
- Framework-appropriate project files, dependencies, and build configurations
- Azure SDK integrations and service connection code
- Health checks, logging, and monitoring infrastructure
- Configuration management and environment variable handling
- Sample implementation code demonstrating component functionality
- Updated application spec documenting generated code structure

🧠 **Execution Guidelines**  

**CRITICAL:** Generate production-ready application scaffolding that follows industry best practices for the selected programming language and framework. Include comprehensive Azure integrations and deployment-ready configurations.

## Project Structure and Organization

**Standard Directory Structure:**

```text
src/
├── <api-component>/          # REST API services
├── <spa-component>/          # Single Page Applications
├── <worker-component>/       # Background services and message processors
├── <function-component>/     # Serverless function applications
├── <webapp-component>/       # Server-rendered web applications
├── <batch-component>/        # Batch processing applications
└── shared/                   # Shared libraries and utilities
```

**Component Naming Conventions:**

- Use kebab-case for directory names (e.g., `user-api`, `admin-portal`, `order-processor`)
- Match component names defined in architecture planning phase
- Ensure names are descriptive and reflect component purpose
- Avoid generic names like `service1`, `app`, or `backend`

## Application Component Types and Scaffolding

### API-Based Projects (REST APIs, GraphQL, gRPC)

**Technology Stack Support:**

**Node.js/TypeScript APIs:**

- Express.js or Fastify framework setup
- TypeScript configuration with strict typing
- REST endpoint scaffolding with OpenAPI/Swagger documentation
- Middleware for authentication, logging, and error handling
- Database integration (Prisma, TypeORM, or native drivers)
- Azure SDK integrations (Storage, Key Vault, Service Bus)

**Python APIs:**

- FastAPI or Flask framework setup with async support
- Pydantic models for request/response validation
- SQLAlchemy or Django ORM for database operations
- Azure SDK for Python integrations
- Comprehensive error handling and logging
- Health check endpoints and metrics collection

**C# APIs:**

- ASP.NET Core Web API project setup
- Entity Framework Core for data access
- Dependency injection and configuration management
- Azure SDK for .NET integrations
- Comprehensive logging with Application Insights
- Authentication and authorization with Azure AD

**Java APIs:**

- Spring Boot application setup with appropriate starters
- Spring Data JPA for database operations
- Spring Security for authentication and authorization
- Azure SDK for Java integrations
- Comprehensive testing setup with JUnit and TestContainers

**Generated Components:**

- Controller/route definitions with CRUD operations
- Data models and entity definitions
- Service layer with business logic separation
- Repository pattern for data access
- Configuration classes for Azure service connections
- Health check endpoints (`/health`, `/ready`)
- API documentation setup and sample endpoints

### SPA-Based Projects (React, Angular, Vue.js)

**React/TypeScript SPAs:**
- Create React App or Vite setup with TypeScript
- React Router for navigation and routing
- State management (Redux Toolkit, Zustand, or Context API)
- API client setup with Axios or React Query
- Azure authentication integration (MSAL.js)
- Component library setup (Material-UI, Chakra UI, or custom)
- Build optimization and environment configuration

**Angular SPAs:**
- Angular CLI project setup with TypeScript
- Angular Material or PrimeNG component library
- Reactive forms and validation
- HTTP client with interceptors for authentication
- Azure AD authentication with MSAL Angular
- State management with NgRx (for complex apps)
- PWA capabilities and service worker setup

**Vue.js SPAs:**
- Vue 3 with Composition API and TypeScript
- Vue Router for navigation
- Pinia for state management
- Azure authentication integration
- Component library integration (Vuetify, Quasar)
- Build tools setup (Vite or Vue CLI)

**Generated Components:**
- Application shell with navigation and layout
- Authentication components and route guards
- Sample pages and component structure
- API service classes for backend communication
- Configuration management for environments
- Build and deployment scripts
- Testing setup (Jest, Cypress, or Playwright)

### Message-Based Projects (Event-Driven Applications)

**Event Processing Applications:**
- Message handler scaffolding for Service Bus, Event Hubs, or Event Grid
- Dead letter queue handling and retry mechanisms
- Message serialization/deserialization utilities
- Batch processing and scaling configurations
- Monitoring and telemetry collection

**Worker Service Applications:**
- Background service templates for long-running processes
- Queue-based work distribution patterns
- Health monitoring and graceful shutdown handling
- Configuration for scaling and resource management
- Integration with Azure Monitor and Application Insights

**Generated Components:**
- Message handler classes with typed message contracts
- Queue/topic configuration and connection management
- Error handling and retry policy implementations
- Metrics collection and performance monitoring
- Configuration for different message sources (Service Bus, Event Hubs, etc.)

### Function-Based Projects (Serverless Applications)

**Azure Functions:**
- Function app project setup with appropriate triggers
- HTTP triggers for API endpoints
- Timer triggers for scheduled operations
- Service Bus/Event Hub triggers for message processing
- Blob/Queue triggers for storage events
- Dependency injection and configuration setup

**Generated Components:**
- Function definitions with appropriate triggers and bindings
- Shared utilities and helper functions
- Configuration for local development and testing
- Integration with Azure services through bindings
- Logging and monitoring setup

### Web Application Projects (Server-Rendered)

**Server-Rendered Applications:**
- Framework setup (Next.js, Nuxt.js, or traditional server frameworks)
- Authentication and session management
- Database integration and ORM setup
- Template engine configuration and layouts
- Static asset management and optimization

**Generated Components:**
- Page templates and routing configuration
- Authentication middleware and user management
- Database models and migration scripts
- Asset pipeline and build configuration
- Environment-specific configuration management

### Batch Processing Projects

**Data Processing Applications:**
- Batch job frameworks (Spring Batch, Apache Airflow workflows)
- Data pipeline scaffolding for ETL operations
- Integration with Azure Data Factory or Azure Batch
- Error handling and job monitoring
- Scalable processing patterns

**Generated Components:**
- Job definition classes and processing logic
- Data source connectors and transformation utilities
- Scheduling and workflow management
- Monitoring and alerting configuration

## Programming Language and Framework Selection

**Language-Specific Best Practices:**

**TypeScript/JavaScript:**
- Strict TypeScript configuration with comprehensive type checking
- ESLint and Prettier configuration for code quality
- Modern ES6+ features and async/await patterns
- Package.json with appropriate scripts and dependencies
- Environment configuration with dotenv or similar

**Python:**
- Virtual environment setup and requirements management
- Type hints and mypy configuration for static typing
- Black and flake8 for code formatting and linting
- pytest setup for comprehensive testing
- Environment configuration with python-dotenv

**C#:**
- Modern C# features and nullable reference types
- Project file configuration with appropriate package references
- Dependency injection and configuration patterns
- Comprehensive logging with structured logging
- Environment-specific appsettings.json files

**Java:**
- Maven or Gradle build configuration
- Spring Boot best practices and auto-configuration
- Comprehensive testing with JUnit and Mockito
- Logging configuration with Logback or Log4j2
- Environment configuration with profiles

## Azure SDK Integration and Service Connections

**Common Azure Service Integrations:**

**Authentication and Identity:**
- Azure AD integration for user authentication
- Managed Identity configuration for service-to-service auth
- Key Vault integration for secrets management
- Role-based access control (RBAC) setup

**Data and Storage:**
- Azure SQL Database or Cosmos DB connection setup
- Azure Storage (Blob, Queue, Table) integration
- Redis Cache configuration and usage patterns
- Database migration and seeding scripts

**Messaging and Events:**
- Service Bus queue and topic integration
- Event Hubs producer and consumer setup
- Event Grid subscription and handler configuration
- Dead letter queue and error handling patterns

**Monitoring and Observability:**
- Application Insights integration and telemetry
- Structured logging with correlation IDs
- Health check endpoints and monitoring
- Custom metrics and performance counters

## Configuration Management and Environment Setup

**Environment Configuration Patterns:**

**Local Development:**
- Local environment setup with development containers
- Mock services and local testing configuration
- Debug configuration and hot reload setup
- Local database and messaging emulators

**Azure Environment Integration:**
- Environment-specific configuration files
- Azure App Configuration or Key Vault integration
- Managed Identity configuration for Azure services
- Connection string management and rotation

**Security and Compliance:**
- Secure coding practices and input validation
- Authentication and authorization implementation
- Data encryption and secure communication
- Compliance with security best practices

## Code Generation Workflow

**Component Analysis and Planning:**

1. **Read Architecture Decisions**: Review application spec for component definitions and technology choices
2. **Identify Component Types**: Classify each component (API, SPA, Worker, Function, etc.)
3. **Determine Language/Framework**: Use user preferences from intent discovery or make appropriate recommendations
4. **Plan Integration Points**: Identify shared libraries, communication patterns, and service dependencies

**Scaffolding Generation Process:**

1. **Create Directory Structure**: Establish `src/<component>` layout for each component
2. **Generate Framework Setup**: Create appropriate project files, configuration, and dependencies
3. **Add Azure Integrations**: Include SDK setup and service connection code
4. **Create Sample Implementation**: Generate basic functionality demonstrating component purpose
5. **Add Supporting Infrastructure**: Include testing, logging, monitoring, and deployment setup

**Quality Assurance and Validation:**

- Ensure generated code compiles and runs without errors
- Validate Azure SDK integrations and authentication setup
- Test basic functionality and health check endpoints
- Verify configuration management and environment handling
- Confirm alignment with architecture decisions and requirements

## Documentation and Next Steps

**Code Structure Documentation:**

Update application spec with generated code details:

```markdown
## Generated Application Code

### Component Structure
- **API Services**: [List of generated API components with technologies]
- **Frontend Applications**: [List of generated SPA components with frameworks]
- **Background Services**: [List of generated worker/message components]
- **Function Applications**: [List of generated serverless components]

### Technology Stack
- **Primary Language**: [Selected programming language]
- **Frameworks**: [List of frameworks used per component type]
- **Azure SDK Integrations**: [List of Azure services integrated]

### Development Setup
- **Local Development**: Instructions for running components locally
- **Testing Strategy**: Overview of testing setup and practices
- **Build and Deployment**: Summary of build processes and deployment configuration

### Next Steps
- [Specific guidance for development team to continue implementation]
- [Testing and validation recommendations]
- [Deployment and monitoring setup instructions]
```

**Success Criteria:**

- [ ] All application components have appropriate scaffolding generated
- [ ] Code follows language and framework best practices
- [ ] Azure SDK integrations are properly configured
- [ ] Configuration management supports multiple environments
- [ ] Health checks and monitoring are implemented
- [ ] Build and deployment scripts are functional
- [ ] Documentation provides clear next steps for development
- [ ] Generated code aligns with architecture decisions and user requirements
