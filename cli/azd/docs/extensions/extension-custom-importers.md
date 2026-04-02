# Authoring an Extension with a Custom Importer

## Overview

The **importer-provider** capability allows extensions to generate infrastructure for azd projects.
An importer reads project-specific definition files and produces Bicep (or Terraform) that azd uses
during `azd provision` (at runtime) and `azd infra gen` (to eject files into `infra/`).

This capability was motivated by [#7425](https://github.com/Azure/azure-dev/issues/7425), which
explores allowing projects to define their infrastructure in languages like C# or TypeScript
instead of writing Bicep or Terraform directly. The importer-provider extension point makes this
possible: an extension can read any project format — whether that's C# files, TypeScript modules,
YAML manifests, or even markdown — and translate it into the IaC that azd knows how to deploy.

## How It Works

```
┌─────────────┐     azure.yaml      ┌─────────────────┐     gRPC      ┌───────────────────┐
│   azd CLI   │ ──── infra: ──────▶ │  ImportManager   │ ───────────▶ │    Extension       │
│             │      importer:      │                  │              │  (ImporterProvider) │
│  provision  │      name: foo      │  Finds importer  │  CanImport?  │                    │
│  infra gen  │                     │  by name "foo"   │  Generate!   │  Reads project     │
│             │                     │                  │ ◀─────────── │  Produces Bicep     │
└─────────────┘                     └─────────────────┘   files       └───────────────────┘
```

1. **User configures** `infra.importer` in `azure.yaml` with the importer name
2. **azd** starts the extension (which registers the importer via gRPC)
3. **On `azd provision`**: ImportManager calls the importer to generate temp Bicep, then provisions
4. **On `azd infra gen`**: ImportManager calls the importer to generate files into `infra/`
5. **After ejection**: If `infra/` exists, azd uses those files directly (importer is skipped)

## The Demo Importer Example

The demo extension (`microsoft.azd.demo`) includes a sample importer that reads `.md` files with
a front-matter header and generates Bicep from resource definitions. This is a simplified analogy
for what a real importer would do:

| Demo Importer | Real-world equivalent (e.g., [#7425](https://github.com/Azure/azure-dev/issues/7425)) |
|---|---|
| Reads `demo-importer/resources.md` | Reads `infra/*.cs` or `infra/*.ts` files |
| Parses markdown with `azd-infra-gen/v1` header | Runs a compiler/transpiler (e.g., `dotnet run`, `npx tsx`) |
| Generates `main.bicep` + `resources.bicep` | Generates equivalent Bicep output |
| Extension owns the default path (`demo-importer/`) | Extension owns its conventions (e.g., `infra-ts/`) |

### Sample Project Structure

```
my-project/
├── azure.yaml              # Importer + service config
├── demo-importer/          # Resource definitions (extension default folder)
│   └── resources.md        # RG + SWA with azd-service-name tag
└── src/app/                # Deployable service
    ├── package.json
    └── dist/
        ├── index.html
        └── app.js
```

### azure.yaml

```yaml
name: my-project
infra:
  importer:
    name: demo-importer       # Extension-provided importer
    # options:                 # Optional: extension-specific settings
    #   path: custom-folder    # Override default "demo-importer" directory
services:
  app:
    host: staticwebapp
    language: js
    project: ./src/app
    dist: dist
```

Key design: the `services` list contains only deployable services. The importer is a separate
concern under `infra`, responsible for generating infrastructure. They connect via `azd-service-name`
tags in the generated Bicep.

## Writing Your Own Importer Extension

### 1. Declare the capability in `extension.yaml`

```yaml
id: my-org.my-importer
capabilities:
  - importer-provider
providers:
  - name: my-importer
    type: importer
    description: Generates infra from my project format
```

### 2. Implement the `ImporterProvider` interface

```go
type MyImporterProvider struct {
    azdClient *azdext.AzdClient
}

func (p *MyImporterProvider) CanImport(
    ctx context.Context, svcConfig *azdext.ServiceConfig,
) (bool, error) {
    // Check if the project directory contains your format
    // Return false to let other importers try
    return hasMyProjectFiles(svcConfig.RelativePath), nil
}

func (p *MyImporterProvider) ProjectInfrastructure(
    ctx context.Context, projectPath string, options map[string]string,
    progress azdext.ProgressReporter,
) (*azdext.ImporterProjectInfrastructureResponse, error) {
    // Read your project files from the resolved path
    dir := resolveDir(projectPath, options)

    // Generate Bicep (or Terraform)
    progress("Generating infrastructure...")
    bicep := generateFromMyFormat(dir)

    return &azdext.ImporterProjectInfrastructureResponse{
        InfraOptions: &azdext.InfraOptions{Provider: "bicep", Module: "main"},
        Files: []*azdext.GeneratedFile{
            {Path: "main.bicep", Content: []byte(bicep)},
            {Path: "main.parameters.json", Content: []byte(params)},
        },
    }, nil
}

func (p *MyImporterProvider) GenerateAllInfrastructure(
    ctx context.Context, projectPath string, options map[string]string,
) ([]*azdext.GeneratedFile, error) {
    // Same as ProjectInfrastructure but prefix paths with "infra/"
    dir := resolveDir(projectPath, options)
    bicep := generateFromMyFormat(dir)

    return []*azdext.GeneratedFile{
        {Path: "infra/main.bicep", Content: []byte(bicep)},
        {Path: "infra/main.parameters.json", Content: []byte(params)},
    }, nil
}
```

### 3. Register in your `listen` command

```go
host := azdext.NewExtensionHost(azdClient).
    WithImporter("my-importer", func() azdext.ImporterProvider {
        return NewMyImporterProvider(azdClient)
    })

host.Run(ctx)
```

### 4. Extension-Owned Options

The `infra.importer.options` map is fully owned by your extension. You define:
- **Default values** (e.g., default directory name, output format)
- **What keys are supported** (e.g., `path`, `format`, `verbose`)
- **Validation logic** (in your provider code)

```yaml
infra:
  importer:
    name: my-importer
    options:
      path: infra-ts           # Your extension's custom option
      format: bicep             # Another custom option
```

## Combining Importers with Services

Importers and services are orthogonal:

- **Importer** generates infrastructure (resource group, hosting resources, databases)
- **Services** define what gets built and deployed (code, containers, static files)
- **Connection**: The generated Bicep includes `azd-service-name` tags that link Azure resources
  to services in `azure.yaml`

This means you can use an importer alongside any number of services, and the services don't need
to know how the infrastructure was created.

## Infra Override (Ejection)

After running `azd infra gen`, the generated Bicep files are written to `infra/`. Once those files
exist, `azd provision` uses them directly and **skips the importer**. This supports:

- **Customization**: Users can edit the generated Bicep to add resources or modify settings
- **CI/CD**: Commit the `infra/` folder so pipelines don't need the extension installed
- **Debugging**: Inspect exactly what the importer generated

To re-generate from the importer, delete the `infra/` folder and run `azd infra gen` again.

## Current Limitations

### `azd init` Integration

Currently, extension importers are **not** invoked during `azd init`. The init command uses a
built-in project detection framework (`appdetect`) with hardcoded language detectors (Java, .NET,
Python, JavaScript). Extensions cannot yet participate in this detection.

**Workaround**: Extensions can add their own init command (e.g., `azd my-importer init`), similar
to how the AI agents extension provides `azd ai agent init`. Users would run this command to
scaffold the `azure.yaml` with the importer configuration.

**Future direction**: A new `project-detector` capability could allow extensions to define detection
rules that integrate into `azd init` automatically. This would follow the same strategy as the
importer capability — the extension defines what to detect and what to write, and azd core
orchestrates the detection flow. This is tracked as future work.

## Reference

- **Proto**: `cli/azd/grpc/proto/importer.proto`
- **Extension SDK**: `cli/azd/pkg/azdext/importer_manager.go`
- **Core integration**: `cli/azd/pkg/project/importer.go`
- **Demo implementation**: `cli/azd/extensions/microsoft.azd.demo/internal/project/importer_demo.go`
- **Sample project**: `cli/azd/test/functional/testdata/samples/extension-importer/`
