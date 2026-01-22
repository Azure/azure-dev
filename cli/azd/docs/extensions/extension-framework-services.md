# Adding Custom Language Frameworks with Extensions

Azure Developer CLI (azd) extensions can provide custom language framework support beyond the built-in languages (Python, JavaScript, TypeScript, Java, .NET, etc.). This allows you to extend azd to support any programming language or build system that isn't natively supported.

## Overview

Framework service extensions enable you to:

- Add support for new programming languages (Rust, Go, PHP, Ruby, etc.)
- Implement custom build systems and dependency management
- Define language-specific packaging and deployment workflows
- Specify external tool requirements for your language
- Integrate with azd's lifecycle events (restore, build, package)

## Prerequisites

- Basic understanding of [azd extensions](./extension-framework.md)
- Go programming knowledge (extensions are written in Go)
- Familiarity with your target language's build tools and ecosystem

## Creating a Framework Service Extension

### 1. Extension Structure

Your extension needs to declare the `framework-service-provider` capability in its `extension.yaml`:

```yaml
# extension.yaml
id: my.custom.extension
namespace: my.extension
displayName: My Custom Language Extension
description: Adds support for Rust programming language
version: 1.0.0
capabilities:
  - framework-service-provider
  - lifecycle-events  # Optional: for additional lifecycle hooks
```

### 2. Implement the FrameworkServiceProvider Interface

Create a Go struct that implements the `azdext.FrameworkServiceProvider` interface:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure your provider implements the interface
var _ azdext.FrameworkServiceProvider = &RustFrameworkServiceProvider{}

type RustFrameworkServiceProvider struct {
    azdClient     *azdext.AzdClient
    serviceConfig *azdext.ServiceConfig
}

