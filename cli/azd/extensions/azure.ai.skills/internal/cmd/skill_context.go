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

type skillContextOptions struct {
	flagEndpoint     string
	environmentName  string
	outputFormat     string
	noPrompt         bool
	progressReporter func(string)
}

var prepareDeveloperAccess = foundry.PrepareDeveloperAccess

func resolveSkillContext(ctx context.Context, flagEndpoint string, environmentName ...string) (*skillContext, error) {
	envName := ""
	if len(environmentName) > 0 {
		envName = environmentName[0]
	}
	return resolveSkillContextWithOptions(ctx, skillContextOptions{
		flagEndpoint:    flagEndpoint,
		environmentName: envName,
	})
}

func resolveSkillContextWithOptions(ctx context.Context, opts skillContextOptions) (*skillContext, error) {
	endpoint, src, err := resolveProjectEndpoint(ctx, opts.flagEndpoint)
	if err != nil {
		return nil, err
	}
	var warn func(string)
	switch {
	case opts.progressReporter != nil:
		warn = func(message string) {
			opts.progressReporter("Warning: " + message)
		}
	case !shouldSuppressWarning(opts.noPrompt, opts.outputFormat):
		warn = func(message string) {
			fmt.Fprintln(os.Stderr, "Warning: "+message)
		}
	}
	cred, err := prepareDeveloperAccess(ctx, foundry.PrepareDeveloperAccessOptions{
		Endpoint:        endpoint,
		EnvironmentName: opts.environmentName,
		Warn:            warn,
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
