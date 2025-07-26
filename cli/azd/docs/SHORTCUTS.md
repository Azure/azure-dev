# Command Shortcuts Feature

Azure Developer CLI (azd) supports command shortcuts to make common operations faster and more convenient.

**Note:** Shortcuts are disabled by default. To enable them, run `azd config set command.shortcuts on`.

## Quick Start

To start using shortcuts:

1. Enable the feature:
   ```bash
   azd config set command.shortcuts on
   ```

2. Try some shortcuts:
   ```bash
   azd b           # azd build
   azd u           # azd up
   azd pi c        # azd pipeline config
   ```

## How It Works

You can use shortened versions of any command or subcommand as long as the shortcut uniquely identifies the intended command.

## Examples

### Basic Shortcuts
```bash
# These shortcuts work:
azd b           # -> azd build
azd u           # -> azd up  
azd m           # -> azd monitor
azd ve          # -> azd version (minimum prefix to avoid conflict with vs-server)
```

### Two-level shortcuts for commands with conflicts
```bash
# Commands that need longer prefixes to avoid ambiguity:
azd pa          # -> azd package (vs pipeline, provision)
azd pi          # -> azd pipeline (vs package, provision)  
azd pr          # -> azd provision (vs package, pipeline)
azd au          # -> azd auth (vs add)
azd ad          # -> azd add (vs auth)
azd do          # -> azd down (vs deploy)
azd de          # -> azd deploy (vs down)
```

### Subcommand Shortcuts
```bash
# Multiple levels work too:
azd au logi     # -> azd auth login
azd au logo     # -> azd auth logout
azd inf c       # -> azd infra create
azd inf d       # -> azd infra delete
azd pi c        # -> azd pipeline config
```

## Ambiguity Handling

When a shortcut could match multiple commands, azd will show you the available options:

```bash
$ azd p
Error: ambiguous command "azd p"
Could match:
  - package (use "azd pa" or longer)
  - pipeline (use "azd pi" or longer)
  - provision (use "azd pr" or longer)
```

```bash
$ azd a
Error: ambiguous command "azd a"
Could match:
  - add (use "azd ad" or longer)
  - auth (use "azd au" or longer)
```

## Minimum Required Prefixes

| Command | Minimum Prefix | Reason |
|---------|----------------|---------|
| add | ad | Distinguish from auth |
| auth | au | Distinguish from add |
| build | b | Unique |
| deploy | de | Distinguish from down |
| down | do | Distinguish from deploy |
| monitor | m | Unique |
| package | pa | Distinguish from pipeline, provision |
| pipeline | pi | Distinguish from package, provision |
| provision | pr | Distinguish from package, pipeline |
| up | u | Unique |
| version | ve | Distinguish from vs-server |

### Subcommand Prefixes

For auth subcommands:
- `auth login` -> `au logi` (distinguish from logout)
- `auth logout` -> `au logo` (distinguish from login)

For infra subcommands:
- `infra create` -> `inf c` (unique within infra)
- `infra delete` -> `inf d` (unique within infra)
- `infra generate` -> `inf g` (unique within infra)

## Configuration

### Enabling Shortcuts

Shortcuts are disabled by default. To enable them, use the following command:

```bash
azd config set command.shortcuts on
```

To disable shortcuts:

```bash
azd config set command.shortcuts off
```

You can check if shortcuts are enabled by viewing your configuration:

```bash
azd config show
```

### Skip Commands

Shortcuts are automatically disabled for:
- Help commands: `help`, `--help`, `-h`
- Version commands: `version`, `--version`, `-v`
- Empty command line

## Implementation Details

The shortcut system:

1. **Prefix Matching**: Finds all commands starting with the given prefix
2. **Disambiguation**: If multiple matches, calculates minimum required prefix
3. **Error Guidance**: Provides helpful suggestions when ambiguous
4. **Backwards Compatibility**: All original full commands continue to work
5. **Performance**: Lightweight prefix matching with minimal overhead

## Examples in Practice

```bash
# Quick deployment workflow
azd ini                    # azd init
azd u                      # azd up

# Authentication
azd au logi                # azd auth login
azd au logo                # azd auth logout  

# Infrastructure management
azd inf c                  # azd infra create
azd inf d                  # azd infra delete

# Build and deploy
azd b                      # azd build
azd de                     # azd deploy

# Pipeline setup
azd pi c                   # azd pipeline config

# Monitoring
azd m                      # azd monitor
```

## Error Handling

If you make a mistake:

```bash
# Too short/ambiguous
$ azd p
Error: ambiguous command "azd p"
Could match:
  - package (use "azd pa" or longer)
  - pipeline (use "azd pi" or longer)
  - provision (use "azd pr" or longer)

# Non-existent command (falls back to normal Cobra behavior)
$ azd xyz
Error: unknown command "xyz" for "azd"
```

## Notes

- Shortcuts work with all flags and arguments
- Exact matches always take priority over partial matches
- Original full commands continue to work as before
- Shortcut suggestions are provided in error messages
- The feature can be completely disabled if needed

This feature significantly speeds up common azd workflows while maintaining full backwards compatibility.
