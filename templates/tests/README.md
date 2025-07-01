# Template Testing Infrastructure

This directory contains scripts for automated testing of Azure Developer CLI (azd) templates. The testing infrastructure provides comprehensive validation of template deployment and functionality.

## Overview

The template testing system performs end-to-end testing by:

1. **Initialization**: Creating new projects from templates
2. **Provisioning**: Deploying Azure infrastructure 
3. **Deployment**: Deploying applications to Azure
4. **Validation**: Running Playwright smoke tests against deployed applications
5. **Cleanup**: Removing Azure resources and local files

## Scripts

### `test-templates.sh`

Main script for testing azd templates with full deployment and validation.

**Features:**
- Tests single templates or all available templates
- Supports custom branches and environments
- Runs Playwright automation tests for validation
- Parallel deployment with serial testing
- Automatic cleanup of resources

**Prerequisites:**
- Azure CLI authenticated (`az login`)
- Azure Developer CLI installed (`azd`)
- Node.js for Playwright tests
- Valid Azure subscription

### `delete-test-templates.sh`

Cleanup script for removing test environments and local files.

**Features:**
- Removes Azure resources using `azd down`
- Cleans up local project directories
- Supports single template or bulk cleanup

## Usage Examples

### Test a Single Template

```bash
# Test specific template with default settings
./test-templates.sh -t "Azure-Samples/todo-nodejs-mongo"

# Test with custom branch
./test-templates.sh -t "Azure-Samples/todo-nodejs-mongo" -b "feature-branch"

# Test with custom environment and location
./test-templates.sh -t "Azure-Samples/todo-nodejs-mongo" -e "my-test" -l "westus2"
```

### Test All Templates

```bash
# Test all available templates
./test-templates.sh

# Test all templates with custom settings
./test-templates.sh -e "ci-test" -l "eastus" -s "your-subscription-id"
```

### Test-Only Mode (Skip Deployment)

```bash
# Run tests on already deployed environments
./test-templates.sh -t "Azure-Samples/todo-nodejs-mongo" -n
```

### Cleanup Environments

```bash
# Cleanup specific template environment
./delete-test-templates.sh -t "Azure-Samples/todo-nodejs-mongo" -u "12345"

# Cleanup all environments with specific suffix
./delete-test-templates.sh -u "12345"
```

## Configuration Options

### `test-templates.sh` Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `-f` | Root folder for test projects | `$HOME` |
| `-t` | Template name (from `azd template list`) | All templates |
| `-b` | Template branch name | `main` |
| `-e` | Environment name prefix | `$(whoami)` |
| `-r` | Playwright test retries | `1` |
| `-p` | Playwright reporter | `list` |
| `-l` | Azure location | `eastus2` |
| `-s` | Azure subscription ID | Default subscription |
| `-u` | Environment suffix | Random number |
| `-n` | Test-only mode (skip deployment) | `false` |
| `-v` | Enable Playwright validation | `true` |
| `-c` | Enable cleanup after tests | `true` |
| `-d` | DevContainer mode | `false` |

### `delete-test-templates.sh` Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `-f` | Root folder for test projects | `$HOME` |
| `-t` | Template name | All templates |
| `-e` | Environment name prefix | `$(whoami)` |
| `-u` | Environment suffix | Required |

## Environment Variables

The following environment variables are automatically set:

- `AZURE_LOCATION`: Set to the location specified by `-l` parameter

## Testing Process

1. **Template Discovery**: Uses `azd template list` to get available templates
2. **Environment Creation**: Creates unique environment names using prefix and suffix
3. **Parallel Deployment**: Deploys multiple templates simultaneously for efficiency
4. **Serial Testing**: Runs Playwright tests sequentially to avoid conflicts
5. **Validation**: Each template's `/tests` directory must contain Playwright tests
6. **Cleanup**: Removes Azure resources and local directories

## Playwright Integration

Templates are expected to include Playwright tests in a `/tests` directory. The test script:

1. Navigates to the template's `tests` directory
2. Runs `npm i && npx playwright install`
3. Executes `npx playwright test` with configured retries and reporter
4. Skips validation for `azd-starter` templates (no web endpoints)

See [`templates/todo/common/tests/README.md`](../todo/common/tests/README.md) for an example of template test setup.

## CI/CD Integration

The scripts support DevContainer environments and can be integrated into CI/CD pipelines:

```bash
# DevContainer mode (skips azd init)
./test-templates.sh -d -t "template-name"
```

## Troubleshooting

### Common Issues

1. **Authentication**: Ensure `az login` is completed before running tests
2. **Subscription**: Verify subscription ID is correct and accessible
3. **Location**: Some Azure services may not be available in all regions
4. **Playwright**: Install Playwright browsers with `npx playwright install`

### Debug Mode

Enable verbose output by adding debug flags to the azd commands within the scripts, or use Playwright's debug mode:

```bash
# In template tests directory
npx playwright test --debug
```

### Resource Cleanup

If tests fail and resources aren't cleaned up automatically:

```bash
# Manual cleanup with environment suffix
./delete-test-templates.sh -u "environment-suffix"

# Or use azd directly
azd down -e "environment-name" --force --purge
```

## Contributing

When adding new templates or modifying existing ones:

1. Ensure templates include a `/tests` directory with Playwright tests
2. Test templates locally before submitting PRs
3. Verify cleanup works properly to avoid resource leaks
4. Update documentation if adding new testing features