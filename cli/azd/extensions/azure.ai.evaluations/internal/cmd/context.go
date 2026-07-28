// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectEndpointEnvKey is the azd environment key holding the Foundry project
// endpoint the data-plane clients target.
const projectEndpointEnvKey = "FOUNDRY_PROJECT_ENDPOINT"

// evalContext carries everything the commands need to reach the data plane.
type evalContext struct {
	azdClient *azdext.AzdClient
	endpoint  string
	envName   string
	cred      azcore.TokenCredential

	evalClient    *eval_api.EvalClient
	datasetClient *dataset_api.DatasetClient
}

// newEvalContext resolves the project endpoint and builds the data-plane
// clients. Endpoint resolution order:
//
//  1. --project-endpoint
//  2. the active azd environment's FOUNDRY_PROJECT_ENDPOINT
//  3. the host environment variable of the same name
func newEvalContext(ctx context.Context, endpointFlag string) (*evalContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("connecting to azd: %w", err)
	}

	ec := &evalContext{azdClient: azdClient}

	// The environment name is resolved regardless of where the endpoint comes
	// from: it is what the cached eval group and run ids are read from and
	// written to. Deriving it only when the endpoint came from azd meant
	// --project-endpoint silently disabled that cache.
	azdEndpoint, envName := lookupEndpointFromAzd(ctx, azdClient)
	ec.envName = envName

	if endpointFlag != "" {
		ec.endpoint = endpointFlag
	} else {
		ec.endpoint = azdEndpoint
	}
	if ec.endpoint == "" {
		ec.endpoint = os.Getenv(projectEndpointEnvKey)
	}
	if ec.endpoint == "" {
		return nil, fmt.Errorf(
			"no Foundry project endpoint found; pass --project-endpoint or set %s "+
				"in the azd environment (azd env set %s <url>)",
			projectEndpointEnvKey, projectEndpointEnvKey)
	}
	ec.endpoint = strings.TrimSuffix(ec.endpoint, "/")

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("creating Azure credential: %w", err)
	}
	ec.cred = cred

	ec.evalClient = eval_api.NewEvalClient(ec.endpoint, cred)
	ec.datasetClient = dataset_api.NewDatasetClient(ec.endpoint, cred)

	return ec, nil
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
// The atomic commands are meant to work standalone against the data plane, so
// running outside a project is ordinary rather than a problem worth reporting.
// A write that fails for any other reason still is.
var errNoAzdEnvironment = errors.New("no active azd environment")

// setEnvValue persists a value into the active azd environment. azd itself
// writes none of these keys — the extension owns them.
func (ec *evalContext) setEnvValue(ctx context.Context, key, value string) error {
	if ec.envName == "" {
		envResp, err := ec.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || envResp == nil || envResp.Environment == nil {
			return fmt.Errorf("%w to write %s into", errNoAzdEnvironment, key)
		}
		ec.envName = envResp.Environment.Name
	}
	_, err := ec.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: ec.envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("writing %s to the azd environment: %w", key, err)
	}
	return nil
}

// getEnvValue reads a value from the active azd environment, returning empty
// when it is unset.
func (ec *evalContext) getEnvValue(ctx context.Context, key string) string {
	if ec.envName == "" {
		return ""
	}
	val, err := ec.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: ec.envName,
		Key:     key,
	})
	if err != nil || val == nil {
		return ""
	}
	return val.Value
}

func (ec *evalContext) Close() {
	if ec.azdClient != nil {
		ec.azdClient.Close()
	}
}

// azd environment keys written by this extension.
const (
	envKeyEvalGroupID       = "EVAL_GROUP_ID"
	envKeyEvalRunID         = "EVAL_RUN_ID"
	envKeyDatasetVersion    = "EVAL_DATASET_VERSION"
	envKeyFingerprintPrefix = "EVAL_FINGERPRINT_"
)
