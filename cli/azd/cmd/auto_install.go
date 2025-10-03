// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

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

	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
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

	var extensionManager *extensions.Manager
	if err := rootContainer.Resolve(&extensionManager); err != nil {
		log.Panic("failed to resolve extension manager for auto-install:", err)
	}
	output, err1 := DiscoverServiceTargetCapabilities(ctx, extensionManager)
	if err1 != nil {
		log.Panic("failed to discover service target capabilities:", err1)
	}
	_ = output

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

	// rootCmd.Find() returns error if the command is not identified. Cobra checks all the registered commands
	// and returns error if the input command is not registered.
	// This allows us to determine if a subcommand was provided or not or if the command is unknown.
	_, originalArgs, err := rootCmd.Find(os.Args[1:])
	if err == nil {
		// Known command, no need to auto-install
		return rootCmd.ExecuteContext(ctx)
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

// DiscoverServiceTargetCapabilities discovers service target capabilities from all non-installed extensions
// by temporarily pulling their binaries and checking what service targets they provide.
func DiscoverServiceTargetCapabilities(ctx context.Context, extensionManager *extensions.Manager) (map[string][]string, error) {
	// Get all extensions from registry
	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list extensions from registry: %w", err)
	}

	// Filter out already installed extensions
	var nonInstalledExtensions []*extensions.ExtensionMetadata
	for _, ext := range registryExtensions {
		_, err := extensionManager.GetInstalled(extensions.LookupOptions{
			Id: ext.Id,
		})
		if err != nil {
			// Extension is not installed, add to list
			nonInstalledExtensions = append(nonInstalledExtensions, ext)
		}
	}

	if len(nonInstalledExtensions) == 0 {
		return make(map[string][]string), nil
	}

	// Create a channel to collect results and a wait group to coordinate goroutines
	type discoveryResult struct {
		extensionName  string
		serviceTargets []string
		err            error
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
			hasServiceTargetCapability := false
			if len(extension.Versions) > 0 {
				// Use the first version (typically the latest)
				latestVersion := extension.Versions[0]
				for _, capability := range latestVersion.Capabilities {
					if capability == extensions.ServiceTargetProviderCapability {
						hasServiceTargetCapability = true
						break
					}
				}
			}

			if !hasServiceTargetCapability {
				resultChan <- discoveryResult{
					extensionName:  extension.DisplayName,
					serviceTargets: nil,
					err:            nil,
				}
				return
			}

			// Try to discover service targets from this extension
			serviceTargets, err := discoverServiceTargetsFromExtension(ctx, extensionManager, extension)
			resultChan <- discoveryResult{
				extensionName:  extension.DisplayName,
				serviceTargets: serviceTargets,
				err:            err,
			}
		}(ext)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	serviceTargetMap := make(map[string][]string)
	for result := range resultChan {
		if result.err != nil {
			log.Printf("Error discovering service targets for extension %s: %v", result.extensionName, result.err)
			continue
		}
		if len(result.serviceTargets) > 0 {
			serviceTargetMap[result.extensionName] = result.serviceTargets
		}
	}

	return serviceTargetMap, nil
}

// discoverServiceTargetsFromExtension attempts to discover service targets provided by an extension
// by temporarily downloading its binary and analyzing its capabilities
func discoverServiceTargetsFromExtension(ctx context.Context, extensionManager *extensions.Manager, extension *extensions.ExtensionMetadata) ([]string, error) {
	// Create a temporary directory for the extension binary
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("azd-ext-discovery-%s-*", extension.Id))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Printf("Warning: failed to clean up temp directory %s: %v", tempDir, removeErr)
		}
	}()

	// Try to download the extension binary to temp directory
	// Note: This is a simplified approach. In a real implementation, you would need to:
	// 1. Download the extension binary from the registry source
	// 2. Extract it if it's compressed
	// 3. Make it executable
	// 4. Run it with appropriate flags to discover service targets

	// For now, we'll simulate this process since we don't have direct access to the download mechanism
	// In a real implementation, you would use extensionManager's internal download methods

	// Simulate running the extension binary to discover service targets
	// This would typically involve:
	// 1. Running: <extension-binary> --list-service-targets or similar
	// 2. Parsing the output to extract service target names
	// 3. Or using gRPC introspection if the extension supports it

	serviceTargets, err := simulateServiceTargetDiscovery(extension)
	if err != nil {
		return nil, fmt.Errorf("failed to discover service targets: %w", err)
	}

	return serviceTargets, nil
}

// simulateServiceTargetDiscovery simulates the process of discovering service targets from an extension
// This is a placeholder implementation that would be replaced with actual binary execution and introspection
func simulateServiceTargetDiscovery(extension *extensions.ExtensionMetadata) ([]string, error) {
	// This is a simulation - in a real implementation, you would:
	// 1. Execute the extension binary with discovery flags
	// 2. Parse its output or use gRPC introspection
	// 3. Extract the list of service targets it provides

	// For demonstration purposes, return some mock data based on extension name patterns
	var serviceTargets []string

	// Check extension metadata for hints about service targets
	extensionName := strings.ToLower(extension.DisplayName)

	// These are examples based on common patterns - replace with actual discovery logic
	if strings.Contains(extensionName, "demo") {
		serviceTargets = append(serviceTargets, "demo-target")
	}
	if strings.Contains(extensionName, "foundry") || strings.Contains(extensionName, "ai") {
		serviceTargets = append(serviceTargets, "ai-agent-target")
	}
	if strings.Contains(extensionName, "custom") {
		serviceTargets = append(serviceTargets, "custom-deployment-target")
	}

	// If no patterns match but it has service target capability, assume it provides at least one
	if len(serviceTargets) == 0 {
		// Use extension namespace as a default service target name
		serviceTargets = append(serviceTargets, extension.Namespace+"-target")
	}

	return serviceTargets, nil
}

// PrintServiceTargetCapabilities prints the discovered service target capabilities in a formatted way
func PrintServiceTargetCapabilities(ctx context.Context, console input.Console, serviceTargetMap map[string][]string) {
	if len(serviceTargetMap) == 0 {
		console.Message(ctx, "No extensions with service target capabilities found.")
		return
	}

	console.Message(ctx, "Discovered Service Target Capabilities:")
	console.Message(ctx, "=====================================")
	console.Message(ctx, "")

	for extensionName, serviceTargets := range serviceTargetMap {
		console.Message(ctx, fmt.Sprintf("Extension: %s", extensionName))
		console.Message(ctx, fmt.Sprintf("  Service Targets: %s", strings.Join(serviceTargets, ", ")))
		console.Message(ctx, "")
	}
}

/*
Example usage of the DiscoverServiceTargetCapabilities function:

func ExampleDiscoverServiceTargets(ctx context.Context, rootContainer *ioc.NestedContainer) error {
	var extensionManager *extensions.Manager
	var console input.Console

	if err := rootContainer.Resolve(&extensionManager); err != nil {
		return fmt.Errorf("failed to resolve extension manager: %w", err)
	}

	if err := rootContainer.Resolve(&console); err != nil {
		return fmt.Errorf("failed to resolve console: %w", err)
	}

	// Discover service target capabilities from all non-installed extensions
	serviceTargetMap, err := DiscoverServiceTargetCapabilities(ctx, extensionManager)
	if err != nil {
		return fmt.Errorf("failed to discover service target capabilities: %w", err)
	}

	// Print the results
	PrintServiceTargetCapabilities(ctx, console, serviceTargetMap)

	return nil
}
*/
