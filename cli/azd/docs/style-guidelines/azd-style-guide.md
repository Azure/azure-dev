# Azure Developer CLI (`azd`) Style Guide

## Overview

This style guide establishes standards for code, user experience, testing, and documentation across the Azure Developer CLI project. Following these guidelines ensures consistency, maintainability, and a high-quality user experience.

> **Scope**: This guide covers **core azd flows** only. Separate guidelines for agentic flows and extension-specific UX will be provided in dedicated files in the future.

## Code Style Guidelines

### Go Conventions

#### Pre-commit Checks

Run these commands from `cli/azd/` and ensure all files include the copyright header:

```bash
gofmt -s -w .
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
```

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
```

#### Naming

- **Packages**: Short, lowercase, single-word (e.g., `auth`, `project`).
- **Interfaces**: Descriptive, typically ending in `-er` (e.g., `Runner`).
- **Constructors**: `New` prefix, injecting dependencies (e.g., `NewProjectService`).
- **Fields**: `PascalCase` for public, `camelCase` for private.

#### Dependency Injection (IoC)

**ALWAYS** use the IoC container. **NEVER** use direct instantiation for major components.

```go
// 1. Register with container
ioc.RegisterSingleton(container, func() *MyService {
    return &MyService{
        dependency: ioc.Get[*SomeDependency](container),
    }
})

// 2. Definition & Constructor
type myService struct {
    dependency *SomeDependency
}

func NewMyService(dep *SomeDependency) *myService {
    return &myService{dependency: dep}
}
```

### Error Handling & Context

- **Errors**: Wrap with `fmt.Errorf` ("%w") to add actionable context and trace IDs.
- **Context**: **ALWAYS** propagate `context.Context` for cancellation and telemetry.

```go
func (s *Service) Run(ctx context.Context) error {
    if err := s.doWork(ctx); err != nil {
        return fmt.Errorf("failed to provision resources: %w", err)
    }
    return nil
}
```

## User Experience Patterns

### Progress Reports

Progress reports provide real-time feedback during a single command's execution, showing the status of individual steps as they happen. Use progress reports to:

- Show status of long-running commands
- Display individual service provisioning/deployment states
- Help users troubleshoot by showing exactly which step failed

#### Progress Report States

Items in a progress report list can be in one of five states:

1. **Loading**: `|===    | [Verb] Message goes here`
   - Indicates operation in progress; animated bar-fill spinner in gray (`WithGrayFormat`)

2. **Done**: `(✓) Done: [Verb] Message goes here`
   - Green checkmark via `WithSuccessFormat`; use past tense verb

3. **Failed**: `(x) Failed: [Verb] Message goes here`
   - Red `x` via `WithErrorFormat`; include specific error message below

4. **Warning**: `(!) Warning: [Verb] Message goes here`
   - Yellow exclamation via `WithWarningFormat`; non-blocking, command continues

5. **Skipped**: `(-) Skipped: [Verb] Message goes here`
   - Gray dash via `WithGrayFormat`; intentionally skipped (not a failure)

#### Quick Reference

**Progress report state → prefix → color function:**

| State    | Prefix         | Color Function      | Notes                     |
| -------- | -------------- | ------------------- | ------------------------- |
| Loading  | `\|===    \|`  | `WithGrayFormat`    | Animated bar-fill spinner |
| Done     | `(✓) Done:`    | `WithSuccessFormat` | Past tense verb           |
| Failed   | `(x) Failed:`  | `WithErrorFormat`   | Follow with error detail  |
| Warning  | `(!) Warning:` | `WithWarningFormat` | Non-blocking issue        |
| Skipped  | `(-) Skipped:` | `WithGrayFormat`    | Expected, not a failure   |

**Log type → prefix → color function:**

| Log Type   | Prefix            | Color Function      | When to use                                |
| ---------- | ----------------- | ------------------- | ------------------------------------------ |
| Success    | `SUCCESS:`        | `WithSuccessFormat` | Command completed its primary goal         |
| Error      | `ERROR:`          | `WithErrorFormat`   | Command failed; top-level failure message  |
| Warning    | `WARNING:`        | `WithWarningFormat` | Non-fatal issue the user should know about |

#### Progress Report Guidelines

- **Indentation**: Progress items are always indented under the main command
- **Verb consistency**: Use present progressive for loading, past tense for completed
- **Contextual information**: Include resource names, identifiers when relevant

#### Example

**Failure scenario** (shows how multiple states compose with error detail):

```
Provisioning Azure resources (azd provision)

  (✓) Done: Creating App Service Plan: plan-r2w2adrz3rvwxu
  (✓) Done: Creating Log Analytics workspace: log-r2w2adrz3rvwxu
  (x) Failed: Creating Cosmos DB: cosmos-r2w2adrz3rvwxu
  The '{US} West US 2 (westus)' region is currently experiencing high demand
  and cannot fulfill your request. Failed to create Cosmos DB account.

