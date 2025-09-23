// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// extractFlagsWithValues extracts flags that take values from a cobra command.
// This ensures we have a single source of truth for flag definitions by
// dynamically inspecting the command's flag definitions instead of
// maintaining a separate hardcoded list.
//
// The function inspects both regular flags and persistent flags, checking
// the flag's value type to determine if it takes an argument:
// - Bool flags don't take values
// - String, Int, StringSlice, etc. flags do take values
func extractFlagsWithValues(cmd *cobra.Command) map[string]bool {
	flagsWithValues := make(map[string]bool)

	// Extract flags that take values from the command
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// String, StringSlice, StringArray, Int, Int64, etc. all take values
		// Bool flags don't take values
		if flag.Value.Type() != "bool" {
			flagsWithValues["--"+flag.Name] = true
			if flag.Shorthand != "" {
				flagsWithValues["-"+flag.Shorthand] = true
			}
		}
	})

	// Also check persistent flags (global flags)
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Value.Type() != "bool" {
			flagsWithValues["--"+flag.Name] = true
			if flag.Shorthand != "" {
				flagsWithValues["-"+flag.Shorthand] = true
			}
		}
	})

	return flagsWithValues
}

// findFirstNonFlagArg finds the first argument that doesn't start with '-' and isn't a flag value.
// This function properly handles flags that take values (like --output json) to avoid
// incorrectly identifying flag values as commands.
// Returns the command and any unknown flags encountered before the command.
func findFirstNonFlagArg(args []string, flagsWithValues map[string]bool) (command string, unknownFlags []string) {
	// Initialize as empty slice instead of nil for consistent behavior
	unknownFlags = []string{}

	skipNext := false
	for i, arg := range args {
		// Skip this argument if it's marked as a flag value from previous iteration
		if skipNext {
			skipNext = false
			continue
		}

		// If it doesn't start with '-', it's a potential command
		if !strings.HasPrefix(arg, "-") {
			return arg, unknownFlags
		}

		// Check if this is a known flag that takes a value
		if flagsWithValues[arg] {
			// This flag takes a value, so skip the next argument
			skipNext = true
			continue
		}

		// Handle flags with '=' syntax like --output=json
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if flagsWithValues[parts[0]] {
				// This is a known flag=value format, no need to skip next
				continue
			}
			// Unknown flag with equals - record it
			unknownFlags = append(unknownFlags, parts[0])
			continue
		}

		// This is an unknown flag - record it
		unknownFlags = append(unknownFlags, arg)

		// Conservative heuristic: if the next argument doesn't start with '-'
		// and there are more args after it, assume the unknown flag takes a value
		if i+1 < len(args) && i+2 < len(args) {
			nextArg := args[i+1]
			argAfterNext := args[i+2]
			if !strings.HasPrefix(nextArg, "-") && !strings.HasPrefix(argAfterNext, "-") {
				// Pattern: --unknown value command
				// Skip the value, let command be found next
				skipNext = true
			}
		}
	}

	return "", unknownFlags
}

// checkForMatchingExtensions checks for extensions that match any possible namespace
// from the command arguments. For example, "azd vhvb demo foo" will check for
// extensions with namespaces: "vhvb", "vhvb.demo", "vhvb.demo.foo"
func checkForMatchingExtensions(
	ctx context.Context, extensionManager *extensions.Manager, args []string) ([]*extensions.ExtensionMetadata, error) {
	if len(args) == 0 {
		return nil, nil
	}

	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		return nil, err
	}

	var matchingExtensions []*extensions.ExtensionMetadata

	// Generate all possible namespace combinations from the command arguments
	// For "azd vhvb demo foo" -> check "vhvb", "vhvb.demo", "vhvb.demo.foo"
	for i := 1; i <= len(args); i++ {
		candidateNamespace := strings.Join(args[:i], ".")

		// Check if any extension has this exact namespace
		for _, ext := range registryExtensions {
			if ext.Namespace == candidateNamespace {
				matchingExtensions = append(matchingExtensions, ext)
			}
		}
	}

	return matchingExtensions, nil
}

