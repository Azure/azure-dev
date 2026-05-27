// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiskills/internal/exterrors"
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

func resolveSkillContext(ctx context.Context, flagEndpoint string) (*skillContext, error) {
	endpoint, src, err := resolveProjectEndpoint(ctx, flagEndpoint)
	if err != nil {
		return nil, err
	}
	cred, err := newCredential()
	if err != nil {
		return nil, err
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
