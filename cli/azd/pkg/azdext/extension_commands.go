// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// NewListenCommand creates the standard "listen" command for lifecycle event extensions.
// The configure function receives an ExtensionHost to register service targets,
// framework services, and event handlers before the host starts.
// If configure is nil, the host runs with no custom registrations.
func NewListenCommand(configure func(host *ExtensionHost)) *cobra.Command {
	return &cobra.Command{
		Use:    "listen",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := WithAccessToken(cmd.Context())

			client, err := NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer client.Close()

			host := NewExtensionHost(client)
			if configure != nil {
				configure(host)
			}

			return host.Run(ctx)
		},
	}
}

// NewMetadataCommand creates the standard "metadata" command that outputs
// extension command metadata for IntelliSense/discovery.
// rootCmdProvider returns the root command to introspect.
func NewMetadataCommand(schemaVersion, extensionId string, rootCmdProvider func() *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "metadata",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := rootCmdProvider()
			metadata := GenerateExtensionMetadata(schemaVersion, extensionId, root)

			jsonBytes, err := json.MarshalIndent(metadata, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal metadata: %w", err)
			}

			fmt.Println(string(jsonBytes))
			return nil
		},
	}
}

// NewVersionCommand creates the standard "version" command.
// outputFormat is a pointer to the output format string (for JSON support).
func NewVersionCommand(extensionId, version string, outputFormat *string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display the extension version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if outputFormat != nil && *outputFormat == "json" {
				versionInfo := map[string]string{
					"name":    extensionId,
					"version": version,
				}

				jsonBytes, err := json.MarshalIndent(versionInfo, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal version info: %w", err)
				}

				fmt.Println(string(jsonBytes))
			} else {
				fmt.Printf("%s %s\n", extensionId, version)
			}

			return nil
		},
	}
}
