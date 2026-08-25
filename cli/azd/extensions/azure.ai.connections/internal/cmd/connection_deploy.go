// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/spf13/cobra"
)

func newConnectionDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy a local connection definition.",
		Long: `Deploy a connection definition to the configured Foundry project.

The path defaults to ./connection.yaml. Deploy creates the connection when it
does not exist and replaces its definition when it does. It never provisions a
Foundry project.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := definition.DefaultPath
			if len(args) == 1 {
				path = args[0]
			}
			flags, err := connectionDeployFlags(path, extCtx.OutputFormat)
			if err != nil {
				return err
			}
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return (&ConnectionCreateAction{flags: flags}).Run(azdext.WithAccessToken(cmd.Context()))
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func connectionDeployFlags(path, output string) (*connectionCreateFlags, error) {
	input, err := definition.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, exterrors.Dependency(
				exterrors.CodeInvalidFromFile,
				fmt.Sprintf("connection definition %q was not found", path),
				"run the command from a directory containing connection.yaml or pass an explicit path",
			)
		}
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFromFile,
			fmt.Sprintf("connection definition %q is invalid: %s", path, err),
			"fix the connection definition and retry",
		)
	}

	expand := func(field, value string) (string, error) {
		expanded, err := foundry.ExpandEnv(value, os.Getenv)
		if err != nil {
			return "", exterrors.Validation(
				exterrors.CodeInvalidFromFile,
				fmt.Sprintf("failed to resolve %s in connection definition %q: %s", field, path, err),
				"fix the environment-variable expression and retry",
			)
		}
		return expanded, nil
	}

	target, err := expand("target", input.Target)
	if err != nil {
		return nil, err
	}
	audience, err := expand("audience", input.Audience)
	if err != nil {
		return nil, err
	}
	authorizationURL, err := expand("authorizationUrl", input.AuthorizationURL)
	if err != nil {
		return nil, err
	}
	tokenURL, err := expand("tokenUrl", input.TokenURL)
	if err != nil {
		return nil, err
	}
	refreshURL, err := expand("refreshUrl", input.RefreshURL)
	if err != nil {
		return nil, err
	}
	connectorName, err := expand("connectorName", input.ConnectorName)
	if err != nil {
		return nil, err
	}

	flags := &connectionCreateFlags{
		name:             strings.TrimSpace(input.Name),
		kind:             strings.TrimSpace(input.Category),
		target:           strings.TrimSpace(target),
		authType:         normalizeAuthType(strings.TrimSpace(input.AuthType)),
		force:            true,
		output:           output,
		audience:         strings.TrimSpace(audience),
		authorizationURL: strings.TrimSpace(authorizationURL),
		tokenURL:         strings.TrimSpace(tokenURL),
		refreshURL:       strings.TrimSpace(refreshURL),
		scopes:           slices.Clone(input.Scopes),
		connectorName:    strings.TrimSpace(connectorName),
	}
	if flags.authType == "" {
		flags.authType = "none"
	}
	if flags.name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			fmt.Sprintf("connection definition %q does not declare a name", path),
			"set 'name' in the connection definition and retry",
		)
	}

	metadataKeys := make([]string, 0, len(input.Metadata))
	for key := range input.Metadata {
		metadataKeys = append(metadataKeys, key)
	}
	slices.Sort(metadataKeys)
	for _, key := range metadataKeys {
		value, err := expand("metadata."+key, input.Metadata[key])
		if err != nil {
			return nil, err
		}
		flags.metadata = append(flags.metadata, key+"="+value)
	}

	credentialKeys := make([]string, 0, len(input.Credentials))
	for key := range input.Credentials {
		credentialKeys = append(credentialKeys, key)
	}
	slices.Sort(credentialKeys)
	for _, key := range credentialKeys {
		value, ok := input.Credentials[key].(string)
		if !ok {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidFromFile,
				fmt.Sprintf("connection credential %q in %q must be a string", key, path),
				"use string credential values or environment-variable references",
			)
		}
		value, err = expand("credentials."+key, value)
		if err != nil {
			return nil, err
		}
		switch flags.authType {
		case "api-key":
			if key == "key" {
				flags.key = value
			}
		case "oauth2":
			switch key {
			case "clientId":
				flags.clientID = value
			case "clientSecret":
				flags.clientSecret = value
			}
		default:
			flags.customKeys = append(flags.customKeys, key+"="+value)
		}
	}

	return flags, nil
}
