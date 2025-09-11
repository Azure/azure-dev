# AZD Application Discovery and Analysis Instructions

**TASK:** Scan and analyze the current workspace to identify all application components, technologies, dependencies, and communication patterns, documenting findings in the application specification.

**SUCCESS CRITERIA:**

- Complete file system inventory documented in application specification
- All application components identified and classified by type
- Component technologies and frameworks documented with file locations
- Dependencies mapped and communication patterns understood
- External services and APIs catalogued with requirements

**VALIDATION REQUIRED:**

- All major application artifacts discovered and documented
- Component classification is accurate and complete
- Dependencies and communication patterns are correctly identified
- Application specification is created or updated with comprehensive findings

**COMPLETION CHECKLIST:**

- [ ] Check if application specification exists and review previous analysis
- [ ] Scan workspace recursively for all application files and configurations
- [ ] Identify programming languages, frameworks, and configuration files
- [ ] Classify discovered components by type (web apps, APIs, databases, etc.)
- [ ] Map dependencies and communication patterns between components
- [ ] Create or update application specification with discovery findings

## Critical Discovery Requirements

**File System Analysis:**

- Programming languages and frameworks
- Configuration files (package.json, requirements.txt, Dockerfile, etc.)
- API endpoints, service definitions, application entry points
- Database configurations and connection patterns
- CI/CD pipeline files and deployment configurations

**Component Classification:**

- **Web Applications**: React/Angular/Vue.js apps, static sites, server-rendered apps
- **API Services**: REST APIs, GraphQL endpoints, gRPC services, microservices
- **Background Services**: Message processors, scheduled tasks, data pipelines
- **Databases**: SQL/NoSQL databases, caching layers, migration scripts
- **Supporting Services**: Authentication, logging, monitoring, configuration

**Dependency Analysis:**

- Internal dependencies (component-to-component communication)
- External dependencies (third-party APIs, SaaS services)
- Data dependencies (shared databases, file systems, caches)
- Configuration dependencies (shared settings, secrets, environment variables)