ERROR: Unable to complete provisioning of Azure resources, 'azd up' failed
```

> Colors: Title → `WithBold`. `(✓) Done:` → `WithSuccessFormat`. `(x) Failed:` and error detail → `WithErrorFormat`. `ERROR:` line → `WithErrorFormat`.

### Success / Error / Warning Logs

These logs represent the final outcome of a command, displayed after execution completes. Standard logs (Success, Error, Warning) use an **all-caps prefix** followed by a colon to separate the log type from the message.

All logs should:

- Be **succinct**
- Be **human legible**
- **Provide a path forward** when possible

There are some specific edge cases where the log prefix is not required.

#### When to Use Each Log Type

- **SUCCESS**: The command achieved its primary goal. Use after deployments, provisioning, or any command that completes a user-requested action.
- **ERROR**: The command failed and cannot continue. Always include the reason and, when possible, a suggested fix. Only use for top-level command failures, not internal retries.
- **WARNING**: The command succeeded (or will proceed), but something unexpected happened that the user should know about. Examples: deprecated flags, region capacity concerns, overwriting existing resources.

#### Log Format

```
PREFIX: Message goes here.
```

#### Log Types

**Success logs** — Use `WithSuccessFormat` (green/bright green).

```
SUCCESS: Message goes here.
```

**Error logs** — Use `WithErrorFormat` (red/bright red). Follow a `[Reason] [Outcome]` structure when possible.

```
ERROR: Message goes here.
```

**Warning logs** — Use `WithWarningFormat` (yellow/bright yellow).

```
WARNING: Message goes here.
```

#### In-Context Example

**Error with suggested command** (shows multi-line composition with inline highlights):

```
ERROR: 'todo-mongojs' is misspelled or missing a recognized flag.
Run azd up --help to see all available flags.
```

> Colors: `ERROR:` line → `WithErrorFormat`. `azd up --help` → `WithHighLightFormat`.

### User Inputs

Certain azd commands require the user to input text, select yes/no, or select an item from a list. Examples include:

- Inputting an environment name (text input)
- Initializing a new Git repository (y/n)
- Selecting a location to deploy to (list select)

#### General Guidelines

All input requests are in bold and begin with a blue `?`. This helps ensure that they stand out to users as different from other plain text logs and CLI outputs.

#### Text Input

Text input captures a single line of input from the user.

**Initial state:**

The prompt is displayed with a `[Type ? for hint]` helper in **hi-blue bold text** via `WithHighLightFormat` (Bright Blue, ANSI 94), followed by ghost-text (secondary text color) as placeholder content.

```
? This captures a single line of input: [Type ? for hint]
```

> Colors: `?` → `WithHighLightFormat` + `WithBold`. `[Type ? for hint]` → `WithHighLightFormat` + `WithBold`.

**Hint feature:**

If the user types `?`, a hint line appears below the prompt to provide additional guidance.

```
? This captures a single line of input: [Type ? for hint]
Hint: This is a help message
```

> Colors: `?` and `[Type ? for hint]` → `WithHighLightFormat` + `WithBold`. Hint line → `WithHintFormat` + `WithBold`.

#### Yes or No Input

- Yes/no inputs include a `(Y/n)` delineator at the end of the input request (before the colon).
- Users can either input `y`/`n` or hit the return key which will select the capitalized choice in the `(Y/n)` delineator.

```
? Do you want to initialize a new Git repository in this directory? (Y/n):
```

> Colors: `?` → `WithHighLightFormat` + `WithBold`.

#### List Select

The list select pattern presents a list of options for the user to choose from.

**Initial state:**

The prompt is displayed with a `Filter:` line showing ghost-text ("Type to filter list") and a footer hint in **hi-blue text** via `WithHighLightFormat` (Bright Blue, ANSI 94). The active selection is indicated by `>` and displayed in bold blue text.

```
? Select a single option:  [Use arrows to move, type to filter]

  > Option 1
    Option 2
    Option 3
    Option 4
    Option 5
    Option 6
    ...
