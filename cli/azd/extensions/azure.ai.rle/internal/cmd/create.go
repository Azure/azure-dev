// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
	recipe  string
	image   string
	version string
}

func newCreateCommand() *cobra.Command {
	flags := &createFlags{
		sharedFlags: sharedFlags{
			account: defaultAccountName,
			project: defaultProjectName,
		},
		recipe:  defaultRecipeName,
		version: "1.0.0",
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create or update an RLE environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			environmentId := slug(name)
			image, err := resolveRecipeImage(flags.recipe, flags.image)
			if err != nil {
				return err
			}

			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			environment, err := client.createOrUpdateEnvironment(
				cmd.Context(),
				flags.account,
				flags.project,
				environmentId,
				newEnvironmentCreateRequest(environmentId, name, image, flags.version),
			)
			if err != nil {
				return serviceError(err)
			}

			if isJsonOutput(cmd) {
				return printJson(cmd, environment)
			}

			return printEnvironmentCreated(cmd, environment, flags)
		},
	}

	addSharedFlags(cmd, &flags.sharedFlags)
	cmd.Flags().StringVar(&flags.recipe, "recipe", flags.recipe, "Recipe to use for the RLE environment")
	cmd.Flags().StringVar(&flags.image, "image", "", "Container image for the RLE environment. Overrides --recipe")
	cmd.Flags().StringVar(&flags.version, "version-label", flags.version, "Environment version label")
	return cmd
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

func printEnvironmentCreated(cmd *cobra.Command, environment *environmentResource, flags *createFlags) error {
	nextCommand := nextSandboxCreateCommand(environment.Id, flags.sharedFlags)
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Environment created: %s\nName: %s\nImage: %s\n\nNext:\n  %s\n",
		environment.Id,
		environment.Name,
		environment.Manifest.Runtime.Image,
		nextCommand,
	)
	return err
}

func nextSandboxCreateCommand(environmentId string, flags sharedFlags) string {
	command := fmt.Sprintf("azd ai rle sandbox create %s --wait", environmentId)
	command = appendNonDefaultSharedFlags(command, flags)
	return command
}

func appendNonDefaultSharedFlags(command string, flags sharedFlags) string {
	if flags.project != "" && flags.project != defaultProjectName {
		command += fmt.Sprintf(" --project %s", flags.project)
	}
	if flags.account != "" && flags.account != defaultAccountName {
		command += fmt.Sprintf(" --account %s", flags.account)
	}
	if flags.endpoint != "" {
		command += fmt.Sprintf(" --endpoint %s", flags.endpoint)
	}
	return command
}
