// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type sharedFlags struct {
	endpoint string
	account  string
	project  string
}

type createFlags struct {
	sharedFlags
	image   string
	version string
}

func newCreateCommand() *cobra.Command {
	flags := &createFlags{
		sharedFlags: sharedFlags{
			account: defaultAccountName,
			project: defaultProjectName,
		},
		version: "1.0.0",
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create or update an RLE environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			environmentId := slug(name)
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			environment, err := client.createOrUpdateEnvironment(
				cmd.Context(),
				flags.account,
				flags.project,
				environmentId,
				newManifest(environmentId, name, flags.image, flags.version),
			)
			if err != nil {
				return serviceError(err)
			}

			encoded, err := json.MarshalIndent(environment, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return err
		},
	}

	addSharedFlags(cmd, &flags.sharedFlags)
	cmd.Flags().StringVar(&flags.image, "image", "", "Container image for the RLE environment")
	cmd.Flags().StringVar(&flags.version, "version-label", flags.version, "Environment version label")
	return cmd
}

func notImplementedError(commandName string, resourceName string) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("azd ai rle %s is not implemented yet for %q.", commandName, resourceName),
		Code:       fmt.Sprintf("%s_not_implemented", commandName),
		Category:   azdext.LocalErrorCategoryCompatibility,
		Suggestion: "Add the RLE service workflow for this command, then try again.",
	}
}

func serviceError(err error) error {
	return &azdext.ServiceError{
		Message:     err.Error(),
		ServiceName: "rle-control-plane",
		Suggestion: fmt.Sprintf(
			"Ensure the RLE control plane is running and reachable, e.g. %s.",
			defaultControlPlaneEndpoint,
		),
	}
}

func addSharedFlags(cmd *cobra.Command, flags *sharedFlags) {
	cmd.Flags().StringVar(
		&flags.endpoint,
		"endpoint",
		"",
		fmt.Sprintf(
			"RLE control plane endpoint. Defaults to AZD_RLE_CONTROL_PLANE, RLE_CONTROL_PLANE, or %s",
			defaultControlPlaneEndpoint,
		),
	)
	cmd.Flags().StringVar(&flags.account, "account", flags.account, "RLE account name")
	cmd.Flags().StringVar(&flags.project, "project", flags.project, "RLE project name")
}

func slug(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
