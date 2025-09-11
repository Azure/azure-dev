# AZD Application Code Generation Instructions

**TASK:** Generate production-ready application scaffolding and starter code for all application components with Azure SDK integrations and deployment-ready configurations.

**SUCCESS CRITERIA:**

- Complete application scaffolding created in `src/<component>` directories for all identified components
- Framework-specific project files and dependency configurations generated
- Azure SDK integrations implemented for all required services
- Health checks, logging, and monitoring infrastructure included
- Configuration management and environment variable handling implemented
- Application spec updated with generated code structure

**VALIDATION REQUIRED:**

- All generated code compiles without errors for the target language/framework
- Azure SDK connections can be established (connection strings validate)
- Health check endpoints respond correctly
- Configuration follows security best practices (no hardcoded secrets)
- Generated code follows language-specific style guidelines

**COMPLETION CHECKLIST:**

- [ ] Read application specification for component requirements and stack decisions
- [ ] Generate scaffolding for each component in `src/<component>` structure
- [ ] Implement Azure SDK integrations for all mapped services
- [ ] Add health checks, logging, and monitoring to each component
- [ ] Update application spec with generated code documentation

## Critical Implementation Requirements

**Standard Directory Structure:**

```text
src/
├── <component-name>/         # One directory per application component
└── shared/                   # Shared libraries and utilities (if needed)
```

**Essential Scaffolding Elements:**

- **Entry Points**: Main application files with proper startup configuration
- **Health Checks**: `/health` endpoint for all services (Container Apps requirement)
- **Configuration**: Environment-based config (no hardcoded values)
- **Azure SDK Integration**: Connection code for all mapped Azure services
- **Error Handling**: Proper exception handling and logging
- **Build Scripts**: Language-appropriate build and dependency management

**Security Requirements:**

- No secrets or connection strings in code
- Use environment variables for all configuration
- Implement proper authentication integration points
- Follow language-specific security best practices