```

> Colors: `?` → `WithHighLightFormat` + `WithBold`. `[Use arrows to move, type to filter]` → `WithHighLightFormat` + `WithBold`. `> Option 1` (selected item) → `WithHighLightFormat` + `WithBold`.

**Display rules:**

- The list should display no more than 7 items at a time.
- When a list contains more than 7 items, ellipses (`...`) should be used to help users understand there are more items available up or down the list.
- If possible, the most commonly selected item (or the item we want to guide the user into selecting) should be highlighted by default.
- The active selection in the list should be bold and in blue text, prefixed with `>`.

**Hint:**

- The `[Type ? for hint]` pattern follows the same behavior as Text Input — **hi-blue bold** via `WithHighLightFormat` (Bright Blue, ANSI 94).

**After submitting:**

Once the user makes their selection:

- The input changes from primary text to hi-blue text
- The `[Type ? for hint]` helper disappears
- If the hint line was visible, it also disappears
- The list collapses and the selected value is printed in blue text next to the prompt

```
? Select an Azure location to use: (US) East US 2 (eastus2)
```

> Colors: `?` → `WithHighLightFormat` + `WithBold`. `(US) East US 2 (eastus2)` (selected value) → `WithHighLightFormat`.

### CLI Color Standards

The CLI uses consistent color formatting through helper functions defined in [`cli/azd/pkg/output/colors.go`](../../pkg/output/colors.go). **Always use these helper functions** instead of writing raw ANSI escape codes.

**Important**: Colors will appear differently depending on which terminal and theme (dark/light) the customer prefers. Always test output in both dark and light terminal themes.

#### Standard Color Helper Functions

Use these functions for consistent color formatting across the CLI:

| Purpose | Function | Usage Example |
| --- | --- | --- |
| **Hyperlinks** | `WithLinkFormat(text string)` | URLs, portal links |
| **Commands & parameters** | `WithHighLightFormat(text string)` | Command names, parameters, system events |
| **Success** | `WithSuccessFormat(text string)` | Success messages, completed operations |
| **Warning** | `WithWarningFormat(text string)` | Warning messages, non-critical issues |
| **Error** | `WithErrorFormat(text string)` | Error messages, failures |
| **Gray text** | `WithGrayFormat(text string)` | Secondary information, muted text |
| **Hint text** | `WithHintFormat(text string)` | Helpful suggestions, tips |
| **Bold text** | `WithBold(text string)` | Emphasis, headers |
| **Underline** | `WithUnderline(text string)` | Emphasis (use sparingly) |

#### Examples

```go
// Success message
fmt.Println(output.WithSuccessFormat("(✓) Done:") + " Creating resource")

// Warning message
fmt.Println(output.WithWarningFormat("(!) Warning:") + " Configuration may need update")

// Error message
fmt.Println(output.WithErrorFormat("(x) Failed:") + " Unable to connect")

// Hyperlink
fmt.Printf("View in portal: %s\n", output.WithLinkFormat(url))

