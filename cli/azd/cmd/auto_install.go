// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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
	// IMPORTANT: cmd.Flags().VisitAll() does NOT include persistent flags.
	// In Cobra, cmd.Flags() only returns local flags specific to that command,
	// while cmd.PersistentFlags() returns flags that are inherited by subcommands.
	// These are separate flag sets, so we must call both VisitAll functions
	// to capture all flags that can take values.
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
// from the command arguments. For example, "azd foo demo bar" will check for
// extensions with namespaces: "foo", "foo.demo", "foo.demo.bar"
func checkForMatchingExtensions(
	ctx context.Context, extensionManager *extensions.Manager, args []string) ([]*extensions.ExtensionMetadata, error) {
	if len(args) == 0 {
		return nil, nil
	}

	options := &extensions.FilterOptions{}
	registryExtensions, err := extensionManager.FindExtensions(ctx, options)
	if err != nil {
		return nil, err
	}

	var matchingExtensions []*extensions.ExtensionMetadata

	// Generate all possible namespace combinations from the command arguments
	// For "azd something demo foo" -> check "something", "something.demo", "something.demo.foo"
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
		return nil, fmt.Errorf("no extensions to choose from")
	}

	if len(extensions) == 1 {
		return extensions[0], nil
	}

	options := make([]string, len(extensions))
	for i, ext := range extensions {
		options[i] = fmt.Sprintf("%s (%s) - %s", ext.DisplayName, ext.Source, ext.Description)
	}

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
	_, err := extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extension.Id,
	})
	if err == nil {
		return false, nil
	}

	// Return error if running in CI/CD environment
	if resource.IsRunningOnCI() {
		return false,
			fmt.Errorf(
				"Auto-installation is not supported in CI/CD environments.\n"+
					"Run '%s' to install it manually.",
				fmt.Sprintf("azd extension install %s", extension.Id))
	}

	console.MessageUxItem(ctx, &ux.WarningMessage{
		Description: "You are about to install an extension !!",
	})
	console.Message(ctx, fmt.Sprintf("Source: %s", extension.Source))
	console.Message(ctx, fmt.Sprintf("Id: %s", extension.Id))
	console.Message(ctx, fmt.Sprintf("Name: %s", extension.DisplayName))
	console.Message(ctx, fmt.Sprintf("Description: %s", extension.Description))

	// Ask user for permission to auto-install the extension
	shouldInstall, err := console.Confirm(ctx, input.ConsoleOptions{
		DefaultValue: true,
		Message:      "Confirm installation",
	})
	if err != nil {
		return false, nil
	}

	if !shouldInstall {
		return false, nil
	}

	// Install the extension
	console.Message(ctx, fmt.Sprintf("Installing extension '%s'...\n", extension.Id))
	_, err = extensionManager.Install(ctx, &extension, "")
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

	var extensionManager *extensions.Manager
	if err := rootContainer.Resolve(&extensionManager); err != nil {
		log.Panic("failed to resolve extension manager for auto-install:", err)
	}
	var console input.Console
	if err := rootContainer.Resolve(&console); err != nil {
		log.Panic("failed to resolve console for unknown flags error:", err)
	}

	// rootCmd.Find() returns error if the command is not identified. Cobra checks all the registered commands
	// and returns error if the input command is not registered.
	// This allows us to determine if a subcommand was provided or not or if the command is unknown.
	_, originalArgs, err := rootCmd.Find(os.Args[1:])
	if err == nil {
		// Known command, no need to auto-install
		err := rootCmd.ExecuteContext(ctx)

		// auto-install for target service
		var unsupportedErr *project.UnsupportedServiceHostError
		if errors.As(err, &unsupportedErr) {
			discoveryResults, capErr := DiscoverServiceTargetCapabilities(ctx, extensionManager)
			if capErr != nil {
				log.Printf("failed to discover service target capabilities: %v", capErr)
				log.Printf("ignoring auto-install for service target")
				return err
			}
			filterMatches := []*extensions.ExtensionMetadata{}
			for _, result := range discoveryResults {
				if slices.Contains(result.serviceTargets, unsupportedErr.Host) {
					filterMatches = append(filterMatches, result.extensionMetadata)
				}
			}
			if len(filterMatches) == 0 {
				// did not find an extension with the capability, just print the original error message
				console.Message(ctx, unsupportedErr.ErrorMessage)
			}

			console.Message(ctx,
				fmt.Sprintf("Your project is using host '%s' which is not supported by default.\n", unsupportedErr.Host))

			var extensionIdToInstall extensions.ExtensionMetadata
			if len(filterMatches) == 1 {
				extensionIdToInstall = *filterMatches[0]
				console.Message(ctx, "An extension was found that provides support for this host.")
			} else {
				console.Message(ctx, "There are multiple extensions that provide support for this host.")
				// Multiple matches found, prompt user to choose
				chosenExtension, err := promptForExtensionChoice(ctx, console, filterMatches)
				if err != nil {
					console.Message(ctx, fmt.Sprintf("Error selecting extension: %v", err))
					return err
				}
				extensionIdToInstall = *chosenExtension
			}

			installed, installErr := tryAutoInstallExtension(ctx, console, extensionManager, extensionIdToInstall)
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

		return err
	}

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

		// Get all remaining arguments starting from the command for namespace matching
		// This allows checking longer namespaces like "something.demo.foo" from "azd something demo foo"
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

			console.Message(ctx,
				fmt.Sprintf("Command '%s' was not found, but there's an available extension that provides it\n",
					strings.Join(argsForMatching, " ")))

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

type discoveryResult struct {
	extensionName     string
	extensionMetadata *extensions.ExtensionMetadata
	serviceTargets    []string
	err               error
}

// DiscoverServiceTargetCapabilities discovers service target capabilities from all non-installed extensions
// by temporarily pulling their binaries and checking what service targets they provide.
func DiscoverServiceTargetCapabilities(
	ctx context.Context, extensionManager *extensions.Manager) ([]discoveryResult, error) {
	// Get all extensions from registry
	options := &extensions.FilterOptions{}
	registryExtensions, err := extensionManager.FindExtensions(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list extensions from registry: %w", err)
	}

	// Filter out already installed extensions
	var nonInstalledExtensions []*extensions.ExtensionMetadata
	for _, ext := range registryExtensions {
		_, err := extensionManager.GetInstalled(extensions.FilterOptions{
			Id: ext.Id,
		})
		if err != nil {
			// Extension is not installed, add to list
			nonInstalledExtensions = append(nonInstalledExtensions, ext)
		}
	}

	if len(nonInstalledExtensions) == 0 {
		return nil, nil
	}

	resultChan := make(chan discoveryResult, len(nonInstalledExtensions))
	var wg sync.WaitGroup

	// Launch a goroutine for each non-installed extension
	for _, ext := range nonInstalledExtensions {
		wg.Add(1)
		go func(extension *extensions.ExtensionMetadata) {
			defer wg.Done()

			// Check if extension has service target provider capability
			// Check the latest version's capabilities
			if len(extension.Versions) > 0 {
				// Use the first version (typically the latest)
				latestVersion := extension.Versions[0]
				if slices.Contains(latestVersion.Capabilities, extensions.ServiceTargetProviderCapability) {
					serviceTargets := make([]string, 0)
					for _, provider := range latestVersion.Providers {
						if provider.Type == extensions.ServiceTargetProviderType {
							serviceTargets = append(serviceTargets, provider.Name)
						}
					}
					resultChan <- discoveryResult{
						extensionName:     extension.DisplayName,
						extensionMetadata: extension,
						serviceTargets:    serviceTargets,
						err:               err,
					}
					return
				}
			}

			// no service target provider capability
			resultChan <- discoveryResult{
				extensionName:     extension.Id,
				extensionMetadata: extension,
				serviceTargets:    nil,
				err:               nil,
			}

		}(ext)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var discoveryResults []discoveryResult
	for result := range resultChan {
		if result.err != nil {
			log.Printf("Error discovering service targets for extension %s: %v", result.extensionName, result.err)
			continue
		}
		if len(result.serviceTargets) > 0 {
			discoveryResults = append(discoveryResults, result)
		}
	}

	return discoveryResults, nil
}
