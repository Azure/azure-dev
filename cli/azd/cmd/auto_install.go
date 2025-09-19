// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	originalArgs := os.Args[1:]
	// Find the first non-flag argument (the actual command)
	unknownCommand := findFirstNonFlagArg(originalArgs)

	// If we have a command, check if it might be an extension command
	if unknownCommand != "" {
		var extensionManager *extensions.Manager
		if err := rootContainer.Resolve(&extensionManager); err != nil {
			return err
		}
		// Check if this command might match an extension before trying to execute
		extensionMatch, err := checkForMatchingExtension(ctx, extensionManager, unknownCommand)
		if err != nil {
			return err
		}
		if extensionMatch != nil {
			// Try to auto-install the extension first
			var console input.Console
			if err := rootContainer.Resolve(&console); err != nil {
				return err
			}
			installed, installErr := tryAutoInstallExtension(ctx, console, extensionManager, *extensionMatch)
			if installErr != nil {
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
