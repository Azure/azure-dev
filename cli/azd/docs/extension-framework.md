# Extension Framework

Table of Contents

- [Getting Started](#getting-started)
- [Managing Extensions](#managing-extensions)
- [Developing Extensions](#developing-extensions)
  - [Capabilities](#capabilities)
  - [Developer Artifacts](#developer-artifacts)
  - [gRPC Services](#grpc-services)
    - [Project Service](#project-service)
    - [Environment Service](#environment-service)
    - [User Config Service](#user-config-service)
    - [Deployment Service](#deployment-service)
    - [Account Service](#account-service)
    - [Prompt Service](#prompt-service)
    - [Event Service](#event-service)
    - [Container Service](#container-service)
    - [Framework Service](#framework-service)
    - [Service Target Service](#service-target-service)
    - [Compose Service](#compose-service)
    - [Workflow Service](#workflow-service)

## Getting Started

`azd` extensions are currently an alpha feature within `azd`.

- Initially official extensions will start shipping at //BUILD 2025.
- Official extensions must be developed in a fork of the [azure/azure-dev](https://github.com/azure/azure-dev) github repo.
- Extension binaries are shipped as Github releases to the same repo through our official pipelines.

## Managing Extensions

### Extension Sources

Extension sources are file or URL based manifests that provide a registry of available `azd` extensions.
Users can add custom extension sources that connect to private, local, or public registries.
Extension sources are an equivalent concept to Nuget or NPM feeds.

Extension registries must adhere to the [official extension registry schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.schema.json).

#### Official Registry

The official extension source registry is pre-configured in `azd` and is hosted at [https://aka.ms/azd/extensions/registry](https://aka.ms/azd/extensions/registry).

The registry is hosted in the [`azd` github repo](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.json).

If you previously removed it and want to add it back:

```bash
azd extension source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
```

#### Development Registry

> [!CAUTION]
> Extensions hosted in the dev registry DO NOT contain signed binaries at the moment.

A shared development registry can be added to your `azd` configuration.
This registry contains extensions that are experiments and also used for internal testing before shipping official extensions.

To opt-in for the development registry run the following command:

```bash
# Add a new extension source name 'dev' to your `azd` configuration.
azd extension source add -n dev -t url -l "https://aka.ms/azd/extensions/registry/dev"
```

#### `azd extension source list`

Displays a list of installed extension sources.

#### `azd extension source add [flags]`

Adds a new named extension source to the global `azd` configuration.

- `-l, --location` The location of the extension source.
- `-n, --name` The name of the extension source.
- `-t, --type` The type of extension source. Supported types are `file` and `url`.

#### `azd extension source remove <name>`

Removes an extension source with the specified named argument

### Extension Management

Extensions are a collection of executable artifacts that extend or enhance functionality within `azd`.

#### `azd extension list [flags]`

Lists matching extensions from one or more extension sources.

- `--installed` When set displays a list of installed extensions.
- `--source` When set will only list extensions from the specified source.
- `--tags` Allows filtering extensions by tags (e.g., AI, test)

#### `azd extension show <extension-name> [flags]`

Shows detailed information for a specific extension, including description, tags, versions, and installation status.

- `-s, --source` The extension source to use. Use this flag when the same extension ID exists in multiple sources.

#### `azd extension install <extension-names> [flags]`

Installs one or more extensions from any configured extension source.

- `-v, --version` Specifies the version constraint to apply when installing extensions. Supports any semver constraint notation.
- `-s, --source` Specifies the extension source used for installations.

#### `azd extension uninstall <extension-names> [flags]`

Uninstalls one or more previously installed extensions.

- `--all` Removes all installed extensions when specified.

#### `azd extension upgrade <extension-names>`

Upgrades one or more extensions to the latest versions.

- `--all` Upgrades all previously installed extensions when specified.
- `-v, --version` Upgrades a specified extension using a semver version constraint, if provided.
- `-s, --source` Specifies the extension source used for installations.

## Developing Extensions

The following guide will help you develop and ship extensions for `azd`.

### Prerequisites

1. [Create a fork](https://github.com/Azure/azure-dev/fork) of the `azure/azure-dev` repo for extension development.
2. Clone your forked repo or open in a codespace.
3. Navigate to the `cli/azd/extensions` directory in your favorite terminal.
4. Install the `azd` [Developer Extension](#developer-extension)

### Capabilities

Extensions can provide different types of capabilities:

#### Event Handlers

Extensions can register handlers for project and service lifecycle events (e.g., `preprovision`, `prepackage`, `predeploy`). These handlers execute custom logic at specific points in the `azd` workflow. The `ExtensionHost` is the preferred way to wire handlers and manage the long-lived connection.

**Example:**
```go
host := azdext.NewExtensionHost(azdClient).
  WithProjectEventHandler(
    "preprovision",
    func(ctx context.Context, args *azdext.ProjectEventArgs) error {
      // Custom logic before provisioning
      return nil
    },
  ).
  WithServiceEventHandler(
    "prepackage",
    func(ctx context.Context, args *azdext.ServiceEventArgs) error {
      // Custom packaging logic
      return nil
    },
    nil,
  )

if err := host.Run(ctx); err != nil {
  return fmt.Errorf("failed to run extension: %w", err)
}
```

#### Service Target Providers

Extensions can implement custom service targets that handle the full deployment lifecycle (package, publish, deploy) for specialized Azure services or custom deployment patterns. `ExtensionHost` handles registration and readiness by default.

**Recommended:**
```go
// Create a service target provider and register it using the extension host
provider := project.NewCustomServiceTargetProvider(azdClient)
host := azdext.NewExtensionHost(azdClient).
  WithServiceTarget("customtype", provider)

// Run blocks until the azd server shuts down the connection
if err := host.Run(ctx); err != nil {
  return fmt.Errorf("failed to run extension: %w", err)
}
```

A service target provider must implement the `azdext.ServiceTargetProvider` interface:

```go
type ServiceTargetProvider interface {
    Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
    GetTargetResource(ctx context.Context, subscriptionId string, serviceConfig *ServiceConfig) (*TargetResource, error)
    Package(ctx context.Context, serviceConfig *ServiceConfig, frameworkPackage *ServicePackageResult, progress ProgressReporter) (*ServicePackageResult, error)
    Publish(ctx context.Context, serviceConfig *ServiceConfig, servicePackage *ServicePackageResult, targetResource *TargetResource, progress ProgressReporter) (*ServicePublishResult, error)
    Deploy(ctx context.Context, serviceConfig *ServiceConfig, servicePackage *ServicePackageResult, servicePublish *ServicePublishResult, targetResource *TargetResource, progress ProgressReporter) (*ServiceDeployResult, error)
    Endpoints(ctx context.Context, serviceConfig *ServiceConfig, targetResource *TargetResource) ([]string, error)
}
```

#### Complete Extension Host Builder Pattern

The `ExtensionHost` provides a fluent builder API that allows you to register all extension capabilities in a single, unified pattern. This is the **recommended approach** for extension development as it handles all service registration, readiness signaling, and lifecycle management automatically.

**Complete Example - Registering All Extension Capabilities:**

```go
func newListenCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "listen",
        Short: "Starts the extension and listens for events.",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Create a new context that includes the AZD access token.
            ctx := azdext.WithAccessToken(cmd.Context())

            // Create a new AZD client.
            azdClient, err := azdext.NewAzdClient()
            if err != nil {
                return fmt.Errorf("failed to create azd client: %w", err)
            }
            defer azdClient.Close()

            // Register all extension capabilities using the builder pattern
            host := azdext.NewExtensionHost(azdClient).
                // Register a custom service target for specialized deployments
                WithServiceTarget("demo", func() azdext.ServiceTargetProvider {
                    return project.NewDemoServiceTargetProvider(azdClient)
                }).
                // Register a custom framework service for language support
                WithFrameworkService("rust", func() azdext.FrameworkServiceProvider {
                    return project.NewDemoFrameworkServiceProvider(azdClient)
                }).
                // Register project-level event handlers
                WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
                    fmt.Printf("Preparing provisioning for project: %s\n", args.Project.Name)
                    // Perform pre-provisioning logic
                    return nil
                }).
                WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
                    fmt.Printf("Preparing deployment for project: %s\n", args.Project.Name)
                    // Perform pre-deployment validation
                    return nil
                }).
                WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
                    fmt.Printf("Deployment completed for project: %s\n", args.Project.Name)
                    // Perform post-deployment tasks (e.g., health checks, notifications)
                    return nil
                }).
                // Register service-level event handlers with optional filtering
                WithServiceEventHandler("prepackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
                    fmt.Printf("Packaging service: %s\n", args.Service.Name)
                    
                    // Access artifacts from previous phases
                    if len(args.ServiceContext.Build) > 0 {
                        fmt.Printf("Found %d build artifacts\n", len(args.ServiceContext.Build))
                    }
                    
                    return nil
                }, nil). // No filtering - applies to all services
                WithServiceEventHandler("postpackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
                    fmt.Printf("Package completed for service: %s\n", args.Service.Name)
                    
                    // Check package artifacts
                    for _, artifact := range args.ServiceContext.Package {
                        fmt.Printf("Package artifact: %s\n", artifact.Path)
                    }
                    
                    return nil
                }, &azdext.ServiceEventOptions{
                    // Optional: Filter to only handle specific service types
                    Host:     "containerapp",
                    Language: "python",
                })

            // Start the extension host - this blocks until shutdown
            if err := host.Run(ctx); err != nil {
                return fmt.Errorf("failed to run extension: %w", err)
            }

            return nil
        },
    }
}
```

**Key Benefits of the Builder Pattern:**

1. **Unified Registration**: Register all capabilities in one place
2. **Automatic Lifecycle Management**: ExtensionHost handles readiness signaling and shutdown
3. **Fluent API**: Chain multiple registrations for clean, readable code
4. **Type Safety**: Compile-time checking of provider interfaces
5. **Centralized Configuration**: All extension behavior defined in one location

**Extension Capabilities You Can Register:**

- **Service Target Providers**: Custom deployment targets (e.g., VMs, custom Azure services)
- **Framework Service Providers**: Language/framework-specific build and package logic
- **Project Event Handlers**: Project-level lifecycle events (preprovision, predeploy, etc.)
- **Service Event Handlers**: Service-level lifecycle events with optional filtering

**Event Handler Filtering:**

Service event handlers support optional filtering to only handle specific service types:

```go
WithServiceEventHandler("prepackage", handler, &azdext.ServiceEventOptions{
    Host:     "containerapp",  // Only handle Container App services
    Language: "python",        // Only handle Python services
})
```

### Supported Languages

`azd` extensions can be built in any programming language but starter templates are included for the following:

- Go (Best support)
- Dotnet (C#)
- Python
- JavaScript
- TypeScript (Coming Soon)

Each language has different build times and integration capabilities:

| Language | Build Time | Platform Support | Integration Level |
|----------|------------|------------------|-------------------|
| Go       | Fast (~15s)| All platforms    | Full native support |
| Dotnet   | Medium (~60s)| All platforms  | Strong integration |
| Python   | Slower (~4m) | All platforms  | Good integration   |
| JavaScript| Medium (~90s)| All platforms | Basic integration  |

The build process automatically creates binaries for multiple platforms and architectures:
- Windows (AMD64, ARM64)
- Linux (AMD64, ARM64)
- macOS/Darwin (AMD64, ARM64)

> [!NOTE]
> Build times may vary depending on your hardware and extension complexity.

### Developer Extension

The easiest way to get started building extensions is to install the `azd` Developer extension.

> [!IMPORTANT]
> Ensure you have added the `dev` extension source to your `azd` configuration
>
> [Configure dev extension source](#development-registry)

```bash
# Install the `azd` developer extension
azd extension install microsoft.azd.extensions
```

Once installed the extension registers a suite of commands under the `x` namespace.

> [!NOTE]
> Having issues or have some ideas to improve the `azd` developer extension?
>
> Just [log an issue](https://github.com/Azure/azure-dev/issues) in the `azure/azure-dev` repo.

#### Commands

`init` - Initialize a new extension project.

> [!TIP]
> You'll typically want to run this command inside the `azd/cli/extensions` directory.

Usage: `azd x init`

- Collects information for the extension and scaffolds and extension in a specified language of choice.
- Creates local extension source if it doesn't already exist
- Builds initial binaries for extension
- Packs extension
- Publishes extension to local extension source
- Installs the extension locally for immediate use

---

`build` - Build the binary for the extension.

Usage: `azd x build`

- `--cwd` - The extension directory, defaults to `.`.
- `--all` - Builds binaries for all supported operating systems and architecture.
- `--output, -o` - Path to the output directory, defaults to `./bin`.
- `--skip-install` - When skips local installation after successful build.

---

`watch` - Watches the extension directory for changes and automatically rebuilds and installs extension

Usage: `azd x watch`

- `--cwd` - The extension directory, defaults to `.`.

---

`pack` - Package your extension to prepare for publishing.

Usage: `azd x pack`

- `--cwd` - The extension directory, defaults to `.`.
- `--input, -i` - Path to the input directory that contains binary files.
- `--output, -o` - Path to the artifacts output directory, defaults to local `azd` artifacts path, `~/.azd/registry`.
- `--rebuild` - When set forces a rebuild before packaging.

---

`release` - Create a new Github release for the extension.

Usage: `azd x release --repo {owner}/{name}`

- `--cwd` - The extension directory, defaults to `.`.
- `--artifacts` - Path to the artifacts to upload for the release, defaults to local `azd` artifacts path, `~/.azd/registry`.
- `--repo` - The Github repo name in `{owner}/{repo}` format.
- `--title, -t` - The name of the release, defaults to extension name plus version.
- `--prerelease` - When set marks the release as a prerelease.
- `--draft, -d` - When set marks the release a draft
- `--notes, -n` - The release notes for the release, defaults to using contents of `CHANGELOG.md` within extension directory.
- `--version, -v` - The version of the release, defaults to extension version from extension manifest
- `--confirm` - When set bypasses confirmation prompts before release

---

`publish` - Updates the extension source registry with new metadata.

Usage: `azd x publish --repo {owner}/{name}`

- `--cwd` - The extension directory, defaults to `.`.
- `--registry, -r` - The path to the registry.json file to update, defaults to local extension registry
- `--repo` - The Github repo name in `{owner}/{repo}` format.
- `--version, -v` - The version of the release, defaults to extension version from extension manifest

---

### Extension Structure Overview

When you create a new extension using `azd x init`, it generates a directory structure with several important files:

```
contoso.azd.samples.<language>/
├── bin/                    # Contains built binaries
├── build.ps1               # Windows build script
├── build.sh                # Unix build script
├── CHANGELOG.md            # Version history and release notes
├── extension.yaml          # Extension metadata and capabilities
├── README.md               # Documentation for your extension
└── <language-specific>     # Source code files specific to the chosen language
```

Key files in the extension structure:

- **extension.yaml**: Defines metadata, capabilities, and commands for your extension
- **CHANGELOG.md**: Documents changes between versions (used for release notes)
- **build scripts**: Language-specific scripts for building your extension

Each supported language has a slightly different structure:

#### Go Extension Structure
```
├── go.mod                  # Go module definition
├── go.sum                  # Go dependency checksums
├── main.go                 # Entry point for the extension
└── internal/               # Internal implementation code
```

#### .NET Extension Structure
```
├── <ExtensionName>.csproj  # .NET project file
├── Program.cs              # Entry point for the extension
└── Commands/               # Command implementations
```

#### Python Extension Structure
```
├── pyproject.toml          # Python project configuration
├── requirements.txt        # Python dependencies
├── setup.py                # Package setup script
├── __main__.py             # Entry point for the extension
└── src/                    # Source code directory
```

#### JavaScript Extension Structure
```
├── package.json            # NPM package configuration
├── package-lock.json       # NPM dependency locks
├── index.js                # Entry point for the extension
└── src/                    # Source code directory
```

### Version Upgrade Path

Managing versions of your extension is an important part of the development process. Here's how to properly upgrade your extension version:

1. **Update Version Number**: 
   - Modify the `version` field in your `extension.yaml` file
   - Follow [Semantic Versioning](https://semver.org/) (MAJOR.MINOR.PATCH)

2. **Document Changes**:
   - Update your `CHANGELOG.md` with details about what's new or fixed
   - Include any breaking changes or migration notes

3. **Build, Package and Test**:
   ```bash
   azd x build --all     # Build for all platforms
   azd x pack            # Package the extension
   ```

4. **Release the New Version**:
   ```bash
   azd x release --repo <owner>/<repo>
   ```

5. **Publish to Registry**:
   ```bash
   azd x publish --repo <owner>/<repo>
   ```

#### Version Compatibility

- **Major Version Changes (1.0.0 → 2.0.0)**: Indicates breaking changes
- **Minor Version Changes (1.0.0 → 1.1.0)**: Adds new features while maintaining backward compatibility
- **Patch Version Changes (1.0.0 → 1.0.1)**: Includes bug fixes with no feature changes

When upgrading across major versions, users may need to adapt to API changes. Document these changes clearly in your `CHANGELOG.md`.

### GitHub Authentication Requirements

When using the `release` and `publish` commands that interact with GitHub repositories, you'll need proper authentication:

1. Ensure you're authenticated with GitHub before attempting to create releases
2. Use a Personal Access Token (PAT) with appropriate permissions:
   - For `azd x release`: `repo` scope permissions
   - For `azd x publish`: `repo` scope permissions

You can authenticate with GitHub using:

```bash
# Login with your GitHub credentials
gh auth login

# Or set the GITHUB_TOKEN environment variable
export GITHUB_TOKEN=your_personal_access_token
```

> [!NOTE]
> Releases and publishing will fail without proper GitHub authentication.

### Publishing Workflow

The publishing process for `azd` extensions involves multiple steps:

1. **Build** the extension for all target platforms
2. **Package** the extension artifacts
3. **Release** the extension to GitHub
4. **Publish** the extension to a registry

Each step can be performed manually or combined in a single script for automation:

```bash
# Complete publishing workflow example
cd path/to/your/extension
azd x pack --rebuild
azd x release --repo owner/repo 
azd x publish --repo owner/repo
```

#### Cross-Platform Support

When publishing an extension, the system automatically generates binaries and packages for multiple platforms:
- Windows (AMD64, ARM64)
- Linux (AMD64, ARM64)
- macOS/Darwin (AMD64, ARM64)

This ensures your extension can be used across different operating systems and architectures without additional configuration.

### Troubleshooting

Common issues you might encounter when developing and publishing extensions:

#### Build Issues

- **Language-Specific Dependencies**: Ensure all required dependencies for your language are installed
- **Platform Compatibility**: Test on the platforms you're targeting
- **Build Times**: Python extensions take significantly longer to build (~4 minutes) compared to Go (~15 seconds)

#### Release Issues

- **GitHub Authentication**: Verify your GitHub token has proper permissions
- **Version Conflicts**: Ensure you're not trying to release a version that already exists
- **Release Asset Size**: Large extensions may take longer to upload

#### Publishing Issues

- **Registry Conflicts**: Check if the extension ID already exists in the registry
- **Permission Denied**: Verify your GitHub token has proper permissions
- **Registry Schema Validation**: Ensure your extension manifest follows the schema

### Capabilities

#### Current Capabilities (October 2025)

The following lists the current capabilities available to `azd` extensions:

##### Extension Commands (`custom-commands`)

> Extensions must declare the `custom-commands` capability in their `extension.yaml` file.

Extensions can register commands under a namespace or command group within `azd`.
For example, installing the AI extension adds a new `ai` command group.

##### Lifecycle Events (`lifecycle-events`)

> Extensions must declare the `lifecycle-events` capability in their `extension.yaml` file.

Extensions can subscribe to project and service lifecycle events (both pre and post events) for:

- build
- restore
- package
- provision
- deploy

Your extension _**must**_ include a `listen` command to subscribe to these events.
`azd` will automatically invoke your extension during supported commands to establish bi-directional communication.

##### Service Target Providers (`service-target-provider`)

> Extensions must declare the `service-target-provider` capability in their `extension.yaml` file.

Extensions can provide custom service targets that handle the full deployment lifecycle (package, publish, deploy) for specialized Azure services or custom deployment patterns. Examples include:

- Custom VM deployment targets
- Specialized container platforms
- Edge computing platforms
- Custom cloud providers

##### Framework Service Providers (`framework-service-provider`)

> Extensions must declare the `framework-service-provider` capability in their `extension.yaml` file.

Extensions can provide custom language and framework support for build, restore, and package operations. Examples include:

- Custom language support (Rust, PHP, etc.)
- Framework-specific build systems
- Custom package managers
- Specialized build toolchains

##### Model Context Protocol Server (`mcp-server`)

> Extensions must declare the `mcp-server` capability in their `extension.yaml` file.

Extensions can provide AI agent tools through the Model Context Protocol, enabling:

- Custom AI tools and integrations
- Specialized knowledge bases
- Azure service automation for AI agents
- Custom development workflows for AI-assisted development

#### Future Considerations

Future ideas include:

- Registration of pluggable providers for:
  - Infrastructure providers (e.g., Pulumi)
  - Source control providers (e.g., GitLab)
  - Pipeline providers (e.g., TeamCity)
---

### Developer Workflow

The following are the most common developer workflow for developing extensions.

#### Creating a new Extension

1. Run `azd x init` to start a new extension

#### Developing an Extension

1. Navigate to the extension folder `azd/cli/extensions/{EXTENSION_ID}`
1. Run `azd x watch` during development to automatically build and install local updates
1. Run `azd x build` incrementally build & install local version

#### Validate, Release and Publish development extension

1. Create a new feature branch for the extension `git checkout -b <branch-name>`
1. Modify the extension code as needed
1. Update the version parameter within the `extension.yaml` file.
1. Run `azd x pack --rebuild`
1. Run `azd x release --prerelease` to create new Github release for the extension
1. Run `azd x publish --registry ../registry.json` to publish the new version metadata into the registry
1. Commit and push the changes to the forked repo.

To share the forked registry with others just provide the raw github link to the `azd/cli/extensions/registry.json` file.

#### Validate, Release and Publish official extension

1. Follow above steps for validating, releasing & publishing development extension
1. Push the local changes `git push <remote> <branch-name> -u`
1. Create PR for the changes within `azure/azure-dev` repo

> [!IMPORTANT]
> Make sure modifications to the `registry.json` file are NOT included on extension code PR.
> Changes to the `registry.json` file should be made after code changes have been merged.

##### After PR has been merged

1. Create a new branch to publish registry changes `git checkout -b <branch-name>`
1. Run `azd x publish --registry ../registry.json` to publish the new version metadata into the registry
1. Commit the changes to the local branch, `git commit -am "<description>"`
1. Create PR for the changes within `azure/azure-dev` repo

Once PR has been merged the extension updates are now live in the official `azd` extension source registry.

### Extension Manifest

Each extension must declare an `extension.yaml` file that describes the metadata for the extension and the capabilities that it supports. This metadata is used within the extension registry to provide details to developers when searching for and installing extensions.

A [JSON schema](../extensions/extension.schema.json) is available to support authoring extension manifests.

#### Schema Properties

**Required Properties:**
- `id`: Unique identifier for the extension
- `version`: Semantic version following MAJOR.MINOR.PATCH format
- `capabilities`: Array of extension capabilities (see below)
- `displayName`: Human-readable name of the extension
- `description`: Detailed description of the extension

**Optional Properties:**
- `namespace`: Command namespace for grouping extension commands
- `entryPoint`: Executable or script that serves as the entry point
- `usage`: Instructions on how to use the extension
- `examples`: Array of usage examples with name, description, and usage
- `tags`: Keywords for categorization and filtering
- `dependencies`: Other extensions this extension depends on
- `providers`: List of providers this extension registers
- `platforms`: Platform-specific metadata
- `mcp`: Model Context Protocol server configuration

#### Extension Capabilities

Extensions can declare the following capabilities in their manifest:

- **`custom-commands`**: Expose new command groups and commands to azd
- **`lifecycle-events`**: Subscribe to azd project and service lifecycle events
- **`mcp-server`**: Provide Model Context Protocol tools for AI agents
- **`service-target-provider`**: Provide custom service deployment targets
- **`framework-service-provider`**: Provide custom language frameworks and build systems

#### Complete Extension Manifest Example

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/extension.schema.json

id: microsoft.azd.demo
namespace: demo
displayName: Demo Extension
description: This extension provides examples of the AZD extension framework.
usage: azd demo <command> [options]
version: 0.1.0
entryPoint: demo

capabilities:
  - custom-commands
  - lifecycle-events
  - service-target-provider
  - framework-service-provider
  - mcp-server

examples:
  - name: context
    description: Displays the current azd project & environment context.
    usage: azd demo context
  - name: prompt
    description: Display prompt capabilities.
    usage: azd demo prompt
  - name: deploy-vm
    description: Deploy application to virtual machine using custom service target.
    usage: azd demo deploy-vm

tags:
  - demo
  - example
  - development

dependencies:
  - id: microsoft.azd.core
    version: "^1.0.0"

providers:
  - name: demo-vm
    type: service-target
    description: Custom VM deployment target for demonstration purposes
  - name: rust-framework
    type: framework-service
    description: Custom Rust language framework support

platforms:
  windows:
    executable: demo.exe
  linux:
    executable: demo
  darwin:
    executable: demo

mcp:
  serve:
    args: ["mcp", "serve"]
    env: ["DEMO_CONFIG=production"]
```

#### Extension Dependencies

Extensions can declare dependencies on other extensions using the `dependencies` array:

```yaml
dependencies:
  - id: microsoft.azd.ai.builder
    version: "^1.2.0"
  - id: contoso.custom.tools
    version: "~2.1.0"
```

Dependencies support semantic versioning constraints:
- `^1.0.0`: Compatible with version 1.x.x
- `~1.2.0`: Compatible with version 1.2.x
- `>=1.0.0 <2.0.0`: Range specification

#### Provider Registration

When your extension provides custom service targets or framework services, declare them in the `providers` section:

```yaml
providers:
  - name: azure-vm
    type: service-target
    description: Deploy applications to Azure Virtual Machines
  - name: go-gin
    type: framework-service
    description: Support for Go applications using the Gin framework
```

This metadata helps azd understand what providers your extension offers and enables proper capability validation.

#### Model Context Protocol (MCP) Configuration

For extensions that provide AI agent tools, configure the MCP server:

```yaml
capabilities:
  - mcp-server

mcp:
  serve:
    args: ["mcp", "serve"]
    env: 
      - "API_KEY=${API_KEY}"
      - "LOG_LEVEL=info"
```

The `mcp.serve.args` specifies the command arguments to start your MCP server, while `env` sets additional environment variables.

#### Platform-Specific Configuration

Extensions can provide platform-specific metadata for different operating systems:

```yaml
platforms:
  windows:
    executable: myext.exe
    installScript: install.ps1
  linux:
    executable: myext
    installScript: install.sh
  darwin:
    executable: myext
    installScript: install.sh
```

#### Basic Extension Manifest Example

Here's a simple extension manifest for getting started:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/extension.schema.json

id: microsoft.azd.demo
namespace: demo
displayName: Demo Extension
description: This extension provides examples of the AZD extension framework.
usage: azd demo <command> [options]
version: 0.1.0
capabilities:
  - custom-commands
  - lifecycle-events
examples:
  - name: context
    description: Displays the current `azd` project & environment context.
    usage: azd demo context
  - name: prompt
    description: Display prompt capabilities.
    usage: azd demo prompt
```

The following is an example of an [extension manifest](../extensions/microsoft.azd.demo/extension.yaml).

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/registry.schema.json

id: microsoft.azd.demo
namespace: demo
displayName: Demo Extension
description: This extension provides examples of the AZD extension framework.
usage: azd demo <command> [options]
version: 0.1.0
capabilities:
  - custom-commands
  - lifecycle-events
examples:
  - name: context
    description: Displays the current `azd` project & environment context.
    usage: azd demo context
  - name: prompt
    description: Display prompt capabilities.
    usage: azd demo prompt
```

### Invoking Extension Commands

When `azd` invokes an extension command, the following steps occur:

1. `azd` starts a gRPC server on a random port.
2. `azd` invokes your command, passing all arguments and flags:
    - An environment variable named `AZD_SERVER` is set with the server address and random port (e.g., `localhost:12345`).
    - An `azd` access token environment variable `AZD_ACCESS_TOKEN` is set which is a JWT token signed with a randomly generated key good for the lifetime of the command. The token includes claims that identify each unique extensions and its supported capabilities.
    - Additional environment variables from the current `azd` environment are also set.
3. The extension command can communicate with `azd` through [extension framework gRPC services](#grpc-services).
4. `azd` waits for the extension command to complete:
    - If a non-zero exit code is returned, `azd` reports the operation as an error.

To enable interaction with `azd` from within the extension, the extension must leverage a gRPC client and connect to the server using the address specified in the `AZD_SERVER` environment variable.

The gRPC client must also include an `authorization` parameter with the value from `AZD_ACCESS_TOKEN`; otherwise, requests will fail due to invalid authorization. Extensions must declare their supported capabilities within the extension registry otherwise certain service operations may fail with a permission denied error.

#### Go

For extensions built using Go, the `azdext` package provides an `AzdClient` which acts as the gRPC client.

#### Other Languages

For extensions authored in other programming languages, the [gRPC proto files](../grpc/proto/) can be used to generate clients in your preferred language.

### How to set `azd` access token on requests

The following example shows how an outgoing Go context can be constructed with the `azd` access token.

```go
// WithAccessToken sets the access token for the `azd` client into a new Go context.
func WithAccessToken(ctx context.Context, params ...string) context.Context {
    tokenValue := strings.Join(params, "")
    if tokenValue == "" {
        tokenValue = os.Getenv("AZD_ACCESS_TOKEN")
    }

    md := metadata.Pairs("authorization", tokenValue)
    return metadata.NewOutgoingContext(ctx, md)
}
```

### How to call `azd` gRPC service

```go
// Create a new context that includes the AZD access token
ctx := azdext.WithAccessToken(cmd.Context())

// Create a new AZD client
// The constructor function automatically constructs the address
// from the `AZD_SERVER` environment variable but can be overridden if required.
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}

defer azdClient.Close()

getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
if err != nil {
    // No project found
    return err
}

// Print out the `azd` project name
fmt.Println(getProjectResponse.Project.Name)
```

### How to subscribe to lifecycle events

The following is an example of subscribing to project & service lifecycle events within an `azd` template.

In this example the extension is leveraging the `azdext.NewExtensionHost` constructor. This provides a fluent API to register event handlers, service target providers, and other extension capabilities in a unified way.

Other languages will need to manually handle the different message types invoked by the service.

```go
// Create a new context that includes the AZD access token.
ctx := azdext.WithAccessToken(cmd.Context())

// Create a new AZD client.
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}
defer azdClient.Close()

// Create an extension host and register event handlers using the fluent API
host := azdext.NewExtensionHost(azdClient).
    WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
        // This is your event handler code
        for i := 1; i <= 20; i++ {
            fmt.Printf("%d. Doing important work in extension...\n", i)
            time.Sleep(250 * time.Millisecond)
        }

        return nil
    }).
    WithServiceEventHandler("prepackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
        // Access service context with artifacts from previous phases
        fmt.Printf("Processing service: %s\n", args.Service.Name)
        
        // Check build artifacts from previous build phase
        if len(args.ServiceContext.Build) > 0 {
            fmt.Printf("Found %d build artifacts:\n", len(args.ServiceContext.Build))
            for _, artifact := range args.ServiceContext.Build {
                fmt.Printf("  - %s (kind: %s)\n", artifact.Path, artifact.Kind)
            }
        }
        
        // Check restore artifacts
        if len(args.ServiceContext.Restore) > 0 {
            fmt.Printf("Found %d restore artifacts:\n", len(args.ServiceContext.Restore))
            for _, artifact := range args.ServiceContext.Restore {
                fmt.Printf("  - %s\n", artifact.Path)
            }
        }

        // Perform your custom packaging logic here
        for i := 1; i <= 10; i++ {
            fmt.Printf("%d. Preparing package for %s...\n", i, args.Service.Name)
            time.Sleep(250 * time.Millisecond)
        }

        return nil
    }, &azdext.ServiceEventOptions{
        // Optionally filter your subscription by service host and/or language
        Host: "containerapp",
        Language: "python",
    }).
    WithServiceEventHandler("postdeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
        // Access deployment results
        fmt.Printf("Service %s deployment completed\n", args.Service.Name)
        
        // Check deployment artifacts
        for _, artifact := range args.ServiceContext.Deploy {
            if artifact.Kind == azdext.ARTIFACT_KIND_ENDPOINT {
                fmt.Printf("Service endpoint: %s\n", artifact.Path)
            } else if artifact.Kind == azdext.ARTIFACT_KIND_DEPLOYMENT {
                fmt.Printf("Deployment ID: %s\n", artifact.Path)
            }
        }

        return nil
    }, nil)

// Start the extension host
// This is a blocking call and will not return until the server connection is closed.
if err := host.Run(ctx); err != nil {
    return fmt.Errorf("failed to run extension: %w", err)
}

```

## Developer Artifacts

`azd` leverages gRPC for the communication protocol between Core `azd` and extensions. gRPC client & server components are automatically generated from profile files.

- Proto files @ [grpc/proto](../grpc/proto/)
- Generated files @ [pkg/azdext](../pkg/azdext)
- Make file @ [Makefile](../Makefile)

To re-generate gRPC clients:

- Run `protoc --version` to check if `protoc` is installed. If not, download and install it from [GitHub](https://github.com/protocolbuffers/protobuf/releases).
- Run `make --version` to check if `make` is installed.
- Run `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` to install the required Go tools.
- Run `make proto` from the `~/cli/azd` folder of the repo in `Git Bash`.
- Run `../../eng/scripts/copyright-check.sh . --fix` to add copyright.

## gRPC Services

The following are a list of available gRPC services for extension developer to integrate with `azd` core.

### Table of Contents

- [Project Service](#project-service)
- [Environment Service](#environment-service)
- [User Config Service](#user-config-service)
- [Deployment Service](#deployment-service)
- [Account Service](#account-service)
- [Prompt Service](#prompt-service)
- [Event Service](#event-service)
- [Container Service](#container-service)
- [Framework Service](#framework-service)
- [Service Target Service](#service-target-service)
- [Compose Service](#compose-service)
- [Workflow Service](#workflow-service)

---

### Project Service

This service manages project configuration retrieval and related operations.

> See [project.proto](../grpc/proto/project.proto) for more details.

#### Get

Gets the current project configuration.

- **Request:** _EmptyRequest_ (no fields)
- **Response:** _GetProjectResponse_
  - Contains **ProjectConfig**:
    - `name` (string)
    - `resource_group_name` (string)
    - `path` (string)
    - `metadata`: `{ template: string }`
    - `services`: map of _ServiceConfig_
    - `infra`: _InfraOptions_

#### AddService

Adds a new service to the project.

- **Request:** _AddServiceRequest_
  - Contains:
    - `service`: _ServiceConfig_
- **Response:** _EmptyResponse_

---

### Environment Service

This service handles environment management including retrieval, selection, and key-value operations.

> See [environment.proto](../grpc/proto/environment.proto) for more details.

#### GetCurrent

Gets the current environment.

- **Request:** _EmptyRequest_
- **Response:** _EnvironmentResponse_ (includes an _Environment_ with `name`)

#### List

Retrieves all azd environments.

- **Request:** _EmptyRequest_
- **Response:** _EnvironmentListResponse_ (list of _EnvironmentDescription_)

#### Get

Retrieves an environment by its name.

- **Request:** _GetEnvironmentRequest_
  - `name` (string)
- **Response:** _EnvironmentResponse_
  - Contains an **Environment** with `name` (string)

#### Select

Sets the selected environment as default.

- **Request:** _SelectEnvironmentRequest_
  - `name` (string)
- **Response:** _EmptyResponse_

#### GetValues

Retrieves all key-value pairs in the specified environment.

- **Request:** _GetEnvironmentRequest_
  - `name` (string)
- **Response:** _KeyValueListResponse_
  - Contains a list of **KeyValue**:
    - `key` (string)
    - `value` (string)

#### GetValue

Retrieves the value of a specific key.

- **Request:** _GetEnvRequest_
  - `env_name` (string)  
  - `key` (string)
- **Response:** _KeyValueResponse_
  - Contains:
    - `key` (string)
    - `value` (string)

#### SetValue

Sets the value of a key in an environment.

- **Request:** _SetEnvRequest_
  - `env_name` (string)  
  - `key` (string)  
  - `value` (string)
- **Response:** _EmptyResponse_

#### GetConfig

Retrieves a config value by path.

- **Request:** _GetConfigRequest_
  - `path` (string)
- **Response:** _GetConfigResponse_
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetConfigString

Retrieves a config value as a string by path.

- **Request:** _GetConfigStringRequest_
  - `path` (string)
- **Response:** _GetConfigStringResponse_
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetConfigSection

Retrieves a config section by path.

- **Request:** _GetConfigSectionRequest_
  - `path` (string)
- **Response:** _GetConfigSectionResponse_
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### SetConfig

Sets a config value at a given path.

- **Request:** _SetConfigRequest_
  - `path` (string)  
  - `value` (bytes)
- **Response:** _EmptyResponse_

#### UnsetConfig

Removes a config value at a given path.

- **Request:** _UnsetConfigRequest_
  - `path` (string)
- **Response:** _EmptyResponse_

---

### User Config Service

This service manages user-specific configuration retrieval and updates.

> See [user_config.proto](../grpc/proto/user_config.proto) for more details.

#### Get

Retrieves a user configuration value by path.

- **Request:** _GetRequest_
  - `path` (string)
- **Response:** _GetResponse_
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetString

Retrieves a user configuration value as a string.

- **Request:** _GetStringRequest_
  - `path` (string)
- **Response:** _GetStringResponse_
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetSection

Retrieves a section of the user configuration by path.

- **Request:** _GetSectionRequest_
  - `path` (string)
- **Response:** _GetSectionResponse_
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### Set

Sets a user configuration value.

- **Request:** _SetRequest_
  - `path` (string)
  - `value` (bytes)
- **Response:** _SetResponse_
  - Contains:
    - `status` (string)

#### Unset

Removes a user configuration value.

- **Request:** _UnsetRequest_
  - `path` (string)
- **Response:** _UnsetResponse_
  - Contains:
    - `status` (string)

---

### Deployment Service

This service provides operations for deployment retrieval and context management.

> See [deployment.proto](../grpc/proto/deployment.proto) for more details.

#### GetDeployment

Retrieves the latest Azure deployment provisioned by azd.

- **Request:** _EmptyRequest_
- **Response:** _GetDeploymentResponse_
  - Contains **Deployment**:
    - `id` (string)
    - `location` (string)
    - `deploymentId` (string)
    - `name` (string)
    - `type` (string)
    - `tags` (map<string, string>)
    - `outputs` (map<string, string>)
    - `resources` (repeated string)

#### GetDeploymentContext

Retrieves the current deployment context.

- **Request:** _EmptyRequest_
- **Response:** _GetDeploymentContextResponse_
  - Contains **AzureContext**:
    - `scope` with:
      - `tenantId` (string)
      - `subscriptionId` (string)
      - `location` (string)
      - `resourceGroup` (string)
    - `resources` (repeated string)

---

### Prompt Service

This service manages user prompt interactions for subscriptions, locations, resources, and confirmations.

> See [prompt.proto](../grpc/proto/prompt.proto) for more details.

#### PromptSubscription

Prompts the user to select a subscription.

- **Request:** _PromptSubscriptionRequest_ (empty)
- **Response:** _PromptSubscriptionResponse_
  - Contains **Subscription**

#### PromptLocation

Prompts the user to select a location.

- **Request:** _PromptLocationRequest_
  - `azure_context` (AzureContext)
- **Response:** _PromptLocationResponse_
  - Contains **Location**

#### PromptResourceGroup

Prompts the user to select a resource group.

- **Request:** _PromptResourceGroupRequest_
  - `azure_context` (AzureContext)
- **Response:** _PromptResourceGroupResponse_
  - Contains **ResourceGroup**

#### Confirm

Prompts the user to confirm an action.

- **Request:** _ConfirmRequest_
  - `options` (ConfirmOptions) with:
    - `default_value` (optional bool)
    - `message` (string)
    - `helpMessage` (string)
    - `hint` (string)
    - `placeholder` (string)
- **Response:** _ConfirmResponse_
  - Contains an optional `value` (bool)

#### Prompt

Prompts the user for text input.

- **Request:** _PromptRequest_
  - `options` (PromptOptions) with:
    - `message` (string)
    - `help_message` (string)
    - `hint` (string)
    - `placeholder` (string)
    - `validation_message` (string)
    - `required_message` (string)
    - `required` (bool)
    - `defaultValue` (string)
    - `clear_on_completion` (bool)
    - `ignore_hint_keys` (bool)
- **Response:** _PromptResponse_
  - Contains `value` (string)

#### Select

Prompts the user to select an option from a list.

- **Request:** _SelectRequest_
  - `options` (SelectOptions) with:
    - `SelectedIndex` (optional int32)
    - `message` (string)
    - `allowed` (repeated string)
    - `help_message` (string)
    - `hint` (string)
    - `display_count` (int32)
    - `display_numbers` (optional bool)
    - `enable_filtering` (optional bool)
- **Response:** _SelectResponse_
  - Contains an optional `value` (int32)

#### MultiSelect

Prompts the user to select multiple options from a list.

- **Request:** _MultiSelectRequest_
  - `options` (MultiSelectOptions) with:
    - `message` (string)
    - `choices` (repeated MultiSelectChoice) with:
      - `value` (string): The actual value
      - `display` (string): Display text for the choice
      - `selected` (bool): Whether initially selected
    - `help_message` (string)
    - `hint` (string)
    - `display_count` (int32)
- **Response:** _MultiSelectResponse_
  - Contains a list of selected **MultiSelectChoice** items

**Example Usage (Go):**

```go
// Prompt for multiple environment selections
ctx := azdext.WithAccessToken(cmd.Context())
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}
defer azdClient.Close()

choices := []*azdext.MultiSelectChoice{
    {Value: "dev", Display: "Development Environment", Selected: true},
    {Value: "staging", Display: "Staging Environment", Selected: false},
    {Value: "prod", Display: "Production Environment", Selected: false},
}

response, err := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
    Options: &azdext.MultiSelectOptions{
        Message:     "Select environments to deploy to:",
        Choices:     choices,
        HelpMessage: "Choose one or more environments for deployment",
        Hint:        "Use space to select, enter to confirm",
    },
})
if err != nil {
    return fmt.Errorf("failed to prompt for environments: %w", err)
}

for _, choice := range response.Values {
    fmt.Printf("Selected: %s (%s)\n", choice.Display, choice.Value)
}
```

#### PromptSubscriptionResource

Prompts the user to select a resource from a subscription.

- **Request:** _PromptSubscriptionResourceRequest_ with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) with:
    - `resource_type` (string)
    - `kinds` (repeated string)
    - `resource_type_display_name` (string)
    - `select_options` (PromptResourceSelectOptions)
- **Response:** _PromptSubscriptionResourceResponse_
  - Contains **ResourceExtended**

#### PromptResourceGroupResource

Prompts the user to select a resource from a resource group.

- **Request:** _PromptResourceGroupResourceRequest_ with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) (same structure as above)
- **Response:** _PromptResourceGroupResourceResponse_
  - Contains **ResourceExtended**

---

### Event Service

This service defines methods for event subscription, invocation, and status updates.  
Clients can subscribe to events and receive notifications via a bidirectional stream.

#### EventStream

- Establishes a bidirectional stream that enables clients to:
  - Subscribe to project and service events.
  - Invoke event handlers.
  - Send status updates regarding event processing.

> See [event.proto](../grpc/proto/event.proto) for more details.

#### Message Types

- **EventMessage**
  Encapsulates a single event payload among several possible types.

  Contains:
  - Uses a oneof field to encapsulate different event types.
- **ExtensionReadyEvent**
  Signals that an extension is ready, including any status updates.

  Contains:
  - `status`: Indicates the readiness state of the extension.
  - `message`: Provides additional details.
- **SubscribeProjectEvent**
  Allows clients to subscribe to events specific to project lifecycle changes.

  Contains:
  - `event_names`: A list of event names to subscribe to for project events.
- **SubscribeServiceEvent**
  Allows clients to subscribe to service-specific events along with context details.

  Contains:
  - `event_names`: A list of event names to subscribe to for service events.
  - `language`: The language of the service.
  - `host`: The host identifier.
- **InvokeProjectHandler**
  Instructs the invocation of a project event handler with configuration details.

  Contains:
  - `event_name`: The name of the event being invoked.
  - `project`: The project configuration.
- **InvokeServiceHandler**
  Instructs the invocation of a service event handler including associated configurations.

  Contains:
  - `event_name`: The name of the event being invoked.
  - `project`: The project configuration.
  - `service`: The specific service configuration.
  - `service_context`: The service context containing artifacts from all lifecycle phases (restore, build, package, publish, deploy).
- **ProjectHandlerStatus**
  Provides status updates for project events.

  Contains:
  - `event_name`: The event name for which this status update applies.
  - `status`: Status such as "running", "completed", or "failed".
  - `message`: Optional additional details.
- **ServiceHandlerStatus**
  Provides status updates for service events.

  Contains:
  - `event_name`: The event name for which this status update applies.
  - `service_name`: The name of the service.
  - `status`: Status such as "running", "completed", or "failed".
  - `message`: Optional additional details.

#### ServiceContext and Service Event Arguments

When service events are invoked, extensions receive a `ServiceEventArgs` structure that includes:

- `Project`: The current project configuration
- `Service`: The specific service configuration
- `ServiceContext`: Artifacts accumulated across all service lifecycle phases

**ServiceContext Structure:**

```go
type ServiceContext struct {
    Restore []*Artifact  // Artifacts from restore phase
    Build   []*Artifact  // Artifacts from build phase
    Package []*Artifact  // Artifacts from package phase
    Publish []*Artifact  // Artifacts from publish phase
    Deploy  []*Artifact  // Artifacts from deploy phase
}
```

**Artifact Definition:**

```go
type Artifact struct {
    Kind        ArtifactKind    // Type of artifact (Directory, Config, Archive, Container, etc.)
    Path        string          // Location of the artifact
    LocationKind LocationKind   // Whether it's local, remote, or service-based
}
```

**Artifact Kinds:**
- `ARTIFACT_KIND_DIRECTORY`: Directory containing project or build artifacts
- `ARTIFACT_KIND_CONFIG`: Configuration file
- `ARTIFACT_KIND_ARCHIVE`: Zip/archive package
- `ARTIFACT_KIND_CONTAINER`: Docker/container image
- `ARTIFACT_KIND_ENDPOINT`: Service endpoint URL
- `ARTIFACT_KIND_DEPLOYMENT`: Deployment result or endpoint
- `ARTIFACT_KIND_RESOURCE`: Azure Resource

**Example: Using ServiceContext in Event Handlers**

```go
host := azdext.NewExtensionHost(azdClient).
    WithServiceEventHandler("prepackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
        // Access build artifacts from previous phase
        for _, artifact := range args.ServiceContext.Build {
            fmt.Printf("Build artifact: %s (kind: %s)\n", artifact.Path, artifact.Kind)
        }
        
        // Access restore artifacts
        for _, artifact := range args.ServiceContext.Restore {
            fmt.Printf("Restore artifact: %s\n", artifact.Path)
        }
        
        return nil
    }, nil).
    WithServiceEventHandler("postdeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
        // Access deployment results
        for _, artifact := range args.ServiceContext.Deploy {
            if artifact.Kind == azdext.ARTIFACT_KIND_ENDPOINT {
                fmt.Printf("Service deployed to: %s\n", artifact.Path)
            }
        }
        
        return nil
    }, nil)
```

---

### Container Service

This service provides container build, package, and publish operations for extensions that need to work with containers but don't want to implement the full complexity of Docker CLI integration, registry authentication, etc.

> See [container.proto](../grpc/proto/container.proto) for more details.

#### Build

Builds a service's container image.

- **Request:** _ContainerBuildRequest_
  - Contains:
    - `service_name` (string): Name of the service to build
    - `service_context` (ServiceContext): Current service context with artifacts
- **Response:** _ContainerBuildResponse_
  - Contains:
    - `result` (ServiceBuildResult): Build result with artifacts

**Example Usage (Go):**

```go
// Build a container for a service
buildResponse, err := azdClient.Container().Build(ctx, &azdext.ContainerBuildRequest{
    ServiceName: "api",
    ServiceContext: &azdext.ServiceContext{
        Restore: restoreArtifacts,
        Build:   buildArtifacts,
    },
})
if err != nil {
    return fmt.Errorf("failed to build container: %w", err)
}

// Access build artifacts
for _, artifact := range buildResponse.Result.Artifacts {
    fmt.Printf("Built artifact: %s\n", artifact.Path)
}
```

#### Package

Packages a service's container for deployment.

- **Request:** _ContainerPackageRequest_
  - Contains:
    - `service_name` (string): Name of the service to package
    - `service_context` (ServiceContext): Current service context with artifacts
- **Response:** _ContainerPackageResponse_
  - Contains:
    - `result` (ServicePackageResult): Package result with artifacts

#### Publish

Publishes a container service to a registry.

- **Request:** _ContainerPublishRequest_
  - Contains:
    - `service_name` (string): Name of the service to publish
    - `service_context` (ServiceContext): Current service context with artifacts
- **Response:** _ContainerPublishResponse_
  - Contains:
    - `result` (ServicePublishResult): Publish result with artifacts

**Complete Container Workflow Example (Go):**

```go
ctx := azdext.WithAccessToken(cmd.Context())
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}
defer azdClient.Close()

serviceName := "web-api"
serviceContext := &azdext.ServiceContext{}

// Build the container
buildResp, err := azdClient.Container().Build(ctx, &azdext.ContainerBuildRequest{
    ServiceName:    serviceName,
    ServiceContext: serviceContext,
})
if err != nil {
    return fmt.Errorf("container build failed: %w", err)
}

// Update context with build artifacts
serviceContext.Build = buildResp.Result.Artifacts

// Package the container
packageResp, err := azdClient.Container().Package(ctx, &azdext.ContainerPackageRequest{
    ServiceName:    serviceName,
    ServiceContext: serviceContext,
})
if err != nil {
    return fmt.Errorf("container package failed: %w", err)
}

// Update context with package artifacts
serviceContext.Package = packageResp.Result.Artifacts

// Publish the container
publishResp, err := azdClient.Container().Publish(ctx, &azdext.ContainerPublishRequest{
    ServiceName:    serviceName,
    ServiceContext: serviceContext,
})
if err != nil {
    return fmt.Errorf("container publish failed: %w", err)
}

fmt.Printf("Container published successfully with %d artifacts\n", len(publishResp.Result.Artifacts))
```

---

### Framework Service

This service handles language and framework-specific operations like restore, build, and package for services. Extensions can register framework service providers to handle custom languages or override default behavior.

> See [framework_service.proto](../grpc/proto/framework_service.proto) for more details.

#### Provider Interface

Framework service providers implement the `FrameworkServiceProvider` interface:

```go
type FrameworkServiceProvider interface {
    Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
    RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) ([]*ExternalTool, error)
    Requirements() (*FrameworkRequirements, error)
    Restore(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, progress ProgressReporter) (*ServiceRestoreResult, error)
    Build(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, progress ProgressReporter) (*ServiceBuildResult, error)
    Package(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, progress ProgressReporter) (*ServicePackageResult, error)
}
```

#### Stream

The framework service uses a bidirectional stream for communication between azd and the extension.

- **Request/Response:** _FrameworkServiceMessage_ (bidirectional stream)
  - Contains various message types:
    - `RegisterFrameworkServiceRequest/Response`: Register a framework provider
    - `FrameworkServiceInitializeRequest/Response`: Initialize the service
    - `FrameworkServiceRequiredExternalToolsRequest/Response`: Get required tools
    - `FrameworkServiceRequirementsRequest/Response`: Get framework requirements
    - `FrameworkServiceRestoreRequest/Response`: Restore dependencies
    - `FrameworkServiceBuildRequest/Response`: Build the service
    - `FrameworkServicePackageRequest/Response`: Package the service

**Example: Registering a Framework Service Provider (Go):**

```go
// Custom Rust framework provider
type RustFrameworkProvider struct{}

func (r *RustFrameworkProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
    // Initialize Rust-specific settings
    return nil
}

func (r *RustFrameworkProvider) RequiredExternalTools(ctx context.Context, serviceConfig *azdext.ServiceConfig) ([]*azdext.ExternalTool, error) {
    return []*azdext.ExternalTool{
        {
            Name:       "cargo",
            InstallUrl: "https://rustup.rs/",
        },
    }, nil
}

func (r *RustFrameworkProvider) Requirements() (*azdext.FrameworkRequirements, error) {
    return &azdext.FrameworkRequirements{
        Package: &azdext.FrameworkPackageRequirements{
            RequireRestore: true,
            RequireBuild:   true,
        },
    }, nil
}

func (r *RustFrameworkProvider) Restore(ctx context.Context, serviceConfig *azdext.ServiceConfig, serviceContext *azdext.ServiceContext, progress azdext.ProgressReporter) (*azdext.ServiceRestoreResult, error) {
    // Run cargo fetch or similar
    return &azdext.ServiceRestoreResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_DIRECTORY,
                Path: "target/deps",
            },
        },
    }, nil
}

func (r *RustFrameworkProvider) Build(ctx context.Context, serviceConfig *azdext.ServiceConfig, serviceContext *azdext.ServiceContext, progress azdext.ProgressReporter) (*azdext.ServiceBuildResult, error) {
    // Run cargo build
    return &azdext.ServiceBuildResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_DIRECTORY,
                Path: "target/release",
            },
        },
    }, nil
}

func (r *RustFrameworkProvider) Package(ctx context.Context, serviceConfig *azdext.ServiceConfig, serviceContext *azdext.ServiceContext, progress azdext.ProgressReporter) (*azdext.ServicePackageResult, error) {
    // Package Rust application
    return &azdext.ServicePackageResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_ARCHIVE,
                Path: "dist/app.tar.gz",
            },
        },
    }, nil
}

// Register the framework provider
func main() {
    ctx := azdext.WithAccessToken(context.Background())
    azdClient, err := azdext.NewAzdClient()
    if err != nil {
        log.Fatal(err)
    }
    defer azdClient.Close()

    host := azdext.NewExtensionHost(azdClient).
        WithFrameworkService("rust", func() azdext.FrameworkServiceProvider {
            return &RustFrameworkProvider{}
        })

    if err := host.Run(ctx); err != nil {
        log.Fatalf("failed to run extension: %v", err)
    }
}
```

---

### Service Target Service

This service handles the full deployment lifecycle for services, including packaging, publishing, and deploying to Azure resources. Extensions can register service target providers for custom deployment scenarios.

> See [service_target.proto](../grpc/proto/service_target.proto) for more details.

#### Provider Interface

Service target providers implement the `ServiceTargetProvider` interface:

```go
type ServiceTargetProvider interface {
    Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
    GetTargetResource(ctx context.Context, subscriptionId string, serviceConfig *ServiceConfig) (*TargetResource, error)
    Package(ctx context.Context, serviceConfig *ServiceConfig, frameworkPackage *ServicePackageResult, progress ProgressReporter) (*ServicePackageResult, error)
    Publish(ctx context.Context, serviceConfig *ServiceConfig, servicePackage *ServicePackageResult, targetResource *TargetResource, progress ProgressReporter) (*ServicePublishResult, error)
    Deploy(ctx context.Context, serviceConfig *ServiceConfig, servicePackage *ServicePackageResult, servicePublish *ServicePublishResult, targetResource *TargetResource, progress ProgressReporter) (*ServiceDeployResult, error)
    Endpoints(ctx context.Context, serviceConfig *ServiceConfig, targetResource *TargetResource) ([]string, error)
}
```

#### Stream

The service target service uses a bidirectional stream for communication between azd and the extension.

- **Request/Response:** _ServiceTargetMessage_ (bidirectional stream)
  - Contains various message types:
    - `RegisterServiceTargetRequest/Response`: Register a service target provider
    - `ServiceTargetInitializeRequest/Response`: Initialize the service target
    - `GetTargetResourceRequest/Response`: Get the target Azure resource
    - `ServiceTargetPackageRequest/Response`: Package the service
    - `ServiceTargetPublishRequest/Response`: Publish the service
    - `ServiceTargetDeployRequest/Response`: Deploy the service
    - `ServiceTargetEndpointsRequest/Response`: Get service endpoints

**Example: Custom Service Target Provider (Go):**

```go
// Custom VM service target provider
type VMServiceTargetProvider struct{}

func (v *VMServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
    // Initialize VM-specific settings
    return nil
}

func (v *VMServiceTargetProvider) GetTargetResource(ctx context.Context, subscriptionId string, serviceConfig *azdext.ServiceConfig) (*azdext.TargetResource, error) {
    return &azdext.TargetResource{
        ResourceId: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", 
            subscriptionId, serviceConfig.ResourceGroupName, serviceConfig.ResourceName),
    }, nil
}

func (v *VMServiceTargetProvider) Package(ctx context.Context, serviceConfig *azdext.ServiceConfig, frameworkPackage *azdext.ServicePackageResult, progress azdext.ProgressReporter) (*azdext.ServicePackageResult, error) {
    // Create deployment package for VM
    return &azdext.ServicePackageResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_ARCHIVE,
                Path: "vm-deploy.zip",
            },
        },
    }, nil
}

