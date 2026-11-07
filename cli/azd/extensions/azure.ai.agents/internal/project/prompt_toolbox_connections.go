// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type toolboxConnectionsClient interface {
	ProjectEndpoint(context.Context) (string, error)
	GetConnection(context.Context, string) (*azure.ToolboxConnectionProperties, error)
	PutConnection(context.Context, string, azure.ToolboxConnectionProperties) error
}

type foundryToolboxConnections struct {
	client        toolboxConnectionsClient
	endpoint      string
	explicitNames map[string]bool
}

func (c *foundryToolboxConnections) VerifyProject(ctx context.Context) error {
	endpoint, err := c.client.ProjectEndpoint(ctx)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetFoundryProject)
	}
	if strings.TrimSpace(endpoint) == "" || !sameProjectEndpoint(endpoint, c.endpoint) {
		return exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"the ARM project's advertised endpoint does not match the agent's Foundry project endpoint",
			"set FOUNDRY_PROJECT_ENDPOINT and AZURE_AI_PROJECT_ID to the same Foundry project")
	}
	return nil
}

func (c *foundryToolboxConnections) EnsureConnection(ctx context.Context, attachment toolboxAttachment) (string, error) {
	desired := azure.ToolboxConnectionProperties{
		Category: "RemoteTool", AuthType: "UserEntraToken", Audience: "https://ai.azure.com",
		Target: attachment.endpoint,
		Metadata: map[string]string{
			"type": "toolbox_MCP", "toolboxName": attachment.name, "toolboxVersion": attachment.version,
		},
	}
	// Legacy names are reuse-only. All new connections are endpoint-scoped and
	// existing connection targets are never changed, even when publishing an agent fails.
	legacy := attachment.name + "-mcp"
	if len(legacy) <= 33 {
		existing, err := c.client.GetConnection(ctx, legacy)
		if err != nil {
			return "", exterrors.ServiceFromAzure(err, exterrors.OpEnsureToolboxConnection)
		}
		if compatibleToolboxConnection(existing, desired) {
			return legacy, nil
		}
	}
	name := toolboxVersionConnectionName(attachment.name, attachment.version, attachment.endpoint)
	existing, err := c.client.GetConnection(ctx, name)
	if err != nil {
		return "", exterrors.ServiceFromAzure(err, exterrors.OpEnsureToolboxConnection)
	}
	if existing != nil {
		if compatibleToolboxConnection(existing, desired) {
			return name, nil
		}
		return "", exterrors.Validation(exterrors.CodeInvalidConnection,
			fmt.Sprintf("project connection %q already exists with incompatible target or authentication settings", name),
			"rename the conflicting connection or use a compatible connection; azd will not overwrite it")
	}
	if c.explicitNames[strings.ToLower(name)] {
		return "", exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("connection %q is explicitly declared in azure.yaml", name),
			"deploy the explicitly declared connection with compatible target and authentication settings first")
	}
	if err := c.client.PutConnection(ctx, name, desired); err != nil {
		return "", exterrors.ServiceFromAzure(err, exterrors.OpEnsureToolboxConnection)
	}
	return name, nil
}

func compatibleToolboxConnection(existing *azure.ToolboxConnectionProperties, desired azure.ToolboxConnectionProperties) bool {
	return existing != nil && strings.TrimSpace(existing.Target) == desired.Target &&
		strings.EqualFold(existing.Category, desired.Category) &&
		strings.EqualFold(existing.AuthType, desired.AuthType) &&
		strings.EqualFold(strings.TrimRight(existing.Audience, "/"), desired.Audience)
}

func toolboxVersionConnectionName(name, version, endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	prefix := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name+"-v"+version)
	prefix = strings.TrimLeft(prefix, "-_")
	return fmt.Sprintf("%s-%x-mcp", prefix[:min(len(prefix), 20)], sum[:4])
}

func newFoundryToolboxConnections(
	g *promptGraph, settings *PromptAgentSettings, credential azcore.TokenCredential,
) (toolboxConnectionEnsurer, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to configure toolbox connections", "run 'azd provision'")
	}
	projectID, err := toolboxProjectResourceID(g.env, settings)
	if err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, exterrors.Auth(exterrors.CodeCredentialCreationFailed,
			"an Azure credential is required to configure toolbox connections", "run 'azd auth login'")
	}
	explicitNames := map[string]bool{}
	identities, err := foundryConnectionServiceIdentities(g.projectServices, g.projectRoot)
	if err != nil {
		return nil, err
	}
	for _, identity := range identities {
		explicitNames[strings.ToLower(identity.ResourceName)] = true
	}
	client, err := azure.NewFoundryToolboxConnectionsClient(projectID, credential, nil)
	if err != nil {
		return nil, err
	}
	return &foundryToolboxConnections{client: client, endpoint: settings.ProjectEndpoint, explicitNames: explicitNames}, nil
}

func toolboxProjectResourceID(env map[string]string, settings *PromptAgentSettings) (string, error) {
	projectID := strings.TrimSpace(env["AZURE_AI_PROJECT_ID"])
	if projectID == "" {
		// Preserve endpoint-only environments; VerifyProject still checks the
		// discovered ARM resource's advertised endpoint before any connection I/O.
		host, project := foundryProjectIdentity(settings.ProjectEndpoint)
		account := strings.TrimSuffix(host, ".services.ai.azure.com")
		if value := strings.TrimSpace(env["AZURE_AI_ACCOUNT_NAME"]); value != "" {
			account = value
		}
		if value := strings.TrimSpace(env["AZURE_AI_PROJECT_NAME"]); value != "" {
			project = value
		}
		projectID = fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
			settings.SubscriptionID, settings.ResourceGroup, account, project)
	}
	id, err := arm.ParseResourceID(projectID)
	if err != nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return "", exterrors.Validation(exterrors.CodeInvalidAiProjectId,
			"a valid Foundry project ARM resource ID is required for toolbox connections", "set AZURE_AI_PROJECT_ID")
	}
	_, endpointProject := foundryProjectIdentity(settings.ProjectEndpoint)
	if !strings.EqualFold(endpointProject, id.Name) {
		return "", exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"Foundry project endpoint and ARM target name different projects",
			"set FOUNDRY_PROJECT_ENDPOINT and AZURE_AI_PROJECT_ID to the same Foundry project")
	}
	return id.String(), nil
}
