# Template Version Management for Azure Developer CLI

## Overview
This package implements the CalVer (Calendar Versioning) functionality for Azure Developer CLI templates. It automatically creates and manages version files within templated projects, enabling better tracking, debugging, and reproducibility of templates.

## Why Template Versioning?

### Problem Statement
When users initialize projects from templates, there's no built-in mechanism to track which version of the template was used. This makes it challenging to:
- Debug issues that might be template-version specific
- Ensure consistent environments across team members
- Understand when a project's template source was last updated
- Reference the exact template state for reproducibility

### Solution
The Template Version Manager introduces a standardized approach to version tracking by:
1. Creating an `AZD_TEMPLATE_VERSION` file in the project directory
2. Using a CalVer format: `YYYY-MM-DD-<short-git-hash>`
3. Setting the file as read-only to prevent accidental modification
4. Adding the version information to `azure.yaml` for easier programmatic access

## How It Works

### Version Format
- **Date Component**: `YYYY-MM-DD` - The date when the template was initialized
- **Git Hash Component**: Short git commit hash from the template source
- **Example**: `2025-07-21-713980be`

### Implementation Details
The version management is implemented as middleware in the CLI command pipeline. When any template-dependent command runs (like `azd init`, `azd up`, `azd deploy`, etc.), the middleware:

1. Checks if the `AZD_TEMPLATE_VERSION` file exists
2. If not found, creates it with the current date and git hash
3. Makes the file read-only (permissions: 0444)
4. Updates `azure.yaml` with a `tracking_id` field containing the version
5. Prompts the user to commit the file to their repository

The middleware is designed to be non-invasive and only creates the version file once. Subsequent commands will use the existing version file if present.

## Package Components

### Key Files
- `template_version.go` - Core implementation of version manager functionality
- `template_version_test.go` - Unit tests for version management
- `registration.go` - IoC container registration for version manager

### Key Types
- `Manager` - Main service that handles version file operations
- `VersionInfo` - Struct representing parsed version information

### Key Functions
- `EnsureTemplateVersion` - Main function that checks/creates version files
- `CreateVersionFile` - Creates the version file with proper format and permissions
- `ReadVersionFile` - Reads and validates existing version files
- `GetShortCommitHash` - Retrieves the git hash for version creation
- `ParseVersionString` - Parses a version string into structured data

## Testing It Yourself

### Prerequisites
- Go development environment
- Azure Developer CLI source code
- Git installed

### How to Test

1. **Build the CLI with template versioning**:
   ```bash
   cd /path/to/azure-dev
   go build -o ./cli/azd/azd ./cli/azd
   ```

2. **Initialize a new project from a template**:
   ```bash
   mkdir -p test-app
   cd test-app
   /path/to/azure-dev/cli/azd/azd init --template azure-samples/todo-nodejs-mongo-aca
   ```

3. **Run a template-related command to trigger the middleware**:
   ```bash
   /path/to/azure-dev/cli/azd/azd env list --debug
   ```

4. **Verify the version file was created**:
   ```bash
   cat AZD_TEMPLATE_VERSION
   ```
   You should see output like: `2025-07-21-713980be`

5. **Check azure.yaml for tracking_id**:
   ```bash
   grep tracking_id azure.yaml
   ```
   It should show: `tracking_id: 2025-07-21-713980be`

### Debugging
For verbose output, run commands with debug logging:
```bash
AZURE_DEV_TRACE_LEVEL=DEBUG /path/to/azure-dev/cli/azd/azd env list --debug
```

## Code Example: Using the Template Version Manager

```go
// Create a new manager
manager := templateversion.NewManager(console, runner)

// Ensure a template version file exists (creates if missing)
version, err := manager.EnsureTemplateVersion(ctx, projectPath)
if err != nil {
    // Handle error
}

// Parse version information
versionInfo, err := templateversion.ParseVersionString(version)
if err != nil {
    // Handle error
}

// Access version components
date := versionInfo.Date           // "2025-07-21"
hash := versionInfo.CommitHash     // "713980be"
fullVersion := versionInfo.FullVersion // "2025-07-21-713980be"
```

## Benefits for the Team

1. **Enhanced Debugging**: When issues arise, the exact template version provides crucial context
2. **Template Evolution**: Track how templates evolve over time and when projects were last updated
3. **Reproducibility**: New team members can easily understand which template version a project is using
4. **Auditing**: Simplifies compliance by tracking template sources
5. **Compatibility**: Helps identify potential issues when updating templates or the CLI itself

## Future Enhancements
- Add template update detection to notify users when newer template versions are available
- Provide commands to explicitly update templates to newer versions
- Enhance version parsing to handle more complex template hierarchies

## Feedback and Contributions
Please provide feedback and suggestions for improving the template version management system. The current implementation is designed to be lightweight and unobtrusive while providing valuable metadata for project maintenance.
