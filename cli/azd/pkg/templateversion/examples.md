# Template Version Examples

## Example 1: Examining Version Contents

```bash
# View the template version
cat AZD_TEMPLATE_VERSION
# Output: 2025-07-21-713980be

# Check the tracking ID in azure.yaml
grep tracking_id azure.yaml
# Output: tracking_id: 2025-07-21-713980be
```

## Example 2: Command Sequence with Template Versioning

```bash
# Initialize a project from template
azd init --template azure-samples/todo-nodejs-mongo-aca

# Run a command that triggers template version check
azd env list

# Verify version file creation
ls -la AZD_TEMPLATE_VERSION
# Should show: -r--r--r-- ... AZD_TEMPLATE_VERSION

# Check the file contents
cat AZD_TEMPLATE_VERSION
# Output: 2025-07-21-713980be (or similar with current date)
```

## Example 3: Using Debug Mode

```bash
# Run with debug logging for detailed information
AZURE_DEV_TRACE_LEVEL=DEBUG azd env list --debug

# Look for template version middleware output in logs
# You'll see entries like:
# "DEBUG: Checking template version"
# "Creating template version file at /path/to/project/AZD_TEMPLATE_VERSION: 2025-07-21-713980be"
```
