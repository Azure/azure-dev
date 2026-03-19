# Glossary

Key terms and concepts used throughout the Azure Developer CLI codebase.

## Core Concepts

### azd (Azure Developer CLI)

A Go-based CLI tool for Azure application development and deployment. Handles infrastructure provisioning, application deployment, environment management, and service lifecycle hooks.

### azure.yaml

The project configuration file that defines an azd project. Located at the root of a project, it declares services, their language/framework, hosting targets, and hooks. See [azure.yaml Schema](../reference/azure-yaml-schema.md).

### Environment

A named collection of configuration values and secrets stored locally (and optionally in a remote backend). Environments let you target different deployment configurations (e.g., `dev`, `staging`, `prod`) from the same project.

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

### Preflight Checks

Client-side validation that runs after Bicep compilation but before server-side deployment. Checks resource availability, permissions, and potential conflicts to surface issues early.

## Extensions

### Extension

A plugin that extends azd functionality via a gRPC-based framework. Extensions can add new commands, service targets, framework providers, event handlers, and more. First-party extensions live in `cli/azd/extensions/`.

### Extension Registry

A JSON manifest that lists available extensions, their versions, capabilities, and download URLs. The official registry is hosted at `https://aka.ms/azd/extensions/registry`.

### Extension Capabilities

The set of features an extension provides. Capabilities include: event handlers, framework service providers, service target providers, compose providers, workflow providers, and various gRPC services.

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
