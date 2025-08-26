# AZD Application Discovery and Analysis Instructions

✅ **Agent Task List**  

1. Check if `azd-arch-plan.md` exists and review previous analysis if present
2. Scan current directory recursively for all files and document structure
3. Identify programming languages, frameworks, and configuration files
4. Classify discovered components by type (web apps, APIs, databases, etc.)
5. Map dependencies and communication patterns between components
6. Create `azd-arch-plan.md` if it doesn't exist, or update existing file with complete discovery report while preserving existing content

📄 **Required Outputs**  

- Complete file system inventory documented in `azd-arch-plan.md` (create file if missing, update existing while preserving content)
- Component classification table with Type | Technology | Location | Purpose (add to existing file)
- Dependency map showing inter-component communication (add to existing file)
- External dependencies list with required environment variables (add to existing file)
- Discovery report ready for architecture planning phase

🧠 **Execution Guidelines**  

**File System Analysis - Document:**

- Programming languages and frameworks detected
- Configuration files (package.json, requirements.txt, pom.xml, Dockerfile, docker-compose.yml)
- API endpoints, service definitions, application entry points
- Database configurations and connection strings
- CI/CD pipeline files (.github/workflows, azure-pipelines.yml)
- Documentation files and existing architecture docs

**Component Classification Categories:**

- **Web Applications:** React/Angular/Vue.js apps, static sites, server-rendered apps
- **API Services:** REST APIs, GraphQL endpoints, gRPC services, microservices
- **Background Services:** Message queue processors, scheduled tasks, data pipelines
- **Databases:** SQL/NoSQL databases, caching layers, migration scripts
- **Messaging Systems:** Message queues, event streaming, pub/sub systems
- **AI/ML Components:** Models, inference endpoints, training pipelines
- **Supporting Services:** Authentication, logging, monitoring, configuration

**Dependency Analysis - Identify:**

- Internal dependencies (component-to-component communication)
- External dependencies (third-party APIs, SaaS services)
- Data dependencies (shared databases, file systems, caches)
- Configuration dependencies (shared settings, secrets, environment variables)
- Runtime dependencies (required services for startup)

**Communication Patterns to Document:**

- Synchronous HTTP/HTTPS calls
- Asynchronous messaging patterns
- Database connections and data access
- File system access patterns
- Caching patterns and session management
- Authentication and authorization flows

📌 **Completion Checklist**

- [ ] Complete inventory of all discoverable application artifacts documented
- [ ] All major application components identified and classified by type
- [ ] Component technologies and frameworks documented with file locations
- [ ] Dependencies mapped and communication patterns understood
- [ ] External services and APIs catalogued with requirements
- [ ] `azd-arch-plan.md` created or updated with comprehensive findings while preserving existing content
- [ ] Ready to proceed to architecture planning phase using `azd_architecture_planning` tool