// Command highlight
fmt.Printf("Run %s to deploy\n", output.WithHighLightFormat("azd deploy"))
```

#### Implementation Guidelines

- **Consistency**: Always use the helper functions for the same purpose across all commands
- **Accessibility**: Never rely solely on color to convey information (use icons/symbols too)
- **Terminal compatibility**: Colors will render differently across terminals - test in multiple environments
- **Theme support**: Test in both light and dark terminal themes
- **Fallback**: Ensure output remains readable if colors are disabled

#### Common Mistakes

| ❌ Don't | ✅ Do | Why |
| --- | --- | --- |
| `Error: something went wrong` | `ERROR: something went wrong.` | All-caps prefix is required |
| `Done: Created resource` | `(✓) Done: Created resource` | Include icon prefix for progress states |
| `color.Red("ERROR")` | `output.WithErrorFormat("ERROR:")` | Always use helper functions |
| Print bare success text | `SUCCESS: Your app has been deployed!` | Always include the `SUCCESS:` prefix |
| `(✗) Failed:` (Unicode ballot X) | `(x) Failed:` (lowercase letter x) | Match the codebase symbol |

<details>
<summary>📚 Learn more about ANSI color codes (optional reference)</summary>

The CLI uses industry-standard [ANSI defined colors](https://en.wikipedia.org/wiki/ANSI_escape_code#Colors) to ensure accessibility and compatibility across all terminals. This allows users to customize color palettes through their IDE or terminal preferences.

#### Color Naming Convention

Each color set has a pair: standard and bright variants:

- **ANSI naming**: "[color]" (FG 30-37) and "Bright [color]" (FG 90-97)
- **PowerShell naming**: "Dark [color]" and "[color]"

Note: There is a discrepancy in the naming convention between ANSI Color coding (which uses "Bright [color]" and "[color]") and PowerShell (which uses "[color]" and "Dark [color]"). Developers working with ANSI Color definitions should use the darker version of the two.

#### ANSI Color Code Reference

**Standard 3/4-bit Colors** (Theme-adaptive - Recommended):

| FG | BG | Name | Foreground Code | Background Code | Standard RGB |
| --- | --- | --- | --- | --- | --- |
| 30 | 40 | Black | `\033[30m` | `\033[40m` | 0, 0, 0 |
| 31 | 41 | Red | `\033[31m` | `\033[41m` | 170, 0, 0 |
| 32 | 42 | Green | `\033[32m` | `\033[42m` | 0, 170, 0 |
| 33 | 43 | Yellow | `\033[33m` | `\033[43m` | 170, 85, 0 |
| 34 | 44 | Blue | `\033[34m` | `\033[44m` | 0, 0, 170 |
| 35 | 45 | Magenta | `\033[35m` | `\033[45m` | 170, 0, 170 |
| 36 | 46 | Cyan | `\033[36m` | `\033[46m` | 0, 170, 170 |
| 37 | 47 | White | `\033[37m` | `\033[47m` | 170, 170, 170 |
| 90 | 100 | Bright Black (Gray) | `\033[90m` | `\033[100m` | 85, 85, 85 |
| 91 | 101 | Bright Red | `\033[91m` | `\033[101m` | 255, 85, 85 |
| 92 | 102 | Bright Green | `\033[92m` | `\033[102m` | 85, 255, 85 |
| 93 | 103 | Bright Yellow | `\033[93m` | `\033[103m` | 255, 255, 85 |
| 94 | 104 | Bright Blue | `\033[94m` | `\033[104m` | 85, 85, 255 |
| 95 | 105 | Bright Magenta | `\033[95m` | `\033[105m` | 255, 85, 255 |
| 96 | 106 | Bright Cyan | `\033[96m` | `\033[106m` | 85, 255, 255 |
| 97 | 107 | Bright White | `\033[97m` | `\033[107m` | 255, 255, 255 |

**24-bit RGB Colors** (Exact colors - Use sparingly):
- Foreground: `\033[38;2;R;G;Bm` where R, G, B are 0-255
- Background: `\033[48;2;R;G;Bm` where R, G, B are 0-255
- Example: `\033[38;2;255;0;0m` for exact red foreground

**Note**: `\033` is the escape character in octal notation (can also be written as `\x1b` in hex or `\e` in some languages). Standard RGB values shown are from the VGA specification, but actual rendering varies by terminal and user theme preferences.

**Recommendation**: Use standard 3/4-bit colors (30-37, 90-97) as they adapt to the user's terminal theme preferences (dark/light mode). Only use 24-bit RGB colors when exact color matching is required. For complete ANSI escape code documentation, see [ANSI escape code - Colors](https://en.wikipedia.org/wiki/ANSI_escape_code#Colors).

</details>

### Loading Animation (Progress Spinner)

`azd` uses a **bar-fill spinner** to indicate ongoing operations. The animation displays a pair of `|` border characters with `=` fill characters that bounce back and forth, alongside a status message describing the current operation.

#### Animation Behavior

The spinner **animates in place** on a single line by overwriting itself. The `=` fill characters grow and bounce back and forth between the `|` borders, creating a fluid loading effect:

```
|=====  | Creating App Service: my-app-r2w2adrz3rvwxu
```

The full animation cycle frames are:

```
|       |  →  |=      |  →  |==     |  →  |===    |  →  |====   |  →  |=====  |  →  |====== |  →  |=======|
|=======|  →  | ======|  →  |  =====|  →  |   ====|  →  |    ===|  →  |     ==|  →  |      =|  →  |       |
```

These frames repeat continuously on the **same line** until the operation completes. The spinner bar is displayed in **gray text** via `WithGrayFormat` to keep focus on the status message.

#### Implementation

Use `console.ShowSpinner()` and `console.StopSpinner()` from [`pkg/input/console.go`](../../pkg/input/console.go) to display the bar-fill spinner. These are the standard functions used across all azd commands.

```go
// Start or update spinner
console.ShowSpinner(ctx, "Creating App Service: my-app-r2w2adrz3rvwxu", input.Step)
```

## Testing Standards

### Test Structure

Use table-driven tests consistently:

```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"ValidInput", "test", "result", false},
        {"InvalidInput", "", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := MyFunction(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            require.Equal(t, tt.expected, result)
        })
    }
}
```

### Snapshot Testing

- **CLI help output**: Test via snapshot files in `cli/azd/cmd/testdata/*.snap`
- **Updating snapshots**: Set `UPDATE_SNAPSHOTS=true` before running `go test` when output changes
- **Review changes**: Always review snapshot diffs to ensure they're intentional

### Mock Usage

- Use testify/mock framework for mocking dependencies
- Place mocks in `cli/azd/test/mocks/` directory
- Mock external dependencies (Azure SDK, file system, HTTP clients)

## Architecture Patterns

### Action-Based Commands

Commands implement the `Action` interface rather than traditional Cobra handlers:

```go
type myAction struct {
    dependency *SomeService
}

func newMyAction(dep *SomeService) actions.Action {
    return &myAction{dependency: dep}
}

func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    // Implementation
    return &actions.ActionResult{
        Message: &actions.ResultMessage{
            Header: "Success",
        },
    }, nil
}
```

### Middleware Pattern

For cross-cutting concerns, implement middleware:

```go
type MyMiddleware struct {
    options *Options
}

func (m *MyMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
    // Pre-processing
    result, err := next(ctx)
    // Post-processing
    return result, err
}
```

### Telemetry Integration

- Add telemetry spans for all major operations
- Use OpenTelemetry conventions
- Include relevant attributes (command, duration, result)
- Respect `AZURE_DEV_COLLECT_TELEMETRY=no` opt-out

## Documentation Standards

### Code Comments

- **Public APIs**: Document all public functions, types, and methods
- **Complex logic**: Add inline comments explaining non-obvious code
- **TODOs**: Use `// TODO:` with context and owner when applicable

### Help Text

- **Consistency**: Match terminology across all commands
- **Examples**: Include at least one example per command
- **Flags**: Document all flags with clear descriptions
- **See also**: Cross-reference related commands

### Markdown Documentation

- Use proper formatting with headers, code blocks, and lists
- Include examples with both command and expected output
- Keep language simple and action-oriented
- Link to related documentation when appropriate

## Command Design Patterns

See [guiding-principles.md](guiding-principles.md) for detailed command structure design principles.

### Key Principles

- Use verb-first structure (`azd add <resource>`, not `azd <resource> add`)
- Build on existing command categories (View, Edit, Run)
- Maintain consistent parameter patterns
- Follow progressive disclosure (simple by default, advanced when needed)

## File Organization

### Package Structure

```
cli/azd/
├── cmd/           # Command definitions
├── pkg/           # Core packages
│   ├── auth/      # Authentication
│   ├── project/   # Project management
│   ├── infra/     # Infrastructure providers
│   └── ...
├── internal/      # Internal packages
│   ├── telemetry/ # Telemetry system
│   └── ...
└── test/          # Test utilities and mocks
```

### File Naming

- `*_test.go` for test files
- `mock_*.go` for mocks
- Keep related functionality in same package
- Split large files when they exceed ~500 lines

## Azure Integration Guidelines

### Authentication

- Support multiple auth methods (device code, service principal, managed identity)
- Handle token refresh gracefully
- Provide clear error messages for auth failures

### SDK Usage

- Use Azure SDK for Go consistently
- Handle rate limiting and retries
- Log Azure API calls for debugging
- Include correlation IDs in requests

## Performance Considerations

- Minimize Azure API calls
- Cache authentication tokens appropriately
- Use parallel operations where safe
- Provide progress feedback for operations >2 seconds

## Security Guidelines

- Never log sensitive information (tokens, passwords, keys)
- Respect Azure RBAC and subscription boundaries
- Validate user input before processing
- Use secure defaults for all operations

---

*For extension development guidelines, see [extensions-style-guide.md](../extensions/extensions-style-guide.md).*
*For core design principles, see [guiding-principles.md](guiding-principles.md).*