// promptForExtensionChoice prompts the user to choose from multiple matching extensions
func promptForExtensionChoice(
	ctx context.Context,
	console input.Console,
	extensions []*extensions.ExtensionMetadata) (*extensions.ExtensionMetadata, error) {

	if len(extensions) == 0 {
		return nil, nil
	}

	if len(extensions) == 1 {
		return extensions[0], nil
	}

	console.Message(ctx, "Multiple extensions found that match your command:")
	console.Message(ctx, "")

	options := make([]string, len(extensions))
	for i, ext := range extensions {
		options[i] = fmt.Sprintf("%s (%s) - %s", ext.Namespace, ext.DisplayName, ext.Description)
		console.Message(ctx, fmt.Sprintf("  %d. %s", i+1, options[i]))
	}
	console.Message(ctx, "")

	choice, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which extension would you like to install?",
		Options: options,
	})
	if err != nil {
		return nil, err
	}

	return extensions[choice], nil
}

// isBuiltInCommand checks if the given command is a built-in command by examining
// the root command's command tree. This includes both core azd commands and any
// installed extensions, preventing auto-install from triggering for known commands.
func isBuiltInCommand(rootCmd *cobra.Command, commandName string) bool {
	if commandName == "" {
		return false
	}

	// Check if the command exists in the root command's subcommands
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == commandName {
			return true
		}
		// Also check aliases
		for _, alias := range cmd.Aliases {
			if alias == commandName {
				return true
			}
		}
	}

	return false
}

// tryAutoInstallExtension attempts to auto-install an extension if the unknown command matches an available
// extension namespace. Returns true if an extension was found and installed, false otherwise.
func tryAutoInstallExtension(
	ctx context.Context,
	console input.Console,
	extensionManager *extensions.Manager,
	extension extensions.ExtensionMetadata) (bool, error) {

	// Check if the extension is already installed
	_, err := extensionManager.GetInstalled(extensions.LookupOptions{
		Id: extension.Id,
	})
	if err == nil {
		return false, nil
	}

	// Return error if running in CI/CD environment
	if resource.IsRunningOnCI() {
		return false,
			fmt.Errorf(
				"Command '%s' not found, but there's an available extension that provides it.\n"+
					"However, auto-installation is not supported in CI/CD environments.\n"+
					"Run '%s' to install it manually.",
				extension.Namespace,
				fmt.Sprintf("azd extension install %s", extension.Id))
	}

	// Ask user for permission to auto-install the extension
	console.Message(ctx,
		fmt.Sprintf("Command '%s' not found, but there's an available extension that provides it.\n", extension.Namespace))
	console.Message(ctx,
		fmt.Sprintf("Extension: %s (%s)\n", extension.DisplayName, extension.Description))
	shouldInstall, err := console.Confirm(ctx, input.ConsoleOptions{
		DefaultValue: true,
		Message:      "Would you like to install it?",
	})
	if err != nil {
		return false, nil
	}

	if !shouldInstall {
		return false, nil
	}

	// Install the extension
	console.Message(ctx, fmt.Sprintf("Installing extension '%s'...\n", extension.Id))
	filterOptions := &extensions.FilterOptions{}
	_, err = extensionManager.Install(ctx, extension.Id, filterOptions)
	if err != nil {
		return false, fmt.Errorf("failed to install extension: %w", err)
	}

	console.Message(ctx, fmt.Sprintf("Extension '%s' installed successfully!\n", extension.Id))
	return true, nil
}

