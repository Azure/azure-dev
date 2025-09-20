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
)

// findFirstNonFlagArg finds the first argument that doesn't start with '-'
func findFirstNonFlagArg(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// checkForMatchingExtension checks if the first argument matches any available extension namespace
func checkForMatchingExtension(
	ctx context.Context, extensionManager *extensions.Manager, command string) (*extensions.ExtensionMetadata, error) {
	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		return nil, err
	}

	for _, ext := range registryExtensions {
		namespaceParts := strings.Split(ext.Namespace, ".")
		if len(namespaceParts) > 0 && namespaceParts[0] == command {
			return ext, nil
		}
	}

	return nil, nil
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

	originalArgs := os.Args[1:]
	// Find the first non-flag argument (the actual command)
	unknownCommand := findFirstNonFlagArg(originalArgs)

	// If we have a command, check if it might be an extension command
	if unknownCommand != "" {
		var extensionManager *extensions.Manager
		if err := rootContainer.Resolve(&extensionManager); err != nil {
			log.Panic("failed to resolve extension manager for auto-install:", err)
		}
		// Check if this command might match an extension before trying to execute
		extensionMatch, err := checkForMatchingExtension(ctx, extensionManager, unknownCommand)
		if err != nil {
			// Do not fail if we couldn't check for extensions - just proceed to normal execution
			log.Println("Error: check for extensions. Skipping auto-install:", err)
			return rootCmd.ExecuteContext(ctx)
		}
		if extensionMatch != nil {
			// Try to auto-install the extension first
			var console input.Console
			if err := rootContainer.Resolve(&console); err != nil {
				log.Panic("failed to resolve console for auto-install:", err)
			}
			installed, installErr := tryAutoInstallExtension(ctx, console, extensionManager, *extensionMatch)
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
