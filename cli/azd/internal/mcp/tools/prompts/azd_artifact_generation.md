# AZD Artifact Generation Orchestration Instructions

**TASK:** Orchestrate the complete artifact generation process for AZD projects, generating infrastructure templates, application scaffolding, Docker configurations, and azure.yaml in the correct order with proper dependencies.

**SUCCESS CRITERIA:**

- Application scaffolding and starter code generated for new project components
- Docker configurations created for containerizable services
- Complete Bicep infrastructure templates generated in `./infra` directory
- Valid `azure.yaml` configuration file created mapping all services and resources
- All artifacts validated for syntax correctness and deployment compatibility

**VALIDATION REQUIRED:**

- All generated artifacts compile and validate without errors
- Infrastructure templates follow IaC generation rules and best practices
- Docker configurations are optimized and include health checks
- azure.yaml file validates against AZD schema
- Generated code is consistent with architecture planning decisions

**COMPLETION CHECKLIST:**

- [ ] Review application spec for complete project architecture and service specifications
- [ ] Generate infrastructure templates using IaC generation rules
- [ ] Generate application scaffolding for new project components
- [ ] Generate Docker configuration for containerizable services
- [ ] Generate azure.yaml configuration for AZD deployment
- [ ] Validate all generated artifacts for consistency and deployment readiness
- [ ] Update application spec with artifact generation status

## Critical Generation Requirements

**Generation Order:**

1. Infrastructure templates first (foundation)
2. Application scaffolding second (code structure)
3. Docker configurations third (containerization)
4. azure.yaml configuration last (deployment orchestration)

**Artifact Dependencies:**

- Infrastructure generation requires architecture planning and IaC rules
- Application scaffolding requires component definitions and technology stack
- Docker generation requires application code and containerization requirements
- azure.yaml requires all service definitions and hosting decisions

**Quality Requirements:**

- Preserve existing user customizations where possible
- Follow security best practices (no hardcoded secrets)
- Ensure deployment readiness and Azure compatibility
- Maintain consistency across all generated artifacts
