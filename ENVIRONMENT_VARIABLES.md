# Environment Variable Support for Parameters

Azure Developer CLI (azd) now supports setting infrastructure parameters via environment variables, allowing you to skip interactive prompts and automate parameter configuration.

## Environment Variable Pattern

Use environment variables with the pattern `AZURE_PARAM_<PARAMETER_NAME>` where `<PARAMETER_NAME>` is the uppercase version of your parameter name.

## Examples

### Basic Usage

For a Bicep parameter named `webAppName`:

```bash
# Set the parameter value
export AZURE_PARAM_WEBAPPNAME="my-web-app"

# Run azd provision - no prompt for webAppName
azd provision
```

### Skip Parameters

Set an empty value to skip a parameter entirely:

```bash
# Skip the optional parameter entirely
export AZURE_PARAM_OPTIONALPARAM=""

azd provision
```

### Different Parameter Types

```bash
# String parameter
export AZURE_PARAM_APPNAME="my-application"

# Boolean parameter (supports: true/false, 1/0, yes/no, on/off)
export AZURE_PARAM_ENABLEHTTPS="true"

# Number parameter
export AZURE_PARAM_INSTANCECOUNT="3"

# Array parameter (JSON format)
export AZURE_PARAM_ALLOWEDIPS='["192.168.1.1", "10.0.0.0/24"]'

# Object parameter (JSON format)
export AZURE_PARAM_CONFIG='{"environment": "production", "debug": false}'
```

## Precedence Order

Environment variables take precedence over other parameter sources:

1. **Environment variables** (`AZURE_PARAM_*`) - Highest priority
2. Saved configuration values (from previous prompts)
3. Parameter file values
4. Default values
5. Interactive prompts - Lowest priority

## DevCenter Support

The same pattern works for DevCenter environment parameters:

```bash
# Set DevCenter parameters
export AZURE_PARAM_ENVIRONMENTNAME="dev-env"
export AZURE_PARAM_SUBSCRIPTIONID="12345678-1234-1234-1234-123456789012"

azd provision
```

## CI/CD Integration

This feature is particularly useful in CI/CD pipelines:

```yaml
# GitHub Actions example
- name: Provision Infrastructure
  env:
    AZURE_PARAM_WEBAPPNAME: ${{ vars.WEB_APP_NAME }}
    AZURE_PARAM_LOCATION: ${{ vars.AZURE_LOCATION }}
    AZURE_PARAM_ENABLELOGGING: "true"
  run: azd provision --no-prompt
```