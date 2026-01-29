# Azure Developer CLI VS Code Extension - Testing Guide

This guide provides a comprehensive list of all extension features and how to test them as a user.

## Table of Contents
- [Deployment Commands](#deployment-commands)
- [Enhanced azure.yaml Editing](#enhanced-azureyaml-editing)
- [View Panels](#view-panels)
- [Environment Management](#environment-management)
- [Azure Integration](#azure-integration)
- [Configuration & Settings](#configuration--settings)
- [Utility Commands](#utility-commands)
- [Onboarding & Walkthrough](#onboarding--walkthrough)
- [Testing Checklist](#testing-checklist)

---

## 🚀 Deployment Commands

### 1. Initialize (azd init)
**What it does**: Scaffold a new application from a template

**How to test**:
- Open Command Palette (`Cmd+Shift+P` or `Ctrl+Shift+P`)
- Type "Azure Developer CLI: Initialize App"
- OR right-click on `pom.xml` or empty folder → Azure Developer CLI → Initialize
- Choose from template options or initialize from existing code
- Verify `azure.yaml` is created

### 2. Provision (azd provision)
**What it does**: Create Azure infrastructure resources (no deployment)

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Provision
- OR use Command Palette → "Azure Developer CLI: Provision Infrastructure"
- Verify Azure resources are created in portal
- Check terminal output for success messages

### 3. Deploy (azd deploy)
**What it does**: Deploy application code to existing Azure resources

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Deploy
- Can also deploy individual services from My Project view
- Verify deployment in Azure Portal
- Check application is running

### 4. Up (azd up)
**What it does**: Provision infrastructure + deploy in one command

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Up
- OR click cloud upload icon in My Project view
- Complete flow: provision → deploy → show README
- Verify resources created and app deployed

### 5. Down (azd down)
**What it does**: Delete all Azure resources and deployments

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Down
- OR click cloud download icon in My Project view
- Confirm deletion prompt
- Verify resources are deleted in Azure Portal

### 6. Monitor (azd monitor)
**What it does**: Open Application Insights in browser for deployed app

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Monitor
- OR click dashboard icon in My Project view
- Browser should open to Application Insights dashboard
- Verify metrics and logs are visible

### 7. Restore (azd restore)
**What it does**: Restore dependencies for your application

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Restore
- Check output for dependency restoration
- Verify dependencies are installed locally

### 8. Package (azd package)
**What it does**: Package application for deployment

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Package
- Can package entire app or individual services
- Check for package artifacts in output directory

### 9. Pipeline Config (azd pipeline config)
**What it does**: Set up CI/CD pipeline (GitHub Actions or Azure DevOps)

**How to test**:
- Right-click `azure.yaml` → Azure Developer CLI → Configure Pipeline
- Follow wizard to set up CI/CD
- Verify workflow files are created (.github/workflows/ or azure-pipelines.yml)

---

## 📝 Enhanced azure.yaml Editing

### 10. Schema Validation (via YAML Extension)
**What it does**: Full IntelliSense support including auto-completion, hover documentation, and schema validation

**Prerequisites**: Install the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)

**How to test**:
- Open `azure.yaml`
- Press `Ctrl+Space` to trigger IntelliSense - should see property suggestions
- Hover over properties - should see documentation from schema
- Add invalid property values - should see validation errors
- These features are provided by the YAML extension using the azure.yaml JSON schema

### 11. Project Path Validation & Diagnostics
**What it does**: Real-time validation for project paths (schema validation handled by YAML extension)

**How to test**:
- Open `azure.yaml`
- Test invalid project path:
  - Set `project: ./nonexistent` for a path that doesn't exist
  - Should see error diagnostic in Problems panel
- Test valid project path:
  - Set `project: ./existing-folder` for a path that exists
  - Should see no error
- Note: Schema validation (required properties, valid values, YAML syntax) is handled by the YAML extension

### 12. Quick Fixes
**What it does**: One-click solutions for missing project paths

**How to test**:
- Create a service with a non-existent project path in `azure.yaml`
- Click lightbulb icon or press `Cmd+.`
- Options should include:
  - Create missing project folder
  - Browse for existing folder
- Verify quick fixes work correctly

### 13. Project Renaming
**What it does**: Automatically updates project paths when folders are renamed

**How to test**:
- Right-click on a project folder referenced in `azure.yaml`
- Select "Rename" and change the name
- `azure.yaml` should automatically update the path
- Verify references are updated correctly

### 14. Drag & Drop Service Addition
**What it does**: Add service to azure.yaml by dragging folder

**How to test**:
- Drag a folder from Explorer
- Hold `Shift` key
- Drop onto `azure.yaml` editor
- Service entry should be created with proper indentation

---

## 🌲 View Panels

### 15. My Project View
**What it does**: Shows azure.yaml configuration and services

**How to test**:
- Open Azure Developer CLI sidebar (activity bar icon)
- Expand "My Project" section
- Should show: application name, services, environments
- Test inline actions (up, down, monitor, deploy)
- Verify tree structure matches azure.yaml

### 16. Environments View
**What it does**: Manage dev, staging, prod environments

**How to test**:
- Open "Environments" section in sidebar
- See list of environments with default marked (⭐)
- Right-click environment for options
- Expand environment to see variables
- Verify environment details are accurate

### 17. Template Tools View
**What it does**: Discover and initialize projects from templates

**How to test**:

#### Quick Start (when no azure.yaml exists)
- Remove or rename `azure.yaml` temporarily
- Open Template Tools view
- Should see initialization options
- Test "Initialize from existing code"
- Test "Create minimal project"

#### Browse by Category
- Expand categories: AI, Web Apps, APIs, Containers, Databases, Functions
- Verify templates are grouped correctly
- Click a template to see details

#### AI Templates
- Find AI Templates section
- Should show templates from aka.ms/aiapps
- Verify AI-focused templates are highlighted

#### Search Templates
- Use search box at top of view
- Search by: name, description, or tags
- Verify results are filtered correctly

#### Template Gallery
- Click "Open Template Gallery" link
- Should open aka.ms/awesome-azd in browser
- Verify gallery loads correctly

#### Initialize from Template
- Click rocket icon (🚀) next to a template
- OR right-click → Initialize from Template
- Follow prompts to create project
- Verify template is cloned and initialized

### 18. Extensions View
**What it does**: Browse and manage azd CLI extensions

**How to test**:
- Open "Extensions" section
- Should show installed azd extensions with versions
- Click `+` icon to install new extensions
- Right-click extension to:
  - Upgrade extension
  - Uninstall extension
- Verify extension operations complete successfully

### 19. Help and Feedback View
**What it does**: Quick access to docs and support

**How to test**:
- Open "Help and Feedback" section
- Test links:
  - Documentation
  - GitHub Issues
  - GitHub Discussions
  - Give Feedback
  - View Getting Started
- Verify all links open correctly

---

## 🔄 Environment Management

### 20. Create Environment
**What it does**: Create new environment (e.g., dev, staging, prod)

**How to test**:
- Click `+` icon in Environments view
- OR Command Palette → "Azure Developer CLI: New Environment"
- Enter environment name
- Verify created in `.azure/<env-name>/` folder
- Check `.env` file is created

### 21. Select Environment
**What it does**: Switch active environment

**How to test**:
- Right-click non-default environment
- Select "Select Environment"
- Default indicator (⭐) should move to selected environment
- Verify `.azure/<env-name>/.env` is now active
- Run `azd env get-values` to confirm

### 22. Delete Environment
**What it does**: Remove environment (not default)

**How to test**:
- Right-click non-default environment
- Select "Delete Environment"
- Confirm deletion in prompt
- Verify removed from `.azure/` folder
- Cannot delete default environment (option disabled)

### 23. Refresh Environment
**What it does**: Sync environment config from Azure

**How to test**:
- Right-click environment → "Refresh Environment"
- OR Command Palette → "Azure Developer CLI: Refresh Environment"
- Environment variables should update from Azure deployment
- Check `.env` file for new values

### 24. Edit Environment Variables
**What it does**: Open .env file for environment

**How to test**:
- Right-click environment → "Edit Environment"
- Should open `.azure/<env>/.env` file
- Make changes and save
- Verify changes persist

### 25. View Environment Variables
**What it does**: Show/hide environment variables in tree

**How to test**:
- Expand environment in tree
- Expand "Variables" group
- Click eye icon (👁️) next to variable to toggle visibility
- Sensitive values should be masked (••••••)
- Clicking again reveals value

### 26. View .env File
**What it does**: Quick access to environment .env file

**How to test**:
- Click file icon (📄) next to environment in tree
- `.env` file should open in editor
- Verify correct environment file opens

---

## 🔗 Azure Integration

### 27. Reveal Azure Resource
**What it does**: Navigate to resource in Azure Resources extension

**How to test**:
- **Prerequisites**: Install Azure Resources extension
- Right-click service in My Project → "Reveal in Azure Resources"
- Azure Resources tree should expand and highlight resource
- Verify correct resource is shown

### 28. Reveal Resource Group
**What it does**: Navigate to resource group in Azure Resources extension

**How to test**:
- Right-click application or environment → "Reveal Resource Group in Azure Resources"
- Azure Resources tree should expand to resource group
- All resources in group should be visible

### 29. Open in Azure Portal
**What it does**: Open resource in Azure Portal in browser

**How to test**:
- Right-click service in My Project → "Show in Azure Portal"
- Browser should open to resource in Azure Portal
- Verify correct resource page loads
- Test with different resource types (Web App, Storage, Cosmos DB, etc.)

### 30. Azure Resources Workspace Integration
**What it does**: Shows azd apps in Azure Resources workspace view

**How to test**:
- Open Azure Resources extension
- Go to "Workspace" section
- Should see azd applications listed with icon
- Expand to see environments and services
- Test commands from Azure Resources context menu

---

## ⚙️ Configuration & Settings

### 31. Maximum Apps to Display Setting
**What it does**: Limit number of apps shown in workspace view

**How to test**:
- Open Settings (`Cmd+,`)
- Search "azure-dev.maximumAppsToDisplay"
- Change value (default: 5)
- Reload window
- Verify workspace view respects limit

### 32. Integrated Authentication (Alpha)
**What it does**: Use VS Code auth instead of CLI auth

**How to test**:
- Open Settings → search "azure-dev.auth.useIntegratedAuth"
- Enable the setting
- Run azd commands (provision, deploy)
- Should use VS Code credentials instead of prompting login
- Check no separate browser login is triggered

---

## 🔧 Utility Commands

### 33. Install CLI
**What it does**: Download and install azd CLI

**How to test**:
- Command Palette → "Azure Developer CLI: Install CLI"
- Follow installation prompts for your OS
- Verify installation with `azd --version` in terminal
- Should show version number

### 34. Login
**What it does**: Authenticate with Azure

**How to test**:
- Command Palette → "Azure Developer CLI: Login"
- Browser should open for Azure authentication
- Complete login flow
- Verify login success message in terminal
- Run `azd auth login --check-status` to confirm

### 35. Get .env File Path (Programmatic)
**What it does**: Returns path to environment .env file

**How to test**:
- This is a programmatic API, not directly user-testable
- Used by other extensions to get environment configuration
- Can verify by checking extension contribution in package.json

### 36. Dev Center Mode
**What it does**: Enable/disable Azure Dev Center integration

**How to test**:
- Command Palette → "Azure Developer CLI: Enable Dev Center Mode"
- Verify mode is enabled (check terminal output)
- Test Dev Center-specific features
- Command Palette → "Azure Developer CLI: Disable Dev Center Mode"
- Verify mode is disabled

---

## 📚 Onboarding & Walkthrough

### 37. Getting Started Walkthrough
**What it does**: Interactive tutorial for new users

**How to test**:
- Open workspace with `azure.yaml` or `azure.yml`
- Command Palette → "Welcome" → Find "Get Started with Azure Developer CLI"
- OR Help → Getting Started
- Complete walkthrough steps:
  1. **Install**: Test CLI installation (if not installed)
  2. **Login**: Test Azure authentication
  3. **Scaffold**: Test project initialization (if no azure.yaml)
  4. **Up**: Test deployment
  5. **Explore**: Review available commands
- Verify each step:
  - Shows clear instructions
  - Marks completed steps
  - Progresses logically

---

## 🧪 Testing Checklist

### Complete Extension Testing Workflow

#### Prerequisites Setup
- [ ] Install VS Code (1.90.0 or higher)
- [ ] Install Azure Developer CLI (1.0.0 or higher)
- [ ] Have an Azure subscription
- [ ] Install Azure Resources extension (optional but recommended)

#### First-Time User Testing (Without azure.yaml)
- [ ] Open empty folder in VS Code
- [ ] Open Azure Developer CLI view
- [ ] Template Tools shows Quick Start options
- [ ] Test "Initialize from existing code"
- [ ] Test template browsing by category
- [ ] Test AI templates section
- [ ] Test template search
- [ ] Test template initialization with rocket icon
- [ ] Verify azure.yaml is created

#### Existing Project Testing (With azure.yaml)
- [ ] Open folder with azure.yaml
- [ ] My Project view shows application and services
- [ ] Environments view shows environments
- [ ] Test all deployment commands (provision, deploy, up, down)
- [ ] Test environment creation and switching
- [ ] Test monitoring command
- [ ] Test restore and package commands

#### azure.yaml Editing Features
- [ ] YAML extension provides auto-completion (Ctrl+Space)
- [ ] YAML extension provides hover documentation
- [ ] Invalid project paths show red squiggles
- [ ] Quick fixes available for missing project paths
- [ ] Drag & drop adds service (with Shift key)
- [ ] Project renaming updates azure.yaml

#### Environment Testing
- [ ] Create new environment
- [ ] Switch between environments
- [ ] View environment variables
- [ ] Toggle variable visibility
- [ ] Edit .env file
- [ ] Refresh environment from Azure
- [ ] Delete environment (non-default)
- [ ] Default environment marked with ⭐

#### Azure Integration Testing
- [ ] Reveal resource in Azure Resources extension
- [ ] Reveal resource group in Azure Resources extension
- [ ] Open resource in Azure Portal
- [ ] Verify workspace integration in Azure Resources

#### Extensions Testing
- [ ] Extensions view shows installed extensions
- [ ] Install new extension
- [ ] Upgrade extension
- [ ] Uninstall extension

#### Context Menu Testing
- [ ] Right-click azure.yaml shows full menu
- [ ] Right-click service shows deploy option
- [ ] Right-click environment shows options
- [ ] Right-click template shows initialize option

#### Command Palette Testing
- [ ] All commands visible in Command Palette
- [ ] Commands execute correctly
- [ ] Search "Azure Developer CLI" shows all commands

#### Settings Testing
- [ ] Maximum apps setting works
- [ ] Integrated auth setting (if enabled)

#### Error Handling Testing
- [ ] Missing CLI prompts installation
- [ ] Not logged in prompts login
- [ ] Invalid project paths show errors in Problems panel
- [ ] YAML extension handles schema validation errors
- [ ] Failed deployments show error details

#### Performance Testing
- [ ] Extension activates quickly
- [ ] Tree views load without delay
- [ ] Commands respond promptly
- [ ] No UI freezes during operations

#### Multiple Environments Scenario
- [ ] Create dev, staging, prod environments
- [ ] Switch between them
- [ ] Deploy to each environment
- [ ] Verify resources are separate
- [ ] Compare environment variables

#### Full Workflow Testing
1. [ ] Start with empty folder
2. [ ] Initialize from template
3. [ ] Examine azure.yaml
4. [ ] Login to Azure
5. [ ] Create dev environment
6. [ ] Run azd up (provision + deploy)
7. [ ] Test deployed application
8. [ ] Make code changes
9. [ ] Run azd deploy
10. [ ] Monitor with Application Insights
11. [ ] Create prod environment
12. [ ] Deploy to production
13. [ ] Clean up with azd down

---

## 📊 Extension Settings Reference

### azure-dev.maximumAppsToDisplay
- **Type**: Number
- **Default**: 5
- **Description**: Maximum number of Azure Developer CLI apps to display in the Workspace Resource view

### azure-dev.auth.useIntegratedAuth
- **Type**: Boolean
- **Default**: false
- **Description**: Use VS Code integrated authentication with the Azure Developer CLI (alpha feature)

---

## 🎯 Testing Tips

### Efficient Testing
1. **Use Command Palette** - Fastest way to access commands (`Cmd+Shift+P`)
2. **Check Output Panel** - View detailed logs (Output → Azure Developer CLI)
3. **Test with Templates** - Start with official templates to ensure working baseline
4. **Use Different Environments** - Test environment switching and isolation
5. **Verify in Azure Portal** - Always confirm resources are created/deleted

### Common Issues to Test
- CLI not installed → Should prompt for installation
- Not authenticated → Should prompt for login
- Missing azure.yaml → Should show Quick Start in Template Tools
- Invalid paths in azure.yaml → Should show diagnostics
- Deployment failures → Should show clear error messages

### Testing Different Scenarios
- **New User**: No CLI, no azure.yaml, first time setup
- **Returning User**: Existing project, multiple environments
- **Team Member**: Cloning existing project, setting up environment
- **CI/CD User**: Setting up pipeline configuration
- **Multiple Projects**: Testing with multiple azure.yaml files

---

## 🚀 Quick Reference

### Essential Commands
```bash
# Initialize new project
Cmd+Shift+P → "Azure Developer CLI: Initialize App"

# Deploy everything
Right-click azure.yaml → Azure Developer CLI → Up

# Monitor application
Right-click azure.yaml → Azure Developer CLI → Monitor

# Clean up resources
Right-click azure.yaml → Azure Developer CLI → Down
```

### View Shortcuts
- **Activity Bar** → Azure Developer CLI icon
- **My Project** → See services and app structure
- **Environments** → Manage dev/staging/prod
- **Template Tools** → Browse and init templates
- **Extensions** → Manage azd extensions
- **Help and Feedback** → Quick access to docs

### Keyboard Shortcuts
- `Cmd+Shift+P` (Mac) / `Ctrl+Shift+P` (Windows/Linux) - Command Palette
- `Ctrl+Space` - Trigger IntelliSense in azure.yaml
- `Cmd+.` (Mac) / `Ctrl+.` (Windows/Linux) - Quick fixes

---

## 📖 Additional Resources

- [Azure Developer CLI Documentation](https://aka.ms/azure-dev)
- [VS Code Extension Documentation](https://aka.ms/azure-dev/vscode)
- [Template Gallery](https://aka.ms/awesome-azd)
- [AI Templates](https://aka.ms/aiapps)
- [GitHub Repository](https://github.com/Azure/azure-dev)
- [GitHub Issues](https://github.com/Azure/azure-dev/issues)
- [GitHub Discussions](https://github.com/Azure/azure-dev/discussions)

---

## 🐛 Reporting Issues

If you find a bug or have a feature request:

1. Check [existing issues](https://github.com/Azure/azure-dev/issues)
2. Create a new issue with:
   - Clear description of the problem
   - Steps to reproduce
   - Expected vs actual behavior
   - VS Code version
   - Extension version
   - Azure Developer CLI version (`azd version`)
   - Operating system

---

## ✅ Testing Certification

After completing all tests, you should be able to:
- ✅ Initialize projects from templates
- ✅ Edit azure.yaml with IntelliSense
- ✅ Provision and deploy to Azure
- ✅ Manage multiple environments
- ✅ Monitor deployed applications
- ✅ Integrate with Azure Resources extension
- ✅ Use all view panels effectively
- ✅ Troubleshoot common issues

**Last Updated**: January 7, 2026