// ExecuteWithAutoInstall executes the command and handles auto-installation of extensions for unknown commands.
func ExecuteWithAutoInstall(ctx context.Context, rootContainer *ioc.NestedContainer) error {
	// Creating the RootCmd takes care of registering common dependencies in rootContainer
	rootCmd := NewRootCmd(false, nil, rootContainer)

	// Continue only if extensions feature is enabled
	err := rootContainer.Invoke(func(alphaFeatureManager *alpha.FeatureManager) error {
		if !alphaFeatureManager.IsEnabled(extensions.FeatureExtensions) {
			return fmt.Errorf("extensions feature is not enabled")
		}
		return nil
	})
	if err != nil {
		// Error here means extensions are not enabled or failed to resolve the feature manager
		// In either case, we just proceed to normal execution
		log.Println("auto-install extensions: ", err)
		return rootCmd.ExecuteContext(ctx)
	}

	// Get the original args passed to the command (excluding the program name)
	originalArgs := os.Args[1:]

	// Extract flags that take values from the root command
	flagsWithValues := extractFlagsWithValues(rootCmd)

	// Find the first non-flag argument (the actual command) and check for unknown flags
	unknownCommand, unknownFlags := findFirstNonFlagArg(originalArgs, flagsWithValues)

	// If we have a command, check if it's a built-in command first
	if unknownCommand != "" {
		// Check if this is a built-in command first (includes core commands and installed extensions)
		if isBuiltInCommand(rootCmd, unknownCommand) {
			// This is a built-in command, proceed with normal execution without checking for extensions
			return rootCmd.ExecuteContext(ctx)
		}

		// If unknown flags were found before a non-built-in command, return an error with helpful guidance
		if len(unknownFlags) > 0 {
			var console input.Console
			if err := rootContainer.Resolve(&console); err != nil {
				log.Panic("failed to resolve console for unknown flags error:", err)
			}

			flagsList := strings.Join(unknownFlags, ", ")
			errorMsg := fmt.Sprintf(
				"Unknown flags detected before command '%s': %s\n\n"+
					"If you're trying to run an extension command, the extension name must come BEFORE any flags.\n"+
					"This is because extension-specific flags are not known until the extension is installed.\n\n"+
					"Correct usage:\n"+
					"  azd %s %s    # Extension name first, then flags\n"+
					"  azd %s --help          # Get help for the extension\n\n"+
					"If this is not an extension command, please check the flag names for typos.",
				unknownCommand, flagsList,
				unknownCommand, strings.Join(unknownFlags, " "),
				unknownCommand)

			console.Message(ctx, errorMsg)
			return fmt.Errorf("unknown flags before command: %s", flagsList)
		}

		var extensionManager *extensions.Manager
		if err := rootContainer.Resolve(&extensionManager); err != nil {
			log.Panic("failed to resolve extension manager for auto-install:", err)
		}

		// Get all remaining arguments starting from the command for namespace matching
		// This allows checking longer namespaces like "vhvb.demo.foo" from "azd vhvb demo foo"
		var argsForMatching []string
		for i, arg := range originalArgs {
			if !strings.HasPrefix(arg, "-") && arg == unknownCommand {
				// Found the command, collect all non-flag arguments from here
				for j := i; j < len(originalArgs); j++ {
					if !strings.HasPrefix(originalArgs[j], "-") {
						argsForMatching = append(argsForMatching, originalArgs[j])
					}
				}
				break
			}
		}

		// Check if any commands might match extensions with various namespace lengths
		extensionMatches, err := checkForMatchingExtensions(ctx, extensionManager, argsForMatching)
		if err != nil {
			// Do not fail if we couldn't check for extensions - just proceed to normal execution
			log.Println("Error: check for extensions. Skipping auto-install:", err)
			return rootCmd.ExecuteContext(ctx)
		}

		if len(extensionMatches) > 0 {
			var console input.Console
			if err := rootContainer.Resolve(&console); err != nil {
				log.Panic("failed to resolve console for auto-install:", err)
			}

			// Prompt user to choose if multiple extensions match
			chosenExtension, err := promptForExtensionChoice(ctx, console, extensionMatches)
			if err != nil {
				console.Message(ctx, fmt.Sprintf("Error selecting extension: %v", err))
				return rootCmd.ExecuteContext(ctx)
			}

			if chosenExtension == nil {
				// User cancelled selection, proceed to normal execution
				return rootCmd.ExecuteContext(ctx)
			}

			// Try to auto-install the chosen extension
			installed, installErr := tryAutoInstallExtension(ctx, console, extensionManager, *chosenExtension)
			if installErr != nil {
				// Error needs to be printed here or else it will be hidden b/c the error printing is handled inside runtime
				console.Message(ctx, installErr.Error())
				return installErr
			}

			if installed {
				// Extension was installed, build command tree and execute
				rootCmd := NewRootCmd(false, nil, rootContainer)
				return rootCmd.ExecuteContext(ctx)
			}
		}
	}

	// Normal execution path - either no args, no matching extension, or user declined install
	return rootCmd.ExecuteContext(ctx)
}
