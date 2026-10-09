# Glossary

Key terms and concepts used throughout the Azure Developer CLI codebase.

## Core Concepts

### azd (Azure Developer CLI)

A Go-based CLI tool for Azure application development and deployment. Handles infrastructure provisioning, application deployment, environment management, and service lifecycle hooks.

### azure.yaml

The project configuration file that defines an azd project. Located at the root of a project, it declares services, their language/framework, hosting targets, and hooks. See [azure.yaml Schema](../reference/azure-yaml-schema.md).

### Environment

A named collection of configuration values and secrets stored locally (and optionally in a remote backend). Environments let you target different deployment configurations (e.g., `dev`, `staging`, `prod`) from the same project.

Local state is stored in `.azure/<environment-name>/`. Names contain 1-64 alphanumeric characters, hyphens, underscores, parentheses, or periods and must be valid directory names, not paths. Paths, dot-only names (such as `.` and `..`), and platform-reserved names are rejected before accessing environment state. The same validation applies to `defaultEnvironment` in `.azure/config.json`; an empty default means no environment is selected.

On Windows, names ending in a period are also rejected because they resolve to the same directory as the name without trailing periods. Interior periods remain supported.

Local environment paths are resolved against the canonical project directory before accessing state. The `.azure` directory, environment directories, and environment state and lock files cannot be symbolic links or Windows reparse points (including junctions), even when a link targets another location within `.azure`. Existing directory paths must be directories, and state and lock file paths must be regular files. Missing paths remain supported, and a project reached through a linked parent directory remains supported. These checks validate existing filesystem entries; they do not provide isolation from a process concurrently replacing filesystem entries.

The same checks protect the resource cache (`.azure/<environment-name>/.state.json`), state-change notification (`.azure/.state-change`), and all reads and writes of project configuration (`.azure/config.json`), including saved session state. Environment listing skips invalid names from both local and remote stores, as well as local entries with links or unsupported filesystem types in their directories, state files, or lock files, logging the reason, so unrelated entries do not prevent selecting a valid environment. Errors accessing the base directory or project configuration still fail the listing.

Remote state is loaded only when the local environment is absent. Errors reading existing local state are reported rather than treated as a reason to replace it with remote state. Remote-only listing entries must also pass local state and cache path validation before they can be selected.

### Service

A deployable unit defined in `azure.yaml`. Each service has a source path, a language/framework, and a host target. Services are built, packaged, and deployed independently during `azd deploy`.

### Service Target (Host)

The Azure resource that hosts a deployed service. Supported targets include Azure App Service, Azure Container Apps, Azure Functions, Azure Static Web Apps, Azure Kubernetes Service (AKS), and Azure AI.

### Framework Service

A language/build framework that knows how to restore, build, and package a service's source code. Built-in frameworks include .NET, Python, Java, JavaScript/TypeScript, and Docker. Extensions can add support for additional frameworks.

## Infrastructure

### Bicep

The default Infrastructure as Code (IaC) language for azd. Bicep templates declare Azure resources and are compiled to ARM templates before deployment.

### Terraform

An alternative IaC provider supported by azd. Terraform configurations use HCL syntax to declare Azure resources.

### Provisioning

The process of creating or updating Azure infrastructure from IaC templates. Triggered by `azd provision` or as part of `azd up`.

### Provision State

A hash-based mechanism that tracks whether the IaC template has changed since the last deployment. Provisioning is skipped when the template hash matches the previous deployment, unless `--no-state` is passed.

### Provision Validation

Client-side (local) validation that runs after Bicep compilation but before deployment. Disabled with `azd config set validation.provision off`. Validates role assignment permissions, AI model quotas, and reserved resource names to surface issues early.

## Extensions

### Extension

A plugin that extends azd functionality via a gRPC-based framework. Extensions can add new commands, service targets, framework providers, event handlers, and more. First-party extensions live in `cli/azd/extensions/`.

### Extension Registry

A JSON manifest that lists available extensions, their versions, capabilities, and download URLs. The official registry is hosted at `https://aka.ms/azd/extensions/registry`.

### Extension Capabilities

The set of features an extension provides. Valid capabilities are: `custom-commands`, `lifecycle-events`, `mcp-server`, `service-target-provider`, `framework-service-provider`, `provisioning-provider`, `validation-provider`, and `metadata`. Capabilities describe customer-facing features azd calls into, not host services an extension consumes — telemetry, for example, is a host service and needs no capability. See [Extension Framework](../architecture/extension-framework.md) for details.

### Extension Usage Event

A named event an extension reports through `TelemetryService.ReportUsage`, carrying an event name and a `map<string, string>` of attributes. `azd` core adds the extension identity, prefixes every caller-supplied key with `ext.`, and bounds size and volume. First-party extensions declare each concrete field and its classification metadata in `cli/azd/extensions/telemetry/fields.go`; repository validation blocks undeclared or dynamically keyed attributes before release. Runtime recording remains limited to eligible official-registry installations. See [ADR-001](../architecture/adr-001-extension-telemetry-events.md).

### Provisioning Provider

An extension capability that lets an extension provide a custom infrastructure provisioning experience as an alternative to built-in providers such as Bicep and Terraform.

## Architecture

### ActionDescriptor

The pattern used to define CLI commands. Each command is described by an `ActionDescriptor` that specifies its metadata, flags, output formats, and the action implementation to resolve via IoC.

### Action

The interface that command implementations satisfy. Actions have a `Run(ctx context.Context) (*ActionResult, error)` method that executes the command logic.

### IoC Container

The dependency injection container (`pkg/ioc`) used throughout azd. All services are registered at startup in `cmd/container.go` and resolved via the container at runtime.

### Middleware

Cross-cutting concerns that wrap command execution. Middleware handles telemetry, hooks, extensions, and other concerns that apply across multiple commands.

### Hooks

User-defined scripts that run at specific lifecycle points (pre/post provision, pre/post deploy, etc.). Hooks are declared in `azure.yaml` and executed by the middleware pipeline.

## Feature Lifecycle

### Alpha Feature

An experimental feature behind a feature flag. Enabled via `azd config set alpha.<name> on` or `AZD_ALPHA_ENABLE_<name>=true`. No stability guarantees.

### Beta Feature

A feature that is functional and supported but may undergo breaking changes. Beta features are available by default without feature flags.

### Stable Feature

A fully supported feature with backward compatibility guarantees and complete documentation.

## Development

### Snapshot Testing

A testing approach where command help output is captured and compared against saved snapshots. Updated via `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'`.

### Fig Spec

A shell completion specification generated from the CLI command tree. Used to provide rich tab-completion in terminals that support Fig/autocomplete.
