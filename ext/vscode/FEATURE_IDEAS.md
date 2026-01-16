# Azure Developer CLI VS Code Extension - Feature Ideas

This document outlines potential features and enhancements for the Azure Developer CLI VS Code extension.

## Developer Experience Enhancements

### 1. Interactive Dashboard/Overview

- Real-time status view showing deployment health, resource costs, and environment states
- Quick action tiles for common workflows (provision → deploy → monitor)
- Recent activity log with links to outputs

### 2. Integrated Debugging Support

- Direct attach to Azure Container Apps or App Service instances
- Port forwarding shortcuts from the tree view
- Log streaming with syntax highlighting and filtering

### 3. Cost Management Integration

- Display estimated/actual costs per environment in the tree view
- Cost breakdown by resource
- Alerts when costs exceed thresholds

## Workflow & Productivity

### 4. Smart Templates & Scaffolding ~~(COMPLETED)~~

- ~~Template preview before init (show what services will be created)~~
- ~~Custom template wizard with live preview~~
- ~~"Add service" command to existing azure.yaml (add database, cache, etc.)~~
- ~~Browse templates from awesome-azd gallery (https://aka.ms/awesome-azd)~~
- ~~Filter templates by AI/ML focus (https://aka.ms/aiapps)~~
- ~~Search templates by language, framework, or Azure service~~
- ~~Quick start options for users without azure.yaml (init from code, minimal project)~~
- ~~Category-based template browsing (AI, Web Apps, APIs, Containers, Databases)~~

### 5. Environment Diff & Management

- Compare configurations between environments
- Bulk environment variable editor with autocomplete
- Environment templates/presets (dev, staging, prod)

### 6. Pipeline & CI/CD Enhancements

- Visualize pipeline runs directly in VS Code
- Monitor GitHub Actions/Azure DevOps pipelines
- One-click rollback to previous deployment

## Language & IntelliSense

### 7. Enhanced azure.yaml Support ~~(COMPLETED)~~

- ~~Auto-completion for service names, hooks, and configurations~~
- ~~Inline documentation on hover~~
- ~~Validation warnings for common mistakes~~
- ~~Quick fixes for errors (missing dependencies, invalid references)~~

### 8. Multi-file Refactoring

- Rename project across all files (azure.yaml, bicep, configs)
- Find all references to services/resources
- Safe rename operations

## Observability & Monitoring

### 9. Advanced Monitoring

- Embed Application Insights charts in VS Code
- Custom metric dashboards
- Alert configuration UI
- Log analytics query builder

### 10. Health Checks & Diagnostics

- Pre-deployment validation (check quotas, permissions, naming conflicts)
- Post-deployment smoke tests
- Resource dependency graph visualization

## Collaboration & Documentation

### 11. Team Collaboration

- Share environment configurations safely (secrets excluded)
- Project documentation generator from azure.yaml
- Architecture diagram generation from resources

### 12. Testing Support

- Integration test runner for deployed services
- API testing (similar to REST Client)
- Load testing integration

## Infrastructure & Resources

### 13. Resource Browser

- Navigate Azure resources with rich details
- Quick actions (restart, scale, view logs, etc.)
- Resource connection strings with secure copy

### 14. Infrastructure as Code Tools

- Bicep/Terraform preview and validation
- What-if analysis before provision
- Generate bicep from existing resources

### 15. Local Development

- Enhanced emulator support (Cosmos DB, Storage, etc.)
- Service dependency orchestration (docker-compose integration)
- Local-to-cloud context switching

## Quick Wins

### 16. UX Improvements

- Search across all commands and views
- Keyboard shortcuts for common operations
- Status bar indicators for active environment
- Toast notifications for long-running operations

### 17. Settings & Preferences

- Favorite/pinned environments
- Custom command aliases
- Auto-refresh intervals for views
- Default region/subscription preferences

## Walkthrough & Onboarding

### 18. Enhanced Getting Started Experience

- **Post-Deployment Steps**: Add guidance on what to do after `azd up` completes (verify deployment, view resources, access endpoints)
- **Troubleshooting Guidance**: Inline tips for common errors (auth failures, quota issues, etc.)
- **Interactive Media**: Replace static SVGs with animated GIFs or embedded videos showing actual workflows
- **Progress Feedback**: Add completion events for all steps and show estimated time for each step

### 19. Improved Walkthrough Content

- **Contextual Steps**: Detect project type and customize walkthrough for specific languages/frameworks
- **Progressive Disclosure**: Show advanced options after basic walkthrough with "Learn more" expansions
- **Better Completion Detection**: Track actual deployment success/failure, not just command execution
- **Explanation of Concepts**: Clarify what happens during `provision` vs `deploy` vs `up`

### 20. Additional Walkthrough Steps

- **Environment Configuration**: Guide for setting environment variables, .env file usage, and secrets management
- **Local Development**: Add steps for `azd restore` and testing services locally before deploying
- **Monitoring & Iteration**: Dedicated step for `azd monitor` and viewing logs/troubleshooting
- **Cleanup Guidance**: Optional step for `azd down` with cost implications and cleanup best practices

### 21. Multiple Walkthrough Paths

- Separate walkthroughs for different user journeys:
  - Complete beginners ("Getting Started")
  - Existing projects ("Migrate to azd")
  - Advanced users ("CI/CD Setup")
  - Specific scenarios ("Add Database", "Enable Authentication", "Configure Monitoring")

### 22. Walkthrough Quick Wins

- **Action-Oriented Copy**: More engaging titles with expected outcomes
- **Code Samples**: Include example azure.yaml snippets and environment variable configurations
- **Smart Defaults**: Pre-fill common values based on workspace detection
- **Feedback Collection**: Add feedback button and track user drop-off points
- **Accessibility**: Improve alt text and ensure walkthrough works without images

## Implementation Priority Considerations

When considering which features to implement, evaluate based on:

- **User Impact**: Features that solve common pain points
- **Effort vs Value**: Quick wins that provide immediate value
- **Technical Complexity**: Balance complexity with team capacity
- **Integration Needs**: Consider dependencies on Azure services/APIs
- **User Feedback**: Prioritize based on community requests and surveys

## Next Steps

1. Review and prioritize features based on user feedback
2. Create detailed specifications for selected features
3. Design user flows and mockups
4. Implement and test in phases
5. Gather feedback and iterate

