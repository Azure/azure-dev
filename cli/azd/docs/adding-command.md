# Adding an azd command

Read this if you are planning to add or update a console command to azd. 

## Design

Azd uses the [go cobra library](https://github.com/spf13/cobra) to describe the commands it support. However, with the introduction of azd-hooks, azd required to create links and properties which are beyond cobra's command regular expectations.

The [cmd](https://github.com/Azure/azure-dev/blob/main/cli/azd/cmd/root.go#L4) package in `azd` uses a higher order component called [ActionDescriptor](https://github.com/Azure/azure-dev/blob/main/cli/azd/cmd/actions/action_descriptor.go). This component defines a command beyond the cobra's command scope. It describes options, flags, middleware, and relations between other actions. After creating a `tree` of actions, the root node can be used to generate a cobra command `tree` our of it.

## Adding top level command

The `root action descriptor` from azd is called `root`. It is created as part of the `NewRootCmd()` implementation, as the starting phase of creating the cobra command hierarchy. In order to add a new top level command, you need to `add` a child *action descriptor* to `root` like:

```golang
root.Add("command-name", &actions.ActionDescriptorOptions{
    Command:          *cobra.Command,
    ActionResolver:   any,
    FlagsResolver:    any,
    DisableTelemetry: bool,
    OutputFormats:    []output.Format,
    DefaultFormat:    output.Format,
    HelpOptions:      ActionHelpOptions,
})
```

Assign each field following this notes:

- **Command**: A reference to the command to use when producing the cobra command hierarchy. Make sure to include at least a `short` description, as it is used to generate the `help documentation`. Example:

```golang
Command: &cobra.Command{
    Short: "command-name short documentation to be displayed in help docs.",
}
```

- **ActionResolver**: This is a reference to a `callback` function that produces an **actions.Action**. This function defines all the dependencies required by the action. Example:

```golang
ActionResolver: func newCommandNameAction(
        dependencyFoo DependencyTypeFoo,
        dependencyBar BarType,
    ) actions.Action {
        return &actionImplementation{
            foo: dependencyFoo,
            bar: dependencyBar,
        }
    }
```
In previous examples `actionImplementation` implements the `actions.Action` interface. As a command author, you need to set the action dependencies, but you must create an implementation for the Action interface to be returned by the `ActionResolver`.


- **FlagsResolver**: You will need to provide this field if your command will register and support flags. Before registering the `actionResolver`, **azd** will invoke this flag resolver callback. Use this resolver to `bind` command flags to your actionImplementation dependencies. Examples:

```golang
// start by abstracting the <New Command> flags. 
type NewCommandFlags struct {
	global      *internal.GlobalCommandOptions
	flagFoo string
	flagBar
}

// Add the flags as dependency for your actionImplementation
ActionResolver: func newCommandNameAction(
    newCommandFlags *NewCommandFlags
    dependencyFoo DependencyTypeFoo,
    dependencyBar BarType,
) actions.Action {
    return &actionImplementation{
        flags: newCommandFlags,
        foo: dependencyFoo,
        bar: dependencyBar,
    }
}

// Set the `FlagsResolver` to describe how to create the flags.
FlagsResolver: func newCommandNameFlags(
    cmd *cobra.Command, global *internal.GlobalCommandOptions) *NewCommandFlags {

    cobraCmdFlags := cmd.Flags()  // source flags parse by cobra command
    flags := &NewCommandFlags{}  // destination flags, your flags structure

    // bind flags
    cobraCmdFlags.StringVar(
		&flags.flagFoo,    // destination
		"foo",             // cobra command flag name          
		"default-foo",     // default value
		"This is foo",     // help for the flag
	)
    cobraCmdFlags.StringVar(
		&flags.flagBar,    // destination
		"bar",             // cobra command flag name          
		"default-bar",     // default value
		"This is bar",     // help for the flag
	)

    return flags
}
```

In the previous example, `FlagsResolver` is invoked by `azd` to produce `NewCommandFlags`, and bind the field from it to flags from a cobra command. Then, azd will use the `NewCommandFlags` as input to call `ActionResolver`. On run time, cobra will parse the flag values and set them to the instance of `NewCommandFlags` created by the `FlagsResolver`.

- **DisableTelemetry**: ***Optional*** Only set this field when no telemetry logs are expected for the command usage.

- **OutputFormats**: ***Optional*** Use this field to describe specific `output.Format` for the command. Example:

```golang
// The command can produce Json and also non-formatted output.
OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat}
```

- **DefaultFormat**: Use this field when setting `OutputFormats` to indicate what's the default format used by azd when flag `--output` is not set. Example:

```golang
DefaultFormat:  output.NoneFormat
```

- **HelpOptions**: ***Optional*** Use this to override the text block for the command help. Each block can be overridden (from default template) using a `cmdHelpGenerator`, which is a callback function like `func(cmd *cobra.Command) string`.  Example:

```golang
// Create a cmdHelpGenerator to override the Description help
func getNewCommandHelpDescription(cmd *cobra.Command) string {
    // the cobra.Command definition can be used to construct the help description.
    return fmt.sprintf("%s is my new command", cmd.Name())
}

// use the cmdHelpGenerator for Description
HelpOptions: actions.ActionHelpOptions{
    Description: getNewCommandHelpDescription,
},
```

During run-time, `azd` will use the `cmdHelpGenerator` to create the command help instead of the default. The help message in the console, for the previous example, would look like:

```
$ azd newCommand
newCommand is my new command

<default help blocks for usage/commands/flags/footer>
```

## Adding inner level command

Follow the same steps from adding a command to the top level, but instead of adding the action descriptor to the `root` descriptor, select the parent descriptor where the command will be added. For example:

```golang
// Adding a new sub command for `azd env`
envActionDescriptor.Add("myEnvSubCommand", &actions.ActionDescriptorOptions{
    <... follow the same steps from adding action descriptor to top level above ...>
})
```

## Adding the command to a command's group

In the top level help (`azd help`), azd uses a grouping classification to display all available sub-commands. If you need to add a command to a group, use the function `setGroupCommandAnnotation()