func (v *VMServiceTargetProvider) Publish(ctx context.Context, serviceConfig *azdext.ServiceConfig, servicePackage *azdext.ServicePackageResult, targetResource *azdext.TargetResource, progress azdext.ProgressReporter) (*azdext.ServicePublishResult, error) {
    // Upload package to storage or registry
    return &azdext.ServicePublishResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_ENDPOINT,
                Path: "https://storage.azure.com/packages/vm-deploy.zip",
            },
        },
    }, nil
}

func (v *VMServiceTargetProvider) Deploy(ctx context.Context, serviceConfig *azdext.ServiceConfig, servicePackage *azdext.ServicePackageResult, servicePublish *azdext.ServicePublishResult, targetResource *azdext.TargetResource, progress azdext.ProgressReporter) (*azdext.ServiceDeployResult, error) {
    // Deploy to VM using scripts or ARM templates
    return &azdext.ServiceDeployResult{
        Artifacts: []*azdext.Artifact{
            {
                Kind: azdext.ARTIFACT_KIND_DEPLOYMENT,
                Path: "deployment-12345",
            },
        },
    }, nil
}

func (v *VMServiceTargetProvider) Endpoints(ctx context.Context, serviceConfig *azdext.ServiceConfig, targetResource *azdext.TargetResource) ([]string, error) {
    // Return VM endpoints
    return []string{"https://myvm.azure.com:8080"}, nil
}

