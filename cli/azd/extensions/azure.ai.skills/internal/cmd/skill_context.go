// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/foundry"
	"azureaiskills/internal/pkg/skill_api"
	"azureaiskills/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type skillContext struct {
	client   *skill_api.Client
	endpoint string
	source   endpointSource
}

var prepareDeveloperAccess = foundry.PrepareDeveloperAccess

func resolveSkillContext(ctx context.Context, flagEndpoint string, environmentName ...string) (*skillContext, error) {
	endpoint, src, err := resolveProjectEndpoint(ctx, flagEndpoint)
	if err != nil {
		return nil, err
	}
	envName := ""
	if len(environmentName) > 0 {
		envName = environmentName[0]
	}
	cred, err := prepareDeveloperAccess(ctx, foundry.PrepareDeveloperAccessOptions{
		Endpoint:        endpoint,
		EnvironmentName: envName,
		Warn: func(message string) {
			fmt.Fprintln(os.Stderr, "Warning: "+message)
		},
	})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		cred, err = newCredential()
		if err != nil {
			return nil, err
		}
	}
	return &skillContext{
		client:   skill_api.NewClient(endpoint, cred, version.Version),
		endpoint: endpoint,
		source:   src,
	}, nil
}

func newCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run `azd auth login` to authenticate",
		)
	}
	return cred, nil
}
