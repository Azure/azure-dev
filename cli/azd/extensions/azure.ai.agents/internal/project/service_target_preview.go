// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ azdext.ServiceTargetPreviewProvider = (*AgentServiceTargetProvider)(nil)

// Preview compares agent configuration without initializing the deployment
// lifecycle, running hooks, building artifacts, or saving deployment state.
func (p *AgentServiceTargetProvider) Preview(
	ctx context.Context, serviceConfig *azdext.ServiceConfig,
) (*azdext.ServiceDeployPreviewResult, error) {
	options, err := loadServicePreviewOptions(ctx, p.azdClient, serviceConfig)
	if err != nil {
		return nil, err
	}
	result, err := previewAgentService(ctx, options, func(endpoint string) (standaloneAgentReader, error) {
		tenantID := ""
		if subscription := options.Environment["AZURE_SUBSCRIPTION_ID"]; subscription != "" {
			tenant, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscription})
			if err != nil {
				return nil, exterrors.FromHost(err, exterrors.CodeTenantLookupFailed, "resolving the user access tenant")
			}
			if tenant == nil || tenant.TenantId == "" {
				return nil, fmt.Errorf("azd returned an empty user access tenant")
			}
			tenantID = tenant.TenantId
		}
		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID: tenantID, AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, exterrors.Auth(exterrors.CodeCredentialCreationFailed,
				fmt.Sprintf("failed to create Azure credential: %s", err), "run 'azd auth login' to authenticate")
		}
		return agent_api.NewAgentClientWithOptions(endpoint, credential, nil), nil
	})
	if err != nil {
		return nil, err
	}
	return servicePreviewResponse(result)
}

func servicePreviewResponse(result *DirectDeployPreviewResult) (*azdext.ServiceDeployPreviewResult, error) {
	if result == nil {
		return nil, fmt.Errorf("agent preview result must not be nil")
	}
	var message bytes.Buffer
	if err := WriteDeploymentPreview(&message, result); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("serialize agent preview: %w", err)
	}
	data := &structpb.Struct{}
	if err := protojson.Unmarshal(encoded, data); err != nil {
		return nil, fmt.Errorf("encode agent preview result: %w", err)
	}
	return &azdext.ServiceDeployPreviewResult{Message: message.String(), Data: data}, nil
}
