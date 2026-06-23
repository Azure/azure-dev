// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type sandboxFlags struct {
	sharedFlags
	version string
	cpu     string
	memory  string
	disk    string
}

func newSandboxCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox <command>",
		Short: "Manage RLE environment sandboxes",
	}

	cmd.AddCommand(newSandboxCreateCommand())
	cmd.AddCommand(newSandboxListCommand())
	cmd.AddCommand(newSandboxShowCommand())
	return cmd
}

func newSandboxCreateCommand() *cobra.Command {
	flags := &sandboxFlags{
		sharedFlags: sharedFlags{
			account: defaultAccountName,
			project: defaultProjectName,
		},
	}

	cmd := &cobra.Command{
		Use:   "create <environment-id>",
		Short: "Create an RLE sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			sandbox, err := client.createSandbox(
				cmd.Context(),
				flags.account,
				flags.project,
				args[0],
				sandboxCreateRequest{
					Version: flags.version,
					Cpu:     flags.cpu,
					Memory:  flags.memory,
					Disk:    flags.disk,
				},
			)
			if err != nil {
				return sandboxCreateError(err)
			}

			return printJson(cmd, sandbox)
		},
	}

	addSharedFlags(cmd, &flags.sharedFlags)
	cmd.Flags().StringVar(&flags.version, "version-label", "", "Environment version to sandbox. Defaults to latest")
	cmd.Flags().StringVar(&flags.cpu, "cpu", "", "Sandbox CPU request. Defaults to server configuration")
	cmd.Flags().StringVar(&flags.memory, "memory", "", "Sandbox memory request. Defaults to server configuration")
	cmd.Flags().StringVar(&flags.disk, "disk", "", "Sandbox disk request. Defaults to server configuration")
	return cmd
}

func newSandboxListCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "list <environment-id>",
		Short: "List RLE sandboxes for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			sandboxes, err := client.listSandboxes(cmd.Context(), flags.account, flags.project, args[0])
			if err != nil {
				return serviceError(err)
			}

			return printJson(cmd, sandboxes)
		},
	}

	addSharedFlags(cmd, flags)
	return cmd
}

func newSandboxShowCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "show <environment-id> <sandbox-id>",
		Short: "Show an RLE sandbox",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			sandbox, err := client.getSandbox(cmd.Context(), flags.account, flags.project, args[0], args[1])
			if err != nil {
				return serviceError(err)
			}

			return printJson(cmd, sandbox)
		},
	}

	addSharedFlags(cmd, flags)
	return cmd
}

func sandboxCreateError(err error) error {
	var httpErr *rleHTTPError
	if errors.As(err, &httpErr) && httpErr.statusCode == 409 {
		message := extractRleErrorMessage(httpErr.body)
		return &azdext.LocalError{
			Message:    message,
			Code:       "rle_sandbox_not_ready",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "The server creates the disk image asynchronously after environment creation. Retry sandbox create after conversion is ready; if the status is Failed, recreate/update the environment with a valid ACR image.",
		}
	}

	return serviceError(err)
}

func extractRleErrorMessage(body string) string {
	var payload struct {
		Error any `json:"error"`
	}

	if err := json.Unmarshal([]byte(body), &payload); err == nil && payload.Error != nil {
		switch value := payload.Error.(type) {
		case string:
			return value
		case map[string]any:
			if message, ok := value["message"].(string); ok && message != "" {
				return message
			}
		}
	}

	return strings.TrimSpace(body)
}

func printJson(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}
