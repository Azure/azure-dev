# Fig spec generation (for VS Code terminal IntelliSense)

## Overview

The `azd completion fig` command automatically generates a [Fig autocomplete specification](https://fig.io/docs/reference/subcommand) from azd's Cobra command structure. This TypeScript-based spec powers **IntelliSense** in VS Code's integrated terminal, providing context-aware suggestions for commands, flags, and arguments.

**What is a Fig Spec?**

A Fig spec is a TypeScript object that declaratively describes a CLI tool's interface, including commands, subcommands, flags/options, positional arguments, and dynamic generators for context-aware completions. The type definition used by VS Code is available at [`index.d.ts`](https://github.com/microsoft/vscode/blob/main/extensions/terminal-suggest/src/completions/index.d.ts). The original reference documentation can be found on [Fig's official docs](https://fig.io/docs/reference/subcommand).

The Fig spec used for IntelliSense lives in the VS Code repo at [`extensions/terminal-suggest/src/completions/azd.ts`](https://github.com/microsoft/vscode/blob/main/extensions/terminal-suggest/src/completions/azd.ts).

A copy of it exists in this repo under `cli/azd/cmd/testdata/TestFigSpec.ts` for snapshot testing purposes.

## Usage

### Generating the spec

```bash
azd completion fig > azd.ts
```

### Testing locally

The generated spec is automatically tested via snapshot tests:

```bash
# Run tests
cd cli/azd
go test ./cmd -run TestFigSpec

# Update snapshot if command structure has changed, similar to the TestUsage tests
UPDATE_SNAPSHOTS=true go test ./cmd -run TestFigSpec
```

The snapshot is stored at `cli/azd/cmd/testdata/TestFigSpec.ts`.

## Updating the spec in VS Code

After azd command or flag changes have been released, update the Fig spec in the VS Code repository to keep IntelliSense up to date.

### Process

1. **Set up VS Code development environment**: Follow the [VS Code contribution guide](https://github.com/microsoft/vscode/wiki/How-to-Contribute) to clone and set up the VS Code repository for local development.

2. **Generate the updated spec**:
   ```bash
   azd completion fig > azd-spec.ts
   ```

3. **Update VS Code repository**: In your local VS Code checkout, update `extensions/terminal-suggest/src/completions/azd.ts` with the newly generated spec. You may need to add in the vscode copyright header.

4. **Build and run VS Code locally**: Follow the instructions in the [VS Code contribution guide](https://github.com/microsoft/vscode/wiki/How-to-Contribute).

5. **Test IntelliSense**: Open the integrated terminal, type `azd ` and verify that:
   - Commands and flags are suggested correctly
   - Dynamic completions work (e.g. `azd init -t <ctrl+space>` lists templates)
   - New/changed commands appear as expected

    Note that on Windows, only `pwsh` supports IntelliSense completions.

6. **Submit a PR**: Create a PR with your changes following VS Code's contribution guidelines. Example: [microsoft/vscode#272348](https://github.com/microsoft/vscode/pull/272348)

## Architecture

### Package structure

The Fig spec generation is implemented in `cli/azd/internal/figspec/`:

```
figspec/
├── types.go                  # Core data structures and interfaces
├── spec_builder.go           # Main spec generation logic
├── fig_generators.go         # Dynamic generator definitions
├── typescript_renderer.go    # TypeScript code generation
└── customizations.go         # azd-specific customizations
```

### Generation flow

```
Cobra Command Tree
       ↓
SpecBuilder.BuildSpec()
       ↓
   [Apply Customizations]
       ↓
TypeScript Renderer
       ↓
Fig Spec (.ts file)
```

1. **Input**: Cobra command tree with all commands, flags, and arguments
2. **Processing**: SpecBuilder walks the command tree and applies customizations
3. **Output**: TypeScript code defining a Fig spec object

### Key components

#### 1. SpecBuilder (`spec_builder.go`)

The central orchestrator that:
- Traverses the Cobra command hierarchy
- Extracts command metadata (names, descriptions, aliases)
- Generates flag/option specifications
- Parses argument definitions from command `Use` fields
- Applies customization providers for azd-specific behavior

**Key Methods:**
- `BuildSpec(root *cobra.Command)`: Entry point for spec generation
- `generateSubcommands()`: Recursively processes command tree
- `generateOptions()`: Converts Cobra flags to Fig options
- `generateCommandArgs()`: Extracts positional arguments

#### 2. Customizations (`customizations.go`)

Implements customization interfaces to add azd-specific intelligence:

- **`CustomSuggestionProvider`**: Static suggestions (e.g. `--provider github|azdo`)
- **`CustomGeneratorProvider`**: Dynamically generated suggestions (e.g. `azd env select` suggesting environment names)
- **`CustomArgsProvider`**: Custom argument patterns (e.g. `azd env set [key] [value]`)
- **`CustomFlagArgsProvider`**: Custom flag argument names (e.g. `from-package` → `file-path|image-tag`)

#### 3. Generators (`fig_generators.go`)

Defines [**generators**](https://fig.io/docs/reference/generator/basic) that execute azd commands to dynamically provide context-aware suggestions. The generators are implemented as TypeScript code embedded inline within `fig_generators.go`:

| Generator | Command | Purpose |
|-----------|---------|---------|
| `listEnvironments` | `azd env list --output json` | Suggest environment names in current azd project |
| `listEnvironmentVariables` | `azd env get-values --output json` | Suggest environment variable keys in current environment |
| `listTemplates` | `azd template list --output json` | Suggest templates |
| `listTemplateTags` | `azd template list --output json` | Suggest template tags |
| `listTemplatesFiltered` | `azd template list --filter <tag> --output json` | Suggest templates filtered by `--filter` flag |
| `listExtensions` | `azd ext list --output json` | Suggest available extension IDs |
| `listInstalledExtensions` | `azd ext list --installed --output json` | Suggest installed extension IDs |

**Basic Generator Structure:**
```typescript
{
  script: ['azd', 'command', '--output', 'json'],
  postProcess: (out) => {
    const items = JSON.parse(out);
    return items.map(item => ({ name: item.name, description: item.description }));
  },
  cache: { strategy: 'stale-while-revalidate' }
}
```

**Advanced Generator: `listTemplatesFiltered`**

This generator uses `custom` field instead of `postProcess`, which offers more flexibility for complex logic like:
- Inspects command line tokens to find the `--filter` flag value
- Dynamically builds the `azd template list` command that is executed with appropriate filters

## Advanced customization

Fig offers more advanced ways to enhance the spec, such as:

- **[Custom icons](https://fig.io/docs/reference/suggestion#icon)**: VS Code provides its own icon set (e.g. see [GitHub CLI spec](https://github.com/microsoft/vscode/blob/main/extensions/terminal-suggest/src/completions/gh.ts))
- **[File path templates](https://fig.io/docs/reference/arg#template)**: Built-in file/folder completion for path arguments
- **[Priority & sorting](https://fig.io/docs/reference/suggestion#priority)**: Rank suggestions with custom priorities
- **[Caching generator results](https://fig.io/docs/reference/generator#cache)**: Optimize performance with `stale-while-revalidate` or `max-age`

See [Fig documentation](https://fig.io/docs) and [other VS Code Fig specs](https://github.com/microsoft/vscode/tree/main/extensions/terminal-suggest/src/completions) for reference.

## References

- **Fig Documentation**: [https://fig.io/docs](https://fig.io/docs)
- **VS Code**:
  - [Fig spec type definition](https://github.com/microsoft/vscode/blob/main/extensions/terminal-suggest/src/completions/index.d.ts)
  - [GitHub CLI spec](https://github.com/microsoft/vscode/blob/main/extensions/terminal-suggest/src/completions/gh.ts)
  - [Other tool Fig specs](https://github.com/microsoft/vscode/tree/main/extensions/terminal-suggest/src/completions)