func NewRustFrameworkServiceProvider(azdClient *azdext.AzdClient) azdext.FrameworkServiceProvider {
    return &RustFrameworkServiceProvider{
        azdClient: azdClient,
    }
}
```

### 3. Implement Required Methods

#### Initialize Method

Called when the framework service is first set up for a service:

```go
func (p *RustFrameworkServiceProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
    fmt.Printf("Initializing Rust framework for service: %s\n", serviceConfig.GetName())
    p.serviceConfig = serviceConfig
    
    // Perform any initialization logic here
    // - Validate Cargo.toml exists
    // - Check Rust toolchain version
    // - Set up configuration
    
    return nil
}
```

#### RequiredExternalTools Method

Specify which external tools your language requires:

```go
func (p *RustFrameworkServiceProvider) RequiredExternalTools(
    ctx context.Context,
    serviceConfig *azdext.ServiceConfig,
) ([]*azdext.ExternalTool, error) {
    return []*azdext.ExternalTool{
        {
            Name:       "cargo",
            InstallUrl: "https://rustup.rs/",
        },
        {
            Name:       "rustc", 
            InstallUrl: "https://rustup.rs/",
        },
    }, nil
}
```

#### Requirements Method

Define what build phases your language needs:

```go
func (p *RustFrameworkServiceProvider) Requirements() (*azdext.FrameworkRequirements, error) {
    return &azdext.FrameworkRequirements{
        Package: &azdext.FrameworkPackageRequirements{
            RequireRestore: true, // Need dependency resolution
            RequireBuild:   true, // Need compilation step
        },
    }, nil
}
```

#### Restore Method

Handle dependency restoration (equivalent to `npm install`, `pip install`, etc.):

```go
func (p *RustFrameworkServiceProvider) Restore(
    ctx context.Context,
    serviceConfig *azdext.ServiceConfig,
    serviceContext *azdext.ServiceContext,
    progress azdext.ProgressReporter,
) (*azdext.ServiceRestoreResult, error) {
    progress("Installing Rust dependencies")
    
    // Your actual restore logic here:
    // - Run `cargo fetch` to download dependencies
    // - Validate Cargo.lock file
    // - Check for dependency conflicts
    
    progress("Cargo dependencies resolved")
    
    // Return artifacts instead of Details map
    restoreArtifacts := []*azdext.Artifact{
        {
            Kind:         "lock-file",
            Location:     "Cargo.lock",
            LocationKind: "local",
            Metadata: map[string]string{
                "timestamp":    time.Now().Format(time.RFC3339),
                "dependencyMgr": "cargo",
            },
        },
    }
    
    return &azdext.ServiceRestoreResult{
        Artifacts: restoreArtifacts,
    }, nil
}
```

#### Build Method

Handle the compilation/build process:

```go
func (p *RustFrameworkServiceProvider) Build(
    ctx context.Context,
    serviceConfig *azdext.ServiceConfig,
    serviceContext *azdext.ServiceContext,
    progress azdext.ProgressReporter,
) (*azdext.ServiceBuildResult, error) {
    progress("Compiling Rust project")
    
    // Your actual build logic here:
    // - Run `cargo build --release`
    // - Handle build errors
    // - Generate optimized binary
    // - Access previous restore artifacts from serviceContext.Restore if needed
    
    progress("Build completed successfully")
    
    binaryPath := fmt.Sprintf("target/release/%s", serviceConfig.GetName())
    
    // Return artifacts instead of Details map
    buildArtifacts := []*azdext.Artifact{
        {
            Kind:         "binary",
            Location:     binaryPath,
            LocationKind: "local",
            Metadata: map[string]string{
                "timestamp":   time.Now().Format(time.RFC3339),
                "buildMode":   "release",
                "target":      "x86_64-unknown-linux-gnu",
            },
        },
    }
    
    return &azdext.ServiceBuildResult{
        Artifacts: buildArtifacts,
    }, nil
}
```

#### Package Method

Handle creating deployable artifacts:

```go
func (p *RustFrameworkServiceProvider) Package(
    ctx context.Context,
    serviceConfig *azdext.ServiceConfig,
    serviceContext *azdext.ServiceContext,
    progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
    progress("Creating deployment package")
    
    // Your actual packaging logic here:
    // - Copy binary to deployment directory
    // - Include necessary runtime files
    // - Create container image or zip archive
    // - Generate deployment manifest
    // - Access build artifacts from serviceContext.Build if needed
    
    packagePath := fmt.Sprintf("%s-deployment.tar.gz", serviceConfig.GetName())
    progress("Package created: " + packagePath)
    
    // Return artifacts instead of Details map
    packageArtifacts := []*azdext.Artifact{
        {
            Kind:         "package",
            Location:     packagePath,
            LocationKind: "local",
            Metadata: map[string]string{
                "timestamp":    time.Now().Format(time.RFC3339),
                "packageType":  "tar.gz",
                "size":         "15.2MB",
            },
        },
    }
    
    return &azdext.ServicePackageResult{
        Artifacts: packageArtifacts,
    }, nil
}
```

### 4. Register the Framework Service

In your extension's `listen` command, register the framework service provider:

```go
func newListenCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "listen",
        Short: "Starts the extension and listens for events.",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := azdext.WithAccessToken(cmd.Context())
            
            azdClient, err := azdext.NewAzdClient()
            if err != nil {
                return fmt.Errorf("failed to create azd client: %w", err)
            }
            defer azdClient.Close()

            // Create your framework service provider
            rustFrameworkProvider := NewRustFrameworkServiceProvider(azdClient)
            
            // Register it with the extension host
            host := azdext.NewExtensionHost(azdClient).
                WithFrameworkService("rust", rustFrameworkProvider)
            
            // Start listening for events
            return host.Run(ctx)
        },
    }
}
```

## Using Your Custom Language Framework

### 1. Project Configuration

In your `azure.yaml` file, specify your custom language:

```yaml
# azure.yaml
name: my-rust-app
services:
  api:
    project: ./api
    language: rust  # This will use your extension's framework service
    host: containerapp
