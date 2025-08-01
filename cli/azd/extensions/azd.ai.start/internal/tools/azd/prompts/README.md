# AZD Modular Tools Overview

This document provides an overview of the modular AZD initialization tools that replace the monolithic `azd_plan_init` tool. Each tool is designed to be used independently or as part of a complete AZD migration workflow.

## Tool Structure

The AZD initialization process has been broken down into focused, modular tools:

### 1. Discovery and Analysis Tool (`azd_discovery_analysis`)

**Purpose:** Analyze applications and identify components and dependencies
**Use When:** Starting a new AZD migration or need to understand an existing codebase
**Output:** Component inventory and dependency mapping in `azd-arch-plan.md`

### 2. Architecture Planning Tool (`azd_architecture_planning`)

**Purpose:** Select Azure services and plan hosting strategies
**Use When:** You have discovered components and need to plan Azure service mapping
**Prerequisites:** Completed discovery and analysis
**Output:** Architecture decisions and service selections in `azd-arch-plan.md`

### 3. Azure.yaml Generation Tool (`azd_azure_yaml_generation`)

**Purpose:** Generate azure.yaml service configuration file
**Use When:** You need to create or update just the service definitions
**Prerequisites:** Understanding of application services and hosting requirements
**Output:** Valid `azure.yaml` file

### 4. Infrastructure Generation Tool (`azd_infrastructure_generation`)

**Purpose:** Generate Bicep infrastructure templates
**Use When:** You need to create or update just the infrastructure components
**Prerequisites:** Architecture decisions about Azure services
**Output:** Complete Bicep template structure

### 5. Docker Generation Tool (`azd_docker_generation`)

**Purpose:** Generate Dockerfiles and container configurations
**Use When:** You need containerization for your services
**Prerequisites:** Understanding of application services and containerization needs
**Output:** Optimized Dockerfiles and .dockerignore files

### 6. Project Validation Tool (`azd_project_validation`)

**Purpose:** Validate the complete AZD project setup and configuration
**Use When:** All files are generated and you need to validate the setup
**Prerequisites:** All configuration files generated
**Output:** Validation report and ready-to-deploy confirmation

## Complete Workflow

For a full AZD migration, use the tools in this sequence:

```
1. azd_discovery_analysis
   ↓
2. azd_architecture_planning  
   ↓
3a. azd_azure_yaml_generation
3b. azd_infrastructure_generation
3c. azd_docker_generation (if containerization needed)
   ↓
4. azd_project_validation
```

## Selective Usage

You can also use individual tools for specific tasks:

**Generate only azure.yaml:**
```
azd_discovery_analysis → azd_azure_yaml_generation
```

**Generate only infrastructure:**
```
azd_architecture_planning → azd_infrastructure_generation
```

**Add containerization:**
```
azd_docker_generation (based on existing analysis)
```

**Validate existing project:**
```
azd_project_validation (for validation and testing)
```

## Central Planning Document

All tools use `azd-arch-plan.md` as the central planning document:

- **Created by:** Discovery and Analysis tool
- **Updated by:** All subsequent tools
- **Purpose:** Track progress, document decisions, and maintain project state
- **Location:** Current working directory

## Key Features

### Modular Design
- Each tool has a specific responsibility
- Tools can be used independently or together
- Clear prerequisites and outputs
- Consistent documentation patterns

### Azure Best Practices
- All tools implement Azure best practices
- Security-first approach
- Cost optimization considerations
- Operational excellence patterns

### LLM Optimized
- Clear, actionable instructions
- Structured output formats
- Comprehensive validation steps
- Troubleshooting guidance

### Progress Tracking
- Checkboxes for completed actions
- Clear success criteria
- Validation requirements
- Next step guidance

## Tool Selection Guide

**Use the Discovery Tool when:**
- Starting a new AZD migration
- Don't understand the application structure
- Need to document existing architecture
- Want to identify all components and dependencies

**Use the Architecture Planning Tool when:**
- Have component inventory
- Need to select Azure services
- Planning hosting strategies
- Designing infrastructure architecture

**Use the File Generation Tool when:**
- Have architecture decisions
- Need to create all AZD files
- Want complete project setup
- Ready to implement infrastructure

**Use the Environment Initialization Tool when:**
- All files are generated
- Ready to create AZD environment
- Need to validate complete setup
- Preparing for deployment

**Use the Azure.yaml Generation Tool when:**
- Only need service configuration
- Updating existing azure.yaml
- Working with known service requirements
- Quick service definition setup

**Use the Infrastructure Generation Tool when:**
- Only need Bicep templates
- Updating existing infrastructure
- Working with specific Azure service requirements
- Advanced infrastructure customization

## Benefits of Modular Approach

### For Users
- **Faster iterations:** Update only what you need
- **Better understanding:** Focus on one aspect at a time
- **Reduced complexity:** Smaller, focused tasks
- **Flexible workflow:** Use tools in different orders based on needs

### For LLMs  
- **Clearer context:** Each tool has specific scope
- **Better accuracy:** Focused instructions reduce errors
- **Improved validation:** Tool-specific validation steps
- **Enhanced troubleshooting:** Targeted problem resolution

### For Maintenance
- **Easier updates:** Modify individual tools without affecting others
- **Better testing:** Test each tool independently
- **Clearer documentation:** Each tool is self-contained
- **Improved reusability:** Tools can be repurposed for different scenarios

## Migration from Original Tool

If you were using the original `azd_plan_init` tool, here's how to migrate:

**Original Phase 1 (Discovery and Analysis):**
Use `azd_discovery_analysis` tool

**Original Phase 2 (Architecture Planning):**
Use `azd_architecture_planning` tool

**Original Phase 3 (File Generation):**
Use `azd_azure_yaml_generation` + `azd_infrastructure_generation` + `azd_docker_generation` for focused file generation

**Original Phase 4 (Project Validation):**
Use `azd_project_validation` tool for final validation and setup verification

The modular tools provide the same functionality with improved focus and flexibility.
