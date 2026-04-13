# Creating an Extension

This guide covers how to create, build, and publish an extension for the Azure Developer CLI.

## Overview

azd extensions use a gRPC-based framework to add functionality. Extensions can provide:

- **Event handlers** — React to lifecycle events (pre/post provision, deploy, etc.)
- **Framework service providers** — Add build/restore support for new languages
- **Service target providers** — Add deployment support for new Azure hosting targets
- **Compose providers** — Custom orchestration logic
- **Workflow providers** — Custom workflow steps
- **gRPC services** — Project, Environment, User Config, Deployment, Account, Prompt, AI Model, and more

## Prerequisites

- Go 1.26 or later
- A fork of the [azure-dev](https://github.com/Azure/azure-dev) repository
- The `azd` Developer Extension installed (`azd extension install azd.dev`)

## Getting Started

### 1. Create the extension

First-party extensions live under `cli/azd/extensions/`. Create a new directory for your extension:

```bash
mkdir -p cli/azd/extensions/my.extension
cd cli/azd/extensions/my.extension
```

### 2. Define extension.yaml

Create an `extension.yaml` manifest that declares the extension's metadata and capabilities:

```yaml
name: my.extension
displayName: My Extension
description: A brief description of what this extension does
version: 0.1.0
capabilities:
  - event-handler
```

### 3. Implement the extension

Implement the required interfaces for your declared capabilities. See the extension framework services documentation for interface details.

### 4. Build

```bash
# Using the developer extension
azd x build

# Or using Go directly
go build
```

### 5. Register in the extension registry

For first-party extensions, add an entry to `cli/azd/extensions/registry.json` with version, capabilities, and download URLs.

## Extension Design Guidelines

- **Extend existing command categories** — Use verb-first structure (e.g., `azd add <resource>`)
- **Reuse parameter patterns** — Use established flags like `--subscription`, `--name`, `--type`
- **Integrate with help** — Make your extension discoverable through `azd help`
- **Error handling** — Use `ServiceError` for Azure API errors and `LocalError` for client-side errors
- **Telemetry** — Follow pattern-based classification (e.g., `ext.service.<errorCode>`)

## Detailed Reference

For comprehensive extension development documentation, see:

- [Extension Framework](../../cli/azd/docs/extensions/extension-framework.md) — Full framework guide
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language/framework support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines and best practices
