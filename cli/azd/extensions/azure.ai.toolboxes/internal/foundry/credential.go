// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundry holds Foundry data-plane primitives that this extension
// duplicates from azure.ai.agents. The shared package is decoupled from
// toolbox-specific concepts so that a future lift-out into a shared module
// is mechanical. See cli/azd/docs/design/azure-ai-toolbox-direct-commands.md
// § 3 for the duplication contract.
package foundry

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// NewCredential returns the credential used by Foundry data-plane clients.
//
// This is the toolboxes-extension copy of azure.ai.agents' newAgentCredential.
func NewCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return cred, nil
}
