# AZD Stack Resources List

**TASK:** Provide the definitive list of Azure resources required for each technology stack (baseline and stack-specific) to guide architecture planning and infrastructure generation.

**SUCCESS CRITERIA:**

- Complete understanding of baseline resources shared across all stacks
- Detailed knowledge of stack-specific resources and their purposes
- Azure resource type identifiers available for precise infrastructure generation
- Resource relationships and dependencies understood for proper deployment ordering

**VALIDATION REQUIRED:**

- Resource definitions are current and match latest Azure service capabilities
- Stack-specific resources align with architectural approaches
- Resource dependencies and integration patterns are clearly documented
- Resource specifications support both planning and generation phases

**COMPLETION CHECKLIST:**

- [ ] Review baseline Azure resources required for all AZD projects
- [ ] Review stack-specific Azure resources for the selected technology stack
- [ ] Understand the purpose and configuration of each resource type
- [ ] Use these resource definitions for architecture planning and infrastructure generation
- [ ] Update application specification to include all required files within IaC checklist

## Critical Resource Categories

**Baseline Resources (All Stacks):**

- Essential capabilities for logging, monitoring, configuration management, and secure storage
- Provisioned regardless of selected technology stack
- Foundation for all AZD projects

**Stack-Specific Resources:**

- **Containers Stack**: Resources optimized for containerized workloads
- **Serverless Stack**: Resources for event-driven and function-based architectures  
- **Logic Apps Stack**: Resources for workflow automation and integration scenarios

**Usage Guidelines:**

- **Architecture Planning**: Map application components to appropriate Azure resources
- **Infrastructure Generation**: Provide exact resource type identifiers and configurations
- **Resource Organization**: Plan resource group structure and naming conventions
- **Deployment Sequencing**: Determine resource dependencies and deployment order
- **Integration Patterns**: Configure monitoring, security, and access control