```

### 2. Service Configuration

Your service directory should contain the necessary files for your language:

```
api/
├── Cargo.toml          # Rust project manifest
├── Cargo.lock          # Dependency lock file
├── src/
│   └── main.rs         # Source code
└── Dockerfile          # Optional: custom container image
```

### 3. azd Commands

Once configured, standard azd commands will use your custom framework:

```bash
# Install dependencies using your Restore method
azd restore

# Compile using your Build method  
azd package

# Deploy (uses your Package output)
azd deploy
```

## Best Practices

### Error Handling

Always provide meaningful error messages and handle common failure scenarios:

```go
func (p *RustFrameworkServiceProvider) Build(ctx context.Context, serviceConfig *azdext.ServiceConfig, restoreOutput *azdext.ServiceRestoreResult, progress azdext.ProgressReporter) (*azdext.ServiceBuildResult, error) {
    // Check if Cargo.toml exists
    cargoTomlPath := filepath.Join(serviceConfig.GetPath(), "Cargo.toml")
    if _, err := os.Stat(cargoTomlPath); os.IsNotExist(err) {
        return nil, fmt.Errorf("Cargo.toml not found in %s. This doesn't appear to be a Rust project", serviceConfig.GetPath())
    }
    
    // Run build command with proper error handling
    cmd := exec.CommandContext(ctx, "cargo", "build", "--release")
    cmd.Dir = serviceConfig.GetPath()
    
    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("cargo build failed: %w", err)
    }
    
    // Rest of build logic...
}
```

### Progress Reporting

Use the progress reporter to keep users informed of long-running operations:

```go
func (p *RustFrameworkServiceProvider) Restore(ctx context.Context, serviceConfig *azdext.ServiceConfig, progress azdext.ProgressReporter) (*azdext.ServiceRestoreResult, error) {
    progress("Fetching Rust dependencies...")
    // Run cargo fetch
    
    progress("Resolving dependency tree...")
    // Check dependencies
    
    progress("Dependencies installed successfully")
    // Complete
}
```

### Configuration Support

Support common configuration patterns for your language:

```go
func (p *RustFrameworkServiceProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
    // Check for custom build flags in azure.yaml
    if buildFlags, exists := serviceConfig.GetEnv()["CARGO_BUILD_FLAGS"]; exists {
        p.customBuildFlags = strings.Split(buildFlags, " ")
    }
    
    // Check for target architecture
    if target, exists := serviceConfig.GetEnv()["RUST_TARGET"]; exists {
        p.targetArch = target
    }
    
    return nil
}
```

## Example: Complete Rust Framework Service

You can find a complete working example in the azd demo extension:

- Extension manifest: [`extensions/microsoft.azd.demo/extension.yaml`](../extensions/microsoft.azd.demo/extension.yaml)
- Framework service implementation: [`extensions/microsoft.azd.demo/internal/project/framework_service_demo.go`](../extensions/microsoft.azd.demo/internal/project/framework_service_demo.go)
- Registration: [`extensions/microsoft.azd.demo/internal/cmd/listen.go`](../extensions/microsoft.azd.demo/internal/cmd/listen.go)

## Troubleshooting

### Common Issues

1. **Extension not recognized**: Ensure `framework-service-provider` capability is declared in `extension.yaml`

2. **Language not found**: Check that your framework service is registered with the correct language name in `WithFrameworkService()`

3. **Build failures**: Verify external tools are installed and available in PATH

4. **Connection errors**: Make sure your extension implements the `listen` command correctly

### Debugging

Enable debug logging to see extension communication:

```bash
azd config set extension.debug true
azd restore --debug
```

## Related Documentation

- [Extension Framework Overview](./extension-framework.md)
- [Service Target Extensions](./extension-service-targets.md)
- [Extension Development Guide](./new-azd-command.md)

## Support

For questions and support:

- [GitHub Issues](https://github.com/Azure/azure-dev/issues)
- [Azure Developer CLI Documentation](https://docs.microsoft.com/azure/developer/azure-developer-cli/)