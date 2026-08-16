// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"
	"strings"

	"azureaidataset/internal/foundry/projectctx"
	"azureaidataset/internal/messages"
	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectEndpointEnvKey is the azd environment key holding the Foundry project
// endpoint the data-plane clients target.
const projectEndpointEnvKey = "FOUNDRY_PROJECT_ENDPOINT"

// datasetContext carries everything the commands need to reach the data plane.
type datasetContext struct {
	azdClient *azdext.AzdClient
	endpoint  string
	envName   string
	cred      azcore.TokenCredential

	datasetClient *dataset_api.DatasetClient
}

// newDatasetContext resolves the project endpoint and builds the data-plane
// clients. The resolution order is projectctx's, so that every Foundry
// extension answers the same question the same way:
//
//  1. --project-endpoint
//  2. the active azd environment (FOUNDRY_PROJECT_ENDPOINT, then AZURE_AI_PROJECT_ENDPOINT)
//  3. global config: extensions.ai-agents.project.context.endpoint
//  4. the host environment variables of the same two names
//  5. otherwise an error naming how to set one
func newDatasetContext(ctx context.Context, endpointFlag string) (*datasetContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, messages.ConnectingToAzd(err)
	}

	dc := &datasetContext{azdClient: azdClient}

	// The environment name is resolved regardless of where the endpoint came
	// from: it is what cached version numbers are read from and written to.
	_, envName := lookupEndpointFromAzd(ctx, azdClient)
	dc.envName = envName

	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: endpointFlag})
	if err != nil {
		// The caller only defers Close on a context it was handed, so every
		// path that abandons this one has to close it here.
		dc.Close()
		return nil, err
	}
	dc.endpoint = strings.TrimSuffix(resolved.Endpoint, "/")
	log.Printf("[endpoint] resolved from %s", resolved.Source)

	cred, err := newAzdTokenCredential()
	if err != nil {
		dc.Close()
		return nil, err
	}
	dc.cred = cred

	dc.datasetClient = dataset_api.NewDatasetClient(dc.endpoint, cred)

	return dc, nil
}

// newAzdTokenCredential returns the azd credential already wrapped in its
// retry. Handing back the wrapper rather than the raw credential is what keeps
// the retry wired: in the sibling extension an earlier version assigned the
// wrapper to the context and then built its clients from the unwrapped one, so
// nothing retried and four tests still passed.
func newAzdTokenCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, messages.CreatingCredential(err)
	}
	return azdTokenRetry{inner: cred}, nil
}

// azdTokenRetry retries a failed token request once. azidentity gives the azd
// subprocess a fixed 10 second timeout and discards its stderr, so an azd that
// overruns surfaces as "exit status 1" with no cause; the next call usually
// finds a warm token. Without this a slow token turns into a failed command.
type azdTokenRetry struct{ inner azcore.TokenCredential }

func (c azdTokenRetry) GetToken(
	ctx context.Context,
	opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	tok, err := c.inner.GetToken(ctx, opts)
	if err == nil || ctx.Err() != nil {
		return tok, err
	}
	log.Printf("[auth] token request failed (%v); retrying once", err)
	return c.inner.GetToken(ctx, opts)
}

// lookupEndpointFromAzd reads the endpoint from the active azd environment,
// returning empty strings when azd has no current environment.
func lookupEndpointFromAzd(ctx context.Context, azdClient *azdext.AzdClient) (endpoint, envName string) {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil || envResp.Environment == nil {
		return "", ""
	}
	val, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     projectEndpointEnvKey,
	})
	if err != nil || val == nil || val.Value == "" {
		return "", envResp.Environment.Name
	}
	return val.Value, envResp.Environment.Name
}

// errNoAzdEnvironment reports that there is no azd environment to persist into.
//
// These commands work standalone against the data plane, so running outside a
// project is ordinary rather than a problem worth reporting.
var errNoAzdEnvironment = messages.ErrNoAzdEnvironment

// setEnvValue persists a value into the active azd environment.
func (dc *datasetContext) setEnvValue(ctx context.Context, key, value string) error {
	if dc.envName == "" {
		envResp, err := dc.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || envResp == nil || envResp.Environment == nil {
			return messages.NoAzdEnvironmentToWrite(key)
		}
		dc.envName = envResp.Environment.Name
	}
	_, err := dc.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: dc.envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return messages.WritingEnvValue(key, err)
	}
	return nil
}

func (dc *datasetContext) Close() {
	if dc.azdClient != nil {
		dc.azdClient.Close()
	}
}
