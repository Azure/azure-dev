# ConcurX extension (experimental)

The **ConcurX** extension (`microsoft.azd.concurx`) provides high-performance concurrent deployment capabilities for `azd`. It orchestrates the deployment of multiple services in parallel, reducing total deployment time for complex applications.

> **Note:** This extension is experimental and primarily designed to optimize deployment workflows for multi-service architectures like .NET Aspire.

## Features

- **Concurrent Deployments**: Deploys multiple services in parallel instead of sequentially.
- **Aspire Build Gate**: Intelligent coordination for .NET Aspire projects. Automatically detects when the AppHost has generated its manifest, allowing dependent services to build and deploy immediately without waiting for the full AppHost deployment to complete.
- **Isolated Logging**: Automatically captures and isolates logs for every service deployment into timestamped directories (`.azure/logs/deploy/<timestamp>/`).

## Installation

You can install this extension using the `azd` extension management command:

```bash
azd extension install microsoft.azd.concurx
```

*If installing from a local source during development:*

```bash
azd extension install <path-to-extension-folder>
```

## Usage

### Run Concurrent Deployment

Replace your standard `azd up` command with:

```bash
azd concurx up
```

This command will:

1. Run `azd provision` to set up infrastructure (sequentially, as per standard `azd` behavior).
2. Once provisioned, launch `azd deploy <service-name>` for all services defined in your `azure.yaml` concurrently.
3. Monitor .NET Aspire builds to release locks as soon as manifests are available, maximizing parallelism.

### Options

- `--debug`: Enable debug logging for the extension and the underlying `azd` commands.

```bash
azd concurx up --debug
```

### Other Commands

- `azd concurx version`: Display the current version of the extension.

## How it Works

1. **Provisioning**: The extension first executes `azd provision` and streams the output to `provision.log`. Once this step completes, it proceeds to start deployments.
2. **Aspire Gate**: For .NET Aspire applications, the AppHost project must build first to generate the deployment manifest. ConcurX identifies this dependency:
   - It starts the "First Aspire" service deployment.
   - It monitors the output for the signal `Deploying services (azd deploy)`.
   - As soon as this signal is detected, it "opens the gate," allowing all other Aspire services to begin their deployment processes instantly, rather than waiting for the AppHost deployment to finish.
3. **Results**: Upon completion, a summary is displayed in the terminal, and individual logs are preserved for troubleshooting.

## Logs

Logs are stored in your project's `.azure` directory:

```
.azure/logs/deploy/YYYYMMDD-HHMMSS/
  ├── provision.log
  ├── deploy-api.log
  ├── deploy-web.log
  └── ...
```