// Register the service target provider
func main() {
    ctx := azdext.WithAccessToken(context.Background())
    azdClient, err := azdext.NewAzdClient()
    if err != nil {
        log.Fatal(err)
    }
    defer azdClient.Close()

    host := azdext.NewExtensionHost(azdClient).
        WithServiceTarget("vm", func() azdext.ServiceTargetProvider {
            return &VMServiceTargetProvider{}
        })

    if err := host.Run(ctx); err != nil {
        log.Fatalf("failed to run extension: %v", err)
    }
}
```

---

### Compose Service

This service manages composability resources in an AZD project.

> See [compose.proto](../grpc/proto/compose.proto) for more details.

#### ListResources

Lists all configured composability resources.

- **Request:** _EmptyRequest_
- **Response:** _ListResourcesResponse_
  - Contains a list of **ComposedResource**

#### GetResource

Retrieves the configuration of a specific composability resource.

- **Request:** _GetResourceRequest_
  - Contains:
    - `name` (string)
- **Response:** _GetResourceResponse_
  - Contains:
    - `resource`: _ComposedResource_

#### ListResourceTypes

Lists all supported composability resource types.

- **Request:** _EmptyRequest_
- **Response:** _ListResourceTypesResponse_
  - Contains a list of **ComposedResourceType**

#### GetResourceType

Retrieves the schema of a specific composability resource type.

- **Request:** _GetResourceTypeRequest_
  - Contains:
    - `type_name` (string)
- **Response:** _GetResourceTypeResponse_
  - Contains:
    - `resource_type`: _ComposedResourceType_

#### AddResource

Adds a new composability resource to the project.

- **Request:** _AddResourceRequest_
  - Contains:
    - `resource`: _ComposedResource_
- **Response:** _AddResourceResponse_
  - Contains:
    - `resource`: _ComposedResource_

---

### Workflow Service

This service executes workflows defined within the project.

> See [workflow.proto](../grpc/proto/workflow.proto) for more details.

#### Run

Executes a workflow consisting of sequential steps.

- **Request:** _RunWorkflowRequest_
  - Contains:
    - `workflow`: _Workflow_ (with `name` and `steps`)
- **Response:** _EmptyResponse_

---

### Account Service

This service provides information about the currently logged-in user or identity.

> See [account.proto](../grpc/proto/account.proto) for more details.

#### ListSubscriptions

Lists all subscriptions accessible by the current account.

- **Request:** _ListSubscriptionsRequest_
  - Contains:
    - `tenant_id` (optional string): Filter subscriptions by tenant ID
- **Response:** _ListSubscriptionsResponse_
  - Contains a list of **Subscription**:
    - `id` (string): Subscription ID (GUID)
    - `name` (string): Subscription display name
    - `tenant_id` (string): The tenant that owns the subscription
    - `user_tenant_id` (string): The tenant under which the user has access to the subscription
    - `is_default` (bool): Whether this is the user's default subscription

**Example Usage (Go):**

```go
ctx := azdext.WithAccessToken(cmd.Context())
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}
defer azdClient.Close()

