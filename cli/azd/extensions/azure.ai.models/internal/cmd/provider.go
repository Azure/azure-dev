// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"azure.ai.models/internal/client"
	"azure.ai.models/internal/fireworks"
	"azure.ai.models/internal/utils"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func newFireworksClient(ctx context.Context, projectEndpoint, connectionName string) (*fireworks.Client, error) {
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	foundryClient, err := client.NewFoundryClient(projectEndpoint, credential)
	if err != nil {
		return nil, err
	}
	connection, err := foundryClient.GetProviderConnection(ctx, connectionName)
	if err != nil {
		return nil, err
	}
	return fireworks.NewClient(connection.Target, connection.APIKey)
}

func runProviderList(ctx context.Context, parentFlags *customFlags, flags *customListFlags, out io.Writer) error {
	provider, err := newFireworksClient(ctx, parentFlags.projectEndpoint, flags.Connection)
	if err != nil {
		return err
	}
	models, err := provider.ListModels(ctx, flags.Account)
	if err != nil {
		return err
	}
	return utils.PrintObjectTo(out, models, utils.OutputFormat(flags.Output))
}
