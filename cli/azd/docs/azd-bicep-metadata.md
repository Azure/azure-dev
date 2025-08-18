# Azd metadata for bicep input parameters

The Azure Developer CLI can improve the experience for deploying bicep by adding azd-specific `metadata` into input parameters. The metadata becomes transparent for bicep and for the ARM deployment service, but it is recognized by azd to control the prompting for parameters during provision (azd provision).

## Adding metadata

Input parameters in bicep supports [@metadata](https://learn.microsoft.com/azure/azure-resource-manager/bicep/parameters#metadata) as a schema-free object. You can include azd specific metadata by adding the `azd` field to the parameter metadata like:

```bicep
@metadata({
  azd: {}
})
param someInput <param-type>
```

Azd metadata does not depend on the parameter's type. It can be added to any parameter.

## Supported azd metadata

The supported configuration fields for azd metadata are:

  | Field | Description |
  |-------|-------------|
  | type | Defines how azd should prompt for this parameter. Example: `location`. |
  | config | Describes the settings for some of the types, like `generate`. |
  | default | Defines a value for azd to highlight initially during a select prompt. |
  | usageName | Controls quota-check for ai-model location select |

### Type

This configuration defines a unique way for azd to prompt for an input parameter. The supported types are the following:

#### location