// List all subscriptions
response, err := azdClient.Account().ListSubscriptions(ctx, &azdext.ListSubscriptionsRequest{})
if err != nil {
    return fmt.Errorf("failed to list subscriptions: %w", err)
}

fmt.Printf("Found %d subscriptions:\n", len(response.Subscriptions))
for _, sub := range response.Subscriptions {
    defaultMarker := ""
    if sub.IsDefault {
        defaultMarker = " (default)"
    }
    fmt.Printf("  %s (%s)%s\n", sub.Name, sub.Id, defaultMarker)
    fmt.Printf("    Tenant: %s\n", sub.TenantId)
    if sub.UserTenantId != sub.TenantId {
        fmt.Printf("    User Tenant: %s\n", sub.UserTenantId)
    }
}

// Filter by tenant ID
tenantId := "your-tenant-id"
filteredResponse, err := azdClient.Account().ListSubscriptions(ctx, &azdext.ListSubscriptionsRequest{
    TenantId: &tenantId,
})
if err != nil {
    return fmt.Errorf("failed to list subscriptions for tenant: %w", err)
}

fmt.Printf("Subscriptions in tenant %s: %d\n", tenantId, len(filteredResponse.Subscriptions))
```

**Use Cases:**

- List available subscriptions for user selection
- Filter subscriptions by tenant in multi-tenant scenarios
- Display subscription details for user confirmation
- Find default subscription for automated operations

#### LookupTenant

Resolves the tenant ID required by the current account to access a given subscription. This is useful in multi-tenant scenarios where a user may have access to subscriptions through different tenants.

- **Request:** _LookupTenantRequest_
  - Contains:
    - `subscription_id` (string): The subscription ID to look up
- **Response:** _LookupTenantResponse_
  - Contains:
    - `tenant_id` (string): The tenant ID that provides access to the subscription

**Example Usage (Go):**

```go
// Look up the tenant for a specific subscription
subscriptionId := "12345678-1234-1234-1234-123456789abc"
response, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
    SubscriptionId: subscriptionId,
})
if err != nil {
    return fmt.Errorf("failed to lookup tenant for subscription %s: %w", subscriptionId, err)
}

