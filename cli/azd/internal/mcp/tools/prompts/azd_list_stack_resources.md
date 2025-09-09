# AZD Stack Resources List

✅ **Agent Task List**  

1. Review the baseline Azure resources required for all AZD projects
2. Review the stack-specific Azure resources for the selected technology stack
3. Understand the purpose and configuration of each resource type
4. Use these resource definitions for architecture planning and infrastructure generation

📄 **Required Outputs**  

- Complete understanding of baseline resources shared across all stacks
- Detailed knowledge of stack-specific resources and their purposes
- Azure resource type identifiers for precise infrastructure generation
- Resource relationships and dependencies for proper deployment ordering

🧠 **Execution Guidelines**  

**CRITICAL:** This tool provides the definitive list of Azure resources required for each technology stack. These resource definitions should be used as the foundation for all architecture planning and infrastructure generation activities.

## Resource Categories

### Baseline Resources

All AZD projects receive a standard set of baseline resources that provide essential capabilities like logging, monitoring, configuration management, and secure storage. These resources are provisioned regardless of the selected technology stack.

### Stack-Specific Resources

Each technology stack (Containers, Serverless, Logic Apps) includes additional resources optimized for that particular architectural approach. These resources provide the compute and orchestration capabilities specific to the chosen stack.

## Usage in Architecture Planning

When proceeding to architecture planning, use these resource definitions to:

- Map application components to appropriate Azure resources
- Plan resource group organization and naming conventions
- Determine resource dependencies and deployment sequencing
- Configure monitoring and logging integration
- Set up security and access control policies

## Infrastructure Generation Integration

During infrastructure generation, these resource specifications provide:

- Exact Azure resource type identifiers for Bicep templates
- Required configuration parameters for each resource
- Baseline security and monitoring configurations
- Integration patterns between resources
