# DotNet Infrastructure Provider

The `dotnet` infrastructure provider enables defining Azure infrastructure in C# using the
[Azure.Provisioning](https://learn.microsoft.com/dotnet/api/overview/azure/provisioning-readme) library.
Bicep is generated automatically as an intermediate step—you never need to write or manage Bicep files directly.

## Prerequisites

- [.NET 10 SDK](https://dotnet.microsoft.com/download) or later (for single-file support)
- Azure CLI (`az login`) or `azd auth login` for Azure authentication

## Quick Start

### 1. Create a single C# file

Create an `infra/` directory with a single `.cs` file. Use `#:package` directives to declare NuGet dependencies inline
(no `.csproj` required):

```csharp
// infra/infra.cs
#:package Azure.Provisioning@1.6.0-alpha.20260325.1
#:package Azure.Provisioning.Storage@1.1.2

using Azure.Provisioning;
using Azure.Provisioning.Storage;

var outputDir = args.Length > 0 ? args[0] : "./generated";
Directory.CreateDirectory(outputDir);

var infra = new Infrastructure("main");

var storageAccount = new StorageAccount("myStorage")
{
    Kind = StorageKind.StorageV2,
    Sku = new StorageSku { Name = StorageSkuName.StandardLrs },
};
infra.Add(storageAccount);

infra.Build().Save(outputDir);
```

### 2. Configure azure.yaml

```yaml
name: my-app
infra:
  provider: dotnet
  path: infra
```

### 3. Deploy

```bash
azd up
```

## How It Works

When you run `azd provision` (or `azd up`), the dotnet provider:

1. **Resolves** the C# entry point (a `.cs` file or `.csproj` project in the `infra.path` directory)
2. **Runs** `dotnet run <file.cs> -- <temp-dir>` to compile the C# code
3. **Generates** Bicep files into a temporary directory via `infrastructure.Build().Save()`
4. **Delegates** to the built-in Bicep provider for ARM deployment, parameter prompting, state tracking, and destruction

Bicep is never exposed to the user—it is a transparent intermediate format.

## Entry Point Resolution

The provider looks for infrastructure code in the directory specified by `infra.path` (default: `infra/`):

| Scenario | Behavior |
|----------|----------|
| Single `.cs` file in directory | Uses `dotnet run file.cs` (dotnet 10+ file-based app) |
| `.csproj` project in directory | Uses `dotnet run --project <dir>` (traditional project) |
| Direct `.cs` file path | Uses `dotnet run file.cs` |
| Direct `.csproj` file path | Uses `dotnet run --project <file>` |

### Single-file apps (recommended)

With .NET 10+, a single `.cs` file with `#:package` directives is the simplest approach:

```csharp
#:package Azure.Provisioning@1.6.0-alpha.20260325.1
#:package Azure.Provisioning.KeyVault@1.1.2

using Azure.Provisioning;
using Azure.Provisioning.KeyVault;

var outputDir = args.Length > 0 ? args[0] : "./generated";
Directory.CreateDirectory(outputDir);

var infra = new Infrastructure("main");
// ... define resources ...
infra.Build().Save(outputDir);
```

### Project-based apps

For complex scenarios with multiple files, use a traditional `.csproj`:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net10.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Azure.Provisioning" Version="1.6.0-alpha.20260325.1" />
    <PackageReference Include="Azure.Provisioning.Storage" Version="1.1.2" />
  </ItemGroup>
</Project>
```

## Contract

Your C# program must:

1. **Accept an output directory** as the first command-line argument (`args[0]`)
2. **Write `.bicep` files** to that directory (typically via `infrastructure.Build().Save(outputDir)`)
3. **Generate a `main.bicep`** file (or the name matching `infra.module`, which defaults to `main`)

## Passing Parameters

You can forward custom arguments to your C# program using the `--` separator:

```bash
azd provision -- --region westus3 --prefix myapp
```

Your program receives these as `args[1]`, `args[2]`, etc. (after the output directory in `args[0]`):

```csharp
// infra/infra.cs
#:package Azure.Provisioning@1.6.0-alpha.20260325.1
#:package Azure.Provisioning.Storage@1.1.2

using Azure.Provisioning;
using Azure.Provisioning.Storage;

var outputDir = args.Length > 0 ? args[0] : "./generated";
Directory.CreateDirectory(outputDir);

// Parse extra arguments forwarded from azd
var region = "eastus2";
var prefix = "test";

for (int i = 1; i < args.Length; i++)
{
    switch (args[i])
    {
        case "--region" when i + 1 < args.Length:
            region = args[++i];
            break;
        case "--prefix" when i + 1 < args.Length:
            prefix = args[++i];
            break;
    }
}

var infra = new Infrastructure("main");

var locationParam = new ProvisioningParameter("location", typeof(string))
{
    Value = new Azure.Provisioning.Expressions.StringLiteralExpression(region),
};
infra.Add(locationParam);

var storageAccount = new StorageAccount(prefix + "storage")
{
    Kind = StorageKind.StorageV2,
    Sku = new StorageSku { Name = StorageSkuName.StandardLrs },
};
infra.Add(storageAccount);

infra.Build().Save(outputDir);
```

This lets you define parameterized infrastructure that can be customized at deploy time without
modifying code or config files.

## Feature Stage

This provider is currently in **Alpha**. See [feature-stages.md](feature-stages.md) for definitions.
