// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// DirectDeployOptions supplies the inputs for previewing a single agent definition.
type DirectDeployOptions struct {
	DefinitionPath    string
	CodePath          string
	ProjectEndpoint   string
	Environment       map[string]string
	PreviewDefinition *AgentPreviewDefinition
}

func resolveAgentCodePath(codePath string) (string, error) {
	absolutePath, err := filepath.Abs(codePath)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("invalid agent code path %q: %s", codePath, err),
			"configure a valid source directory in the agent service",
		)
	}
	info, err := os.Stat(absolutePath)
	if err != nil || !info.IsDir() {
		return "", exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("agent code directory %q was not found", absolutePath),
			"configure a readable source directory in the agent service",
		)
	}
	return absolutePath, nil
}

func reconcileStandaloneEndpointWithDeployedAgent(
	request *agent_api.CreateAgentRequest,
	localProfile ActivityProfile,
	existingAgent *agent_api.AgentObject,
) error {
	resolvedProfile, err := ResolveDeployedActivityProfile(localProfile, existingAgent.DigitalWorkerType)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			err.Error(),
			digitalWorkerTypeMismatchSuggestion(),
		)
	}
	EnsureActivityEndpointAuthSchemeForProfile(request.AgentEndpoint, resolvedProfile)
	return nil
}

func newStandaloneCredential() (azcore.TokenCredential, error) {
	azureCLI, azureCLIError := azidentity.NewAzureCLICredential(
		&azidentity.AzureCLICredentialOptions{AdditionallyAllowedTenants: []string{"*"}},
	)
	azd, azdError := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{AdditionallyAllowedTenants: []string{"*"}},
	)
	credentials := make([]azcore.TokenCredential, 0, 2)
	if azureCLIError == nil {
		credentials = append(credentials, azureCLI)
	}
	if azdError == nil {
		credentials = append(credentials, azd)
	}
	if len(credentials) == 0 {
		return nil, fmt.Errorf("azure CLI credential: %w; azd credential: %w", azureCLIError, azdError)
	}
	if len(credentials) == 1 {
		return credentials[0], nil
	}
	credential, err := azidentity.NewChainedTokenCredential(credentials, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential chain: %w", err)
	}
	return credential, nil
}
