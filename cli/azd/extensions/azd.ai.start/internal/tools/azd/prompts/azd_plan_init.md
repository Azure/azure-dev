# AZD Application Initialization and Migration Plan

This document provides a comprehensive, step-by-step plan for initializing or migrating applications to use Azure Developer CLI (AZD). This is the orchestrating tool that guides you through using the specialized AZD tools.

**IMPORTANT: Before starting any workflow, ALWAYS check if `azd-arch-plan.md` exists in the current directory and review it to understand current progress, previous decisions, and what work has already been completed. This prevents duplicate work and ensures continuity.**

Always use Azure best practices with intelligent defaults.

## Executive Summary

Transform any application into an AZD-compatible project using a structured approach with specialized tools. Each tool has a focused responsibility and builds upon the previous phase to create a complete AZD deployment.

## Success Criteria

The migration is successful when:

- [ ] All application components are identified and classified
- [ ] `azure.yaml` file is valid and complete
- [ ] All infrastructure files are generated and error-free
- [ ] Required Dockerfiles are created for containerizable components
- [ ] `azd-arch-plan.md` provides comprehensive documentation
- [ ] AZD environment is initialized and configured
- [ ] **All validation checks pass (use `azd_project_validation` tool)**

## Complete Workflow Guide

### Phase 1: Review Existing Progress

Check if the file `azd-arch-plan.md` exists in the current directory and review it to understand current progress, previous decisions, and what work has already been completed. This prevents duplicate work and ensures continuity.

- If file exists: Review thoroughly and skip completed phases
- If file doesn't exist: Proceed to Phase 2

### Phase 2: Discovery and Analysis

**Tool:** `azd_discovery_analysis`

Scans files recursively, documents structure/languages/frameworks, identifies entry points, maps dependencies, and creates component inventory in `azd-arch-plan.md`.

### Phase 3: Architecture Planning and Azure Service Selection

**Tool:** `azd_architecture_planning`

Maps components to Azure services, plans hosting strategies, designs database/messaging architecture, and creates containerization strategies. Updates `azd-arch-plan.md`.

### Phase 4: File Generation

Generate all necessary AZD files using these focused tools (most projects need all three):

#### 1. Generate Azure.yaml Configuration

**Tool:** `azd_azure_yaml_generation` (Required for all AZD projects)

#### 2. Generate Infrastructure Templates

**Tool:** `azd_infrastructure_generation` (Required for all AZD projects)

#### 3. Generate Docker Configurations

**Tool:** `azd_docker_generation` (Required for containerizable services)

**Use in sequence:** azure.yaml → infrastructure → docker

### Phase 5: Project Validation and Environment Setup

**Tool:** `azd_project_validation`

Validates azure.yaml against schema, compiles Bicep templates, ensures AZD environment exists, tests packaging, validates deployment with preview, and provides readiness confirmation.

## Usage Patterns

### Complete New Project Migration

```text
1. Review existing azd-arch-plan.md (Phase 1)
2. azd_discovery_analysis
3. azd_architecture_planning
4. azd_azure_yaml_generation
5. azd_infrastructure_generation
6. azd_docker_generation (if containerization needed)
7. azd_project_validation
```

### Update Existing AZD Project

```text
1. Review existing azd-arch-plan.md (Phase 1)
2. azd_azure_yaml_generation → azd_infrastructure_generation → azd_docker_generation → azd_project_validation
```

### Quick Service Addition

```text
1. Review existing azd-arch-plan.md (Phase 1)
2. azd_discovery_analysis → azd_azure_yaml_generation → azd_docker_generation → azd_project_validation
```

## Central Planning Document

**CRITICAL:** `azd-arch-plan.md` is the central coordination file that tracks progress, documents decisions, and maintains project state. Always review this file before starting any tool to understand current progress and avoid duplicate work.

## Supporting Resources

### Schema and Validation

- Use `azd_yaml_schema` tool to get complete azure.yaml schema information
- Use `azd_iac_generation_rules` tool for Infrastructure as Code best practices

### Troubleshooting

Each tool includes:

- Validation checklists
- Testing commands
- Common issues and solutions
- Next step guidance

## Getting Started

**Standard workflow:** 
1. Review existing `azd-arch-plan.md` (Phase 1)
2. `azd_discovery_analysis` → `azd_architecture_planning` → File generation tools → `azd_project_validation`

Keep `azd-arch-plan.md` updated throughout the process as the central coordination document.
