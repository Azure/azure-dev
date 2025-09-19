// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// tryAutoInstallExtension attempts to auto-install an extension if the unknown command matches an available
// extension namespace. Returns true if an extension was found and installed, false otherwise.
func tryAutoInstallExtension(
	ctx context.Context, rootContainer *ioc.NestedContainer, unknownCommand string) (bool, error) {
	var extensionManager *extensions.Manager

	if err := rootContainer.Resolve(&extensionManager); err != nil {
		// If we can't resolve the extension manager, we can't auto-install
		log.Println("Failed to resolve extension manager for auto-install extension:", err)
		return false, nil
	}

	// Check if the unknown command matches any available extension namespace
	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		// If we can't list registry extensions, we can't auto-install
		log.Println("Failed to list registry extensions:", err)
		return false, nil
	}

	var matchingExtension *extensions.ExtensionMetadata
	for _, ext := range registryExtensions {
		// Check if the namespace matches the unknown command
		namespaceParts := strings.Split(ext.Namespace, ".")
		if len(namespaceParts) > 0 && namespaceParts[0] == unknownCommand {
			matchingExtension = ext
			break
		}
	}

	if matchingExtension == nil {
		// No matching extension found
		log.Println("No matching extension found for auto-install")
		return false, nil
	}

	// Check if the extension is already installed
	_, err = extensionManager.GetInstalled(extensions.LookupOptions{
		Id: matchingExtension.Id,
	})
	if err == nil {
		// Extension is already installed, this shouldn't happen but let's be safe
		log.Println("Extension already installed during auto-install check:", matchingExtension.Id)
		return false, nil
	}

	var console input.Console
	if err := rootContainer.Resolve(&console); err != nil {
		log.Println("Failed to resolve console for auto-install extension:", err)
		return false, nil
	}

	// Ask user for permission to auto-install the extension
	console.Message(ctx,
		fmt.Sprintf("Command '%s' not found, but there's an available extension that provides it.\n", unknownCommand))
	console.Message(ctx,
		fmt.Sprintf("Extension: %s (%s)\n", matchingExtension.DisplayName, matchingExtension.Description))
	shouldInstall, err := console.Confirm(ctx, input.ConsoleOptions{
		DefaultValue: true,
		Message:      "Would you like to install it?",
	})
	if err != nil {
		log.Println("Failed to get user confirmation for auto-install extension:", err)
		return false, nil
	}

	if !shouldInstall {
		log.Println("User declined to install extension:", matchingExtension.Id)
		return false, nil
	}

	// Install the extension
	console.Message(ctx,
		fmt.Sprintf("Installing extension '%s'...\n", matchingExtension.Id))
	filterOptions := &extensions.FilterOptions{}
	_, err = extensionManager.Install(ctx, matchingExtension.Id, filterOptions)
	if err != nil {
		return false, fmt.Errorf("failed to install extension: %w", err)
	}

	console.Message(ctx,
		fmt.Sprintf("Extension '%s' installed successfully!\n", matchingExtension.Id))
	return true, nil
}

// ExecuteWithAutoInstall executes the command and handles auto-installation of extensions for unknown commands.
func ExecuteWithAutoInstall(ctx context.Context, rootContainer *ioc.NestedContainer) error {
	// First, try to execute the command normally
	rootCmd := NewRootCmd(false, nil, rootContainer)
	err := rootCmd.ExecuteContext(ctx)

	if err != nil {
		originalArgs := os.Args[1:]
		// Check if this is an "unknown command" error
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown command") && len(originalArgs) > 0 {
			// Extract the unknown command from the arguments
			unknownCommand := originalArgs[0]

			// Try to auto-install an extension for this command
			installed, installErr := tryAutoInstallExtension(ctx, rootContainer, unknownCommand)
			if installErr != nil {
				// If auto-install failed, return the original error
				return err
			}

			if installed {
				// Extension was installed, rebuild the command tree and try again
				rootCmd = NewRootCmd(false, nil, rootContainer)
				return rootCmd.ExecuteContext(ctx)
			}
		}

		// Return the original error if we couldn't auto-install
		return err
	}

	return nil
}
