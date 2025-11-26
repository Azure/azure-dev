# Adding New azd Commands - Comprehensive Guide

This document provides detailed instructions for adding new commands or command groups to the Azure Developer CLI (azd). It's designed to enable both human developers and LLMs to systematically create new commands that integrate seamlessly with the existing azd architecture.

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [File Structure and Naming Conventions](#file-structure-and-naming-conventions)
3. [Adding a New Top-Level Command Group](#adding-a-new-top-level-command-group)
4. [Adding Commands to Existing Groups](#adding-commands-to-existing-groups)
5. [Action Implementation Patterns](#action-implementation-patterns)
6. [Flags and Input Handling](#flags-and-input-handling)
7. [Output Formatting](#output-formatting)
8. [Error Handling](#error-handling)
9. [Integration with IoC Container](#integration-with-ioc-container)
10. [Complete Examples](#complete-examples)

## Architecture Overview

azd uses a layered architecture built on top of the [Cobra CLI library](https://github.com/spf13/cobra):

```
ActionDescriptor Tree → CobraBuilder → Cobra Commands → CLI
```

**Key Components:**
- **ActionDescriptor**: Higher-order component that describes commands, flags, middleware, and relationships
- **Action Interface**: Contains the actual command logic (`Run(ctx context.Context) (*ActionResult, error)`)
- **Flags**: Input parameters and options for commands
- **IoC Container**: Dependency injection system for resolving services
- **Output Formatters**: Handle JSON, Table, and None output formats

## File Structure and Naming Conventions

### File Organization

Commands should be organized following these patterns:

```
cmd/
├── root.go                    # Root command registration
├── <command_group>.go         # Top-level command groups (e.g., env.go, extension.go)
├── <simple_command>.go        # Single commands (e.g., version.go, monitor.go)
└── actions/
    ├── action.go              # Action interface definitions
    └── action_descriptor.go   # ActionDescriptor framework
```

### Naming Conventions

| Component | Pattern | Example |
|-----------|---------|---------|
| **File Names** | `<command_name>.go` | `extension.go`, `monitor.go` |
| **Command Groups** | `<group>Actions(root *ActionDescriptor)` | `extensionActions()`, `envActions()` |
| **Action Types** | `<command><subcommand>Action` | `extensionListAction`, `envNewAction` |
| **Flag Types** | `<command><subcommand>Flags` | `extensionListFlags`, `envNewFlags` |
| **Constructors** | `new<TypeName>` | `newExtensionListAction`, `newExtensionListFlags` |
| **Cobra Commands** | `new<Command>Cmd()` (when needed) | `newMonitorCmd()`, `newEnvListCmd()` |

## Adding a New Top-Level Command Group

### Step 1: Create the Command File

Create a new file: `cmd/<command_group>.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Register <command-group> commands
func <commandGroup>Actions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("<command-group>", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:     "<command-group>",
			Aliases: []string{"<alias>"}, // Optional
			Short:   "Manage <command-group> resources.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupAzure, // Or appropriate group
		},
	})

	// Add subcommands here
	// Example: azd <command-group> list
	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list",
			Short: "List <command-group> items.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: new<CommandGroup>ListAction,
		FlagsResolver:  new<CommandGroup>ListFlags,
	})

	// Example: azd <command-group> create
	group.Add("create", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "create <name>",
			Short: "Create a new <command-group> item.",
			Args:  cobra.ExactArgs(1),
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: new<CommandGroup>CreateAction,
		FlagsResolver:  new<CommandGroup>CreateFlags,
	})

	return group
}

// Flags for list command
type <commandGroup>ListFlags struct {
	global *internal.GlobalCommandOptions
	filter string
	all    bool
	internal.EnvFlag
}

func new<CommandGroup>ListFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *<commandGroup>ListFlags {
	flags := &<commandGroup>ListFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *<commandGroup>ListFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(&f.filter, "filter", "", "Filter results by name pattern")
	local.BoolVar(&f.all, "all", false, "Show all items including hidden ones")
	f.EnvFlag.Bind(local, global)
	f.global = global
}

// Action for list command
type <commandGroup>ListAction struct {
	flags     *<commandGroup>ListFlags
	formatter output.Formatter
	console   input.Console
	writer    io.Writer
	// Add your service dependencies here
	// exampleService *services.ExampleService
}

func new<CommandGroup>ListAction(
	flags *<commandGroup>ListFlags,
	formatter output.Formatter,
	console input.Console,
	writer io.Writer,
	// Add your service dependencies here
	// exampleService *services.ExampleService,
) actions.Action {
	return &<commandGroup>ListAction{
		flags:     flags,
		formatter: formatter,
		console:   console,
		writer:    writer,
		// exampleService: exampleService,
	}
}

type <commandGroup>ListItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Created     string `json:"created"`
}

func (a *<commandGroup>ListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "List <command-group> items (azd <command-group> list)",
		TitleNote: "Retrieving available <command-group> items",
	})

	// TODO: Implement actual list logic
	// items, err := a.exampleService.List(ctx, a.flags.filter)
	// if err != nil {
	//     return nil, fmt.Errorf("failed to list <command-group> items: %w", err)
	// }

	// Example placeholder data
	items := []<commandGroup>ListItem{
		{
			Name:        "example-item",
			Description: "An example item",
			Status:      "active",
			Created:     "2024-01-01",
		},
	}

	if len(items) == 0 {
		a.console.Message(ctx, output.WithWarningFormat("No <command-group> items found."))
		return nil, nil
	}

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Description",
				ValueTemplate: "{{.Description}}",
			},
			{
				Heading:       "Status",
				ValueTemplate: "{{.Status}}",
			},
			{
				Heading:       "Created",
				ValueTemplate: "{{.Created}}",
			},
		}

		return nil, a.formatter.Format(items, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	}

	return nil, a.formatter.Format(items, a.writer, nil)
}

// Flags for create command
type <commandGroup>CreateFlags struct {
	global      *internal.GlobalCommandOptions
	description string
	force       bool
	internal.EnvFlag
}

func new<CommandGroup>CreateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *<commandGroup>CreateFlags {
	flags := &<commandGroup>CreateFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *<commandGroup>CreateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(&f.description, "description", "d", "", "Description for the new item")
	local.BoolVarP(&f.force, "force", "f", false, "Force creation even if item exists")
	f.EnvFlag.Bind(local, global)
	f.global = global
}

// Action for create command
type <commandGroup>CreateAction struct {
	args    []string
	flags   *<commandGroup>CreateFlags
	console input.Console
	// Add your service dependencies here
	// exampleService *services.ExampleService
}

func new<CommandGroup>CreateAction(
	args []string,
	flags *<commandGroup>CreateFlags,
	console input.Console,
	// Add your service dependencies here
	// exampleService *services.ExampleService,
) actions.Action {
	return &<commandGroup>CreateAction{
		args:    args,
		flags:   flags,
		console: console,
		// exampleService: exampleService,
	}
}

func (a *<commandGroup>CreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	itemName := a.args[0]

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Create <command-group> item (azd <command-group> create)",
		TitleNote: fmt.Sprintf("Creating new <command-group> item '%s'", itemName),
	})

	stepMessage := fmt.Sprintf("Creating %s", output.WithHighLightFormat(itemName))
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	// TODO: Implement actual creation logic
	// err := a.exampleService.Create(ctx, itemName, a.flags.description, a.flags.force)
	// if err != nil {
	//     a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
	//     return nil, fmt.Errorf("failed to create <command-group> item: %w", err)
	// }

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Successfully created <command-group> item '%s'", itemName),
			FollowUp: "Use 'azd <command-group> list' to see all items.",
		},
	}, nil
}
```

### Step 2: Register the Command Group

Add the command group registration to `cmd/root.go`:

```go
// In the NewRootCmd function, add your command group registration
func NewRootCmd(...) *cobra.Command {
	// ... existing code ...

	configActions(root, opts)
	envActions(root)
	infraActions(root)
	pipelineActions(root)
	telemetryActions(root)
	templatesActions(root)
	authActions(root)
	hooksActions(root)
	<commandGroup>Actions(root)  // Add this line

	// ... rest of function ...
}
```

## Adding Commands to Existing Groups

To add a new command to an existing command group (e.g., adding to `azd extension`):

### Step 1: Add the Command to the Group

In the existing command file (e.g., `cmd/extension.go`), add to the group registration function:

```go
func extensionActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("extension", &actions.ActionDescriptorOptions{
		// ... existing options ...
	})

	// ... existing commands ...

	// Add your new command
	group.Add("validate", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "validate <extension-id>",
			Short: "Validate an extension configuration.",
			Args:  cobra.ExactArgs(1),
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newExtensionValidateAction,
		FlagsResolver:  newExtensionValidateFlags,
	})

	return group
}
```

### Step 2: Implement Flags and Action

Add the flags and action implementation to the same file:

```go
// Flags for the new command
type extensionValidateFlags struct {
	strict bool
	output string
	global *internal.GlobalCommandOptions
}

func newExtensionValidateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *extensionValidateFlags {
	flags := &extensionValidateFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *extensionValidateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&f.strict, "strict", false, "Enable strict validation mode")
	local.StringVar(&f.output, "output-file", "", "Write validation results to file")
	f.global = global
}

// Action implementation
type extensionValidateAction struct {
	args             []string
	flags            *extensionValidateFlags
	console          input.Console
	extensionManager *extensions.Manager // Use existing service dependencies
}

func newExtensionValidateAction(
	args []string,
	flags *extensionValidateFlags,
	console input.Console,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionValidateAction{
		args:             args,
		flags:            flags,
		console:          console,
		extensionManager: extensionManager,
	}
}

func (a *extensionValidateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	extensionName := a.args[0]

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Validate extension (azd extension validate)",
		TitleNote: fmt.Sprintf("Validating extension '%s'", extensionName),
	})

	stepMessage := fmt.Sprintf("Validating %s", output.WithHighLightFormat(extensionName))
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	// TODO: Implement validation logic
	// validationResult, err := a.extensionManager.Validate(ctx, extensionName, a.flags.strict)
	// if err != nil {
	//     a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
	//     return nil, fmt.Errorf("validation failed: %w", err)
	// }

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Extension '%s' validation completed successfully", extensionName),
			FollowUp: "Extension is ready for use.",
		},
	}, nil
}
```

## Action Implementation Patterns

### Basic Action Structure

```go
type myCommandAction struct {
	// Dependencies
	console     input.Console
	flags       *myCommandFlags
	
	// Services (injected via IoC)
	someService *services.SomeService
	formatter   output.Formatter
	writer      io.Writer
}

func newMyCommandAction(
	console input.Console,
	flags *myCommandFlags,
	someService *services.SomeService,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &myCommandAction{
		console:     console,
		flags:       flags,
		someService: someService,
		formatter:   formatter,
		writer:      writer,
	}
}

func (a *myCommandAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// 1. Display command start message
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "My Command (azd mycommand)",
		TitleNote: "Performing operation",
	})

	// 2. Show progress for long operations
	stepMessage := "Processing request"
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	// 3. Perform the actual work
	result, err := a.someService.DoWork(ctx, a.flags.someFlag)
	if err != nil {
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return nil, fmt.Errorf("operation failed: %w", err)
	}

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	// 4. Format and display results
	if a.formatter.Kind() != output.NoneFormat {
		if err := a.formatter.Format(result, a.writer, nil); err != nil {
			return nil, fmt.Errorf("failed to format output: %w", err)
		}
	}

	// 5. Return success result
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Operation completed successfully",
			FollowUp: "Next steps: run 'azd mycommand list' to see results",
		},
	}, nil
}
```

### Action with Complex Output Formatting

```go
func (a *myListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	items, err := a.service.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve items: %w", err)
	}

	// Handle empty results
	if len(items) == 0 {
		a.console.Message(ctx, output.WithWarningFormat("No items found."))
		a.console.Message(ctx, fmt.Sprintf(
			"Create one with %s",
			output.WithHighLightFormat("azd mycommand create <name>"),
		))
		return nil, nil
	}

	// Format output based on format type
	switch a.formatter.Kind() {
	case output.TableFormat:
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Status",
				ValueTemplate: "{{.Status}}",
			},
			{
				Heading:       "Created",
				ValueTemplate: "{{.CreatedAt | date}}",
			},
		}
		
		return nil, a.formatter.Format(items, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	default:
		return nil, a.formatter.Format(items, a.writer, nil)
	}
}
```

## Flags and Input Handling

### Standard Flag Patterns

```go
type myCommandFlags struct {
	// Basic types
	stringFlag   string
	intFlag      int
	boolFlag     bool
	sliceFlag    []string
	
	// Common azd patterns
	subscription string
	location     string
	environment  string
	
	// Always include global options
	global *internal.GlobalCommandOptions
	
	// Include environment flag for env-aware commands
	internal.EnvFlag
}

func newMyCommandFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *myCommandFlags {
	flags := &myCommandFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *myCommandFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	// String flags
	local.StringVarP(&f.stringFlag, "name", "n", "", "Name of the resource")
	local.StringVar(&f.stringFlag, "long-flag", "default", "Description of flag")
	
	// Boolean flags
	local.BoolVar(&f.boolFlag, "force", false, "Force the operation")
	local.BoolVarP(&f.boolFlag, "verbose", "v", false, "Enable verbose output")
	
	// Integer flags
	local.IntVar(&f.intFlag, "timeout", 300, "Timeout in seconds")
	
	// String slice flags
	local.StringSliceVar(&f.sliceFlag, "tags", nil, "Tags to apply (can specify multiple)")
	
	// Common Azure flags
	local.StringVarP(&f.subscription, "subscription", "s", "", "Azure subscription ID")
	local.StringVarP(&f.location, "location", "l", "", "Azure location")
	
	// Bind environment flag for env-aware commands
	f.EnvFlag.Bind(local, global)
	
	// Always set global
	f.global = global
}
```

### Flag Validation

```go
func (a *myCommandAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Validate required flags
	if a.flags.stringFlag == "" {
		return nil, fmt.Errorf("--name flag is required")
	}
	
	// Validate flag combinations
	if a.flags.force && a.flags.interactive {
		return nil, fmt.Errorf("cannot use --force and --interactive together")
	}
	
	// Validate enum values
	validValues := []string{"dev", "test", "prod"}
	if !slices.Contains(validValues, a.flags.environment) {
		return nil, fmt.Errorf("invalid environment '%s', must be one of: %s", 
			a.flags.environment, strings.Join(validValues, ", "))
	}
	
	// Continue with command logic...
}
```

## Output Formatting

### Standard Output Formats

```go
// Define your output model
type MyItemOutput struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	Description string    `json:"description,omitempty"`
}

// Configure output formats in ActionDescriptorOptions
&actions.ActionDescriptorOptions{
	OutputFormats: []output.Format{
		output.JsonFormat,    // --output json
		output.TableFormat,   // --output table (default)
		output.NoneFormat,    // --output none
	},
	DefaultFormat: output.TableFormat,
	// ... other options
}

// Handle formatting in your action
func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	data := getMyData() // Your data retrieval logic
	
	switch a.formatter.Kind() {
	case output.TableFormat:
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Status",
				ValueTemplate: "{{.Status}}",
				Width:         10,
			},
			{
				Heading:       "Created",
				ValueTemplate: "{{.CreatedAt | date}}",
			},
		}
		
		return nil, a.formatter.Format(data, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	
	case output.NoneFormat:
		// Custom formatting for none output
		for _, item := range data {
			fmt.Fprintf(a.writer, "%s (%s)\n", item.Name, item.Status)
		}
		return nil, nil
	
	default: // JsonFormat and others
		return nil, a.formatter.Format(data, a.writer, nil)
	}
}
```

### Custom Display Methods

```go
type MyDetailedOutput struct {
	Name        string
	Description string
	Properties  map[string]string
}

// Implement custom display for complex output
func (o *MyDetailedOutput) Display(writer io.Writer) error {
	tabs := tabwriter.NewWriter(
		writer,
		0,
		output.TableTabSize,
		1,
		output.TablePadCharacter,
		output.TableFlags)
	
	text := [][]string{
		{"Name", ":", o.Name},
		{"Description", ":", o.Description},
		{"", "", ""},
		{"Properties", ":", ""},
	}
	
	for key, value := range o.Properties {
		text = append(text, []string{"  " + key, ":", value})
	}
	
	for _, line := range text {
		_, err := tabs.Write([]byte(strings.Join(line, "\t") + "\n"))
		if err != nil {
			return err
		}
	}
	
	return tabs.Flush()
}

// Use in action
func (a *myShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	data := getDetailedData()
	
	if a.formatter.Kind() == output.NoneFormat {
		return nil, data.Display(a.writer)
	}
	
	return nil, a.formatter.Format(data, a.writer, nil)
}
```

## Error Handling

### Standard Error Patterns

```go
func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Service/API errors
	result, err := a.service.DoSomething(ctx)
	if err != nil {
		// Wrap with context
		return nil, fmt.Errorf("failed to perform operation: %w", err)
	}
	
	// Validation errors
	if result == nil {
		return nil, fmt.Errorf("operation returned no results")
	}
	
	// Business logic errors
	if !result.IsValid {
		return nil, fmt.Errorf("operation completed but result is invalid: %s", result.ValidationMessage)
	}
	
	// Stop spinner on errors
	stepMessage := "Processing"
	a.console.ShowSpinner(ctx, stepMessage, input.Step)
	
	_, err = a.service.Process(ctx)
	if err != nil {
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return nil, fmt.Errorf("processing failed: %w", err)
	}
	
	a.console.StopSpinner(ctx, stepMessage, input.StepDone)
	
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Operation completed successfully",
		},
	}, nil
}
```

### Error Handling with User Guidance

```go
func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Check prerequisites
	if !a.checkPrerequisites(ctx) {
		return nil, fmt.Errorf("prerequisites not met. Run 'azd auth login' first")
	}
	
	// Handle specific error types
	err := a.service.Operate(ctx)
	if err != nil {
		var notFoundErr *services.NotFoundError
		var authErr *services.AuthenticationError
		
		switch {
		case errors.As(err, &notFoundErr):
			return nil, fmt.Errorf("resource not found: %s. Use 'azd mycommand list' to see available resources", notFoundErr.ResourceName)
		
		case errors.As(err, &authErr):
			return nil, fmt.Errorf("authentication failed: %w. Run 'azd auth login' to re-authenticate", err)
		
		default:
			return nil, fmt.Errorf("operation failed: %w", err)
		}
	}
	
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Operation completed",
		},
	}, nil
}
```

## Integration with IoC Container

### Service Registration

When your command requires new services, register them in the appropriate place:

```go
// In pkg/ioc/container.go or appropriate service registration location
func RegisterMyServices(container *ioc.Container) {
	// Register your service
	ioc.RegisterSingleton(container, func() *services.MyService {
		return services.NewMyService()
	})
	
	// Register service with dependencies
	ioc.RegisterSingleton(container, func(
		httpClient *http.Client,
		config *config.Config,
	) *services.MyComplexService {
		return services.NewMyComplexService(httpClient, config)
	})
}
```

### Using Services in Actions

```go
// Your action constructor automatically receives services via DI
func newMyCommandAction(
	flags *myCommandFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	// Your custom services
	myService *services.MyService,
	azureService *azure.AzureService,
	// Standard azd services
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
) actions.Action {
	return &myCommandAction{
		flags:        flags,
		console:      console,
		formatter:    formatter,
		writer:       writer,
		myService:    myService,
		azureService: azureService,
		azdContext:   azdContext,
		env:          env,
	}
}
```

### Common Service Dependencies

```go
// Commonly used services in azd commands:

// Environment and context
azdContext *azdcontext.AzdContext
env *environment.Environment

// Azure services
accountManager account.Manager
subscriptionResolver account.SubscriptionTenantResolver
resourceManager infra.ResourceManager
resourceService *azapi.ResourceService

// User interaction
console input.Console
formatter output.Formatter
writer io.Writer

// Configuration
config *config.Config
alphaFeatureManager *alpha.FeatureManager

// Project and templates
projectManager *project.ProjectManager
templateManager *templates.TemplateManager
```

## Complete Examples

### Example 1: Simple Single Command

File: `cmd/validate.go`

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Add to root.go registration
// root.Add("validate", &actions.ActionDescriptorOptions{
//     Command:        newValidateCmd(),
//     ActionResolver: newValidateAction,
//     FlagsResolver:  newValidateFlags,
//     OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
//     DefaultFormat:  output.NoneFormat,
//     GroupingOptions: actions.CommandGroupOptions{
//         RootLevelHelp: actions.CmdGroupManage,
//     },
// })

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the current project configuration.",
	}
}

type validateFlags struct {
	strict bool
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func newValidateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *validateFlags {
	flags := &validateFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *validateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&f.strict, "strict", false, "Enable strict validation mode")
	f.EnvFlag.Bind(local, global)
	f.global = global
}

type validateAction struct {
	flags          *validateFlags
	console        input.Console
	projectManager *project.ProjectManager
}

func newValidateAction(
	flags *validateFlags,
	console input.Console,
	projectManager *project.ProjectManager,
) actions.Action {
	return &validateAction{
		flags:          flags,
		console:        console,
		projectManager: projectManager,
	}
}

func (a *validateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.Message(ctx, "Validating project configuration...")
	
	// TODO: Implement validation logic
	// isValid, errors := a.projectManager.Validate(ctx, a.flags.strict)
	// if !isValid {
	//     return nil, fmt.Errorf("validation failed: %v", errors)
	// }
	
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Project validation completed successfully",
			FollowUp: "Your project is ready for deployment.",
		},
	}, nil
}
```

### Example 2: Command Group with Multiple Subcommands

File: `cmd/resource.go`

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Add to root.go: resourceActions(root)
func resourceActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("resource", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "resource",
			Short: "Manage Azure resources for the current project.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupAzure,
		},
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list",
			Short: "List Azure resources for the current project.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newResourceListAction,
		FlagsResolver:  newResourceListFlags,
	})

	group.Add("show", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "show <resource-id>",
			Short: "Show details for a specific Azure resource.",
			Args:  cobra.ExactArgs(1),
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newResourceShowAction,
	})

	group.Add("delete", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "delete <resource-id>",
			Short: "Delete a specific Azure resource.",
			Args:  cobra.ExactArgs(1),
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newResourceDeleteAction,
		FlagsResolver:  newResourceDeleteFlags,
	})

	return group
}

// List command implementation
type resourceListFlags struct {
	resourceType string
	location     string
	global       *internal.GlobalCommandOptions
	internal.EnvFlag
}

func newResourceListFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *resourceListFlags {
	flags := &resourceListFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *resourceListFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(&f.resourceType, "type", "", "Filter by resource type")
	local.StringVar(&f.location, "location", "", "Filter by location")
	f.EnvFlag.Bind(local, global)
	f.global = global
}

type resourceListAction struct {
	flags     *resourceListFlags
	formatter output.Formatter
	console   input.Console
	writer    io.Writer
	// TODO: Add actual Azure resource service
	// resourceService *azure.ResourceService
}

func newResourceListAction(
	flags *resourceListFlags,
	formatter output.Formatter,
	console input.Console,
	writer io.Writer,
) actions.Action {
	return &resourceListAction{
		flags:     flags,
		formatter: formatter,
		console:   console,
		writer:    writer,
	}
}

type resourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Status   string `json:"status"`
}

func (a *resourceListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "List Azure resources (azd resource list)",
		TitleNote: "Retrieving resources for current project",
	})

	// TODO: Implement actual resource listing
	// resources, err := a.resourceService.ListForProject(ctx, a.flags.resourceType, a.flags.location)
	// if err != nil {
	//     return nil, fmt.Errorf("failed to list resources: %w", err)
	// }

	// Placeholder data
	resources := []resourceInfo{
		{
			ID:       "/subscriptions/xxx/resourceGroups/rg-example/providers/Microsoft.Web/sites/example-app",
			Name:     "example-app",
			Type:     "Microsoft.Web/sites",
			Location: "eastus",
			Status:   "Running",
		},
	}

	if len(resources) == 0 {
		a.console.Message(ctx, output.WithWarningFormat("No resources found."))
		return nil, nil
	}

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Type",
				ValueTemplate: "{{.Type}}",
			},
			{
				Heading:       "Location",
				ValueTemplate: "{{.Location}}",
			},
			{
				Heading:       "Status",
				ValueTemplate: "{{.Status}}",
			},
		}

		return nil, a.formatter.Format(resources, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	}

	return nil, a.formatter.Format(resources, a.writer, nil)
}

// Show command implementation
type resourceShowAction struct {
	args      []string
	formatter output.Formatter
	console   input.Console
	writer    io.Writer
}

func newResourceShowAction(
	args []string,
	formatter output.Formatter,
	console input.Console,
	writer io.Writer,
) actions.Action {
	return &resourceShowAction{
		args:      args,
		formatter: formatter,
		console:   console,
		writer:    writer,
	}
}

func (a *resourceShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	resourceID := a.args[0]

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Show Azure resource (azd resource show)",
		TitleNote: fmt.Sprintf("Retrieving details for '%s'", resourceID),
	})

	// TODO: Implement actual resource details retrieval
	// resource, err := a.resourceService.Get(ctx, resourceID)
	// if err != nil {
	//     return nil, fmt.Errorf("failed to get resource details: %w", err)
	// }

	// For now, just show that the command structure works
	a.console.Message(ctx, fmt.Sprintf("Resource ID: %s", resourceID))
	a.console.Message(ctx, "TODO: Implement resource details display")

	return nil, nil
}

// Delete command implementation
type resourceDeleteFlags struct {
	force  bool
	global *internal.GlobalCommandOptions
}

func newResourceDeleteFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *resourceDeleteFlags {
	flags := &resourceDeleteFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *resourceDeleteFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVarP(&f.force, "force", "f", false, "Force deletion without confirmation")
	f.global = global
}

type resourceDeleteAction struct {
	args    []string
	flags   *resourceDeleteFlags
	console input.Console
}

func newResourceDeleteAction(
	args []string,
	flags *resourceDeleteFlags,
	console input.Console,
) actions.Action {
	return &resourceDeleteAction{
		args:    args,
		flags:   flags,
		console: console,
	}
}

func (a *resourceDeleteAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	resourceID := a.args[0]

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Delete Azure resource (azd resource delete)",
		TitleNote: fmt.Sprintf("Deleting resource '%s'", resourceID),
	})

	if !a.flags.force {
		confirmed, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Are you sure you want to delete '%s'?", resourceID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get confirmation: %w", err)
		}
		if !confirmed {
			a.console.Message(ctx, "Deletion cancelled.")
			return nil, nil
		}
	}

	stepMessage := fmt.Sprintf("Deleting %s", output.WithHighLightFormat(resourceID))
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	// TODO: Implement actual resource deletion
	// err := a.resourceService.Delete(ctx, resourceID)
	// if err != nil {
	//     a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
	//     return nil, fmt.Errorf("failed to delete resource: %w", err)
	// }

	// Simulate work
	time.Sleep(1 * time.Second)

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Successfully deleted resource '%s'", resourceID),
			FollowUp: "Use 'azd resource list' to see remaining resources.",
		},
	}, nil
}
```

## Summary

This guide provides a complete framework for adding new commands to azd. The key steps are:

1. **Choose the pattern**: Single command or command group
2. **Create the file**: Follow naming conventions in `cmd/` directory
3. **Define the structure**: ActionDescriptor → Flags → Action
4. **Implement the logic**: Start with TODO comments for actual functionality
5. **Register the command**: Add to `root.go` or parent command group
6. **Handle dependencies**: Use IoC container for service injection
7. **Format output**: Support JSON, Table, and None formats appropriately
8. **Handle errors**: Provide clear error messages with guidance

The generated command shells will compile and provide the basic CLI structure, allowing developers to focus on implementing the actual business logic within the marked TODO sections.
