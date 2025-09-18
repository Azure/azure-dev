// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// tryAutoInstallExtension attempts to auto-install an extension if the unknown command matches an available
// extension namespace. Returns true if an extension was found and installed, false otherwise.
func tryAutoInstallExtension(ctx context.Context, rootContainer *ioc.NestedContainer, unknownCommand string,
	originalArgs []string) (bool, error) {
	var extensionManager *extensions.Manager

	if err := rootContainer.Resolve(&extensionManager); err != nil {
		// If we can't resolve the extension manager, we can't auto-install
		return false, nil
	}

	// Check if the unknown command matches any available extension namespace
	options := &extensions.ListOptions{}
	registryExtensions, err := extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		// If we can't list registry extensions, we can't auto-install
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
		return false, nil
	}

	// Check if the extension is already installed
	_, err = extensionManager.GetInstalled(extensions.LookupOptions{
		Id: matchingExtension.Id,
	})
	if err == nil {
		// Extension is already installed, this shouldn't happen but let's be safe
		return false, nil
	}

	// Ask user for permission to auto-install the extension
	fmt.Printf("Command '%s' not found, but there's an available extension that provides it.\n", unknownCommand)
	fmt.Printf("Extension: %s (%s)\n", matchingExtension.DisplayName, matchingExtension.Description)
	fmt.Printf("Would you like to install it? (y/N): ")

	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "y" && response != "yes" {
		return false, nil
	}

	// Install the extension
	fmt.Printf("Installing extension '%s'...\n", matchingExtension.Id)
	filterOptions := &extensions.FilterOptions{}
	_, err = extensionManager.Install(ctx, matchingExtension.Id, filterOptions)
	if err != nil {
		return false, fmt.Errorf("failed to install extension: %w", err)
	}

	fmt.Printf("Extension '%s' installed successfully!\n", matchingExtension.Id)
	return true, nil
}

// ExecuteWithAutoInstall executes the command and handles auto-installation of extensions for unknown commands.
func ExecuteWithAutoInstall(ctx context.Context, rootContainer *ioc.NestedContainer, originalArgs []string) error {
	// First, try to execute the command normally
	rootCmd := NewRootCmd(false, nil, rootContainer)
	rootCmd.SetArgs(originalArgs)
	err := rootCmd.Execute()

	if err != nil {
		// Check if this is an "unknown command" error
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown command") && len(originalArgs) > 0 {
			// Extract the unknown command from the arguments
			unknownCommand := originalArgs[0]

			// Try to auto-install an extension for this command
			installed, installErr := tryAutoInstallExtension(ctx, rootContainer, unknownCommand, originalArgs)
			if installErr != nil {
				// If auto-install failed, return the original error
				return err
			}

			if installed {
				// Extension was installed, rebuild the command tree and try again
				rootCmd = NewRootCmd(false, nil, rootContainer)
				// Set the arguments again to execute the original command
				rootCmd.SetArgs(originalArgs)
				return rootCmd.Execute()
			}
		}

		// Return the original error if we couldn't auto-install
		return err
	}

	return nil
}
