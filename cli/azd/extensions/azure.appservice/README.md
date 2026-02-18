# Azure App Service Extension

This extension provides commands for managing Azure App Service resources.

## Commands

### swap

Swap deployment slots for an Azure App Service.

```bash
azd appservice swap --service <service-name> --src <source-slot> --dst <destination-slot>
```

**Flags:**

- `--service` - The name of the service to swap slots for (optional if there's only one App Service)
- `--src` - The source slot name. Use `@main` for production
- `--dst` - The destination slot name. Use `@main` for production

**Examples:**

```bash
# Swap the staging slot to production
azd appservice swap --service myapp --src staging --dst @main

# Interactive mode - prompts for service and slots
azd appservice swap

# Swap with production as source
azd appservice swap --src @main --dst staging
```