fmt.Printf("Subscription %s requires tenant %s for access\n", subscriptionId, response.TenantId)

// Complete example: Get subscription info and tenant
func getSubscriptionDetails(ctx context.Context, azdClient *azdext.AzdClient, subscriptionId string) error {
    // First lookup the required tenant
    tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
        SubscriptionId: subscriptionId,
    })
    if err != nil {
        return fmt.Errorf("failed to lookup tenant: %w", err)
    }

    // Then get subscription details using the tenant filter
    subsResp, err := azdClient.Account().ListSubscriptions(ctx, &azdext.ListSubscriptionsRequest{
        TenantId: &tenantResp.TenantId,
    })
    if err != nil {
        return fmt.Errorf("failed to list subscriptions: %w", err)
    }

    // Find the specific subscription
    for _, sub := range subsResp.Subscriptions {
        if sub.Id == subscriptionId {
            fmt.Printf("Subscription Details:\n")
            fmt.Printf("  Name: %s\n", sub.Name)
            fmt.Printf("  ID: %s\n", sub.Id)
            fmt.Printf("  Tenant: %s\n", sub.TenantId)
            fmt.Printf("  User Tenant: %s\n", sub.UserTenantId)
            fmt.Printf("  Is Default: %t\n", sub.IsDefault)
            break
        }
    }

    return nil
}
```

**Use Cases:**

- Determine which tenant ID to use when making Azure API calls for a subscription
- Handle multi-tenant scenarios where users access subscriptions through different tenants
- Validate subscription access before performing operations
- Set up proper authentication context for Azure SDK calls
