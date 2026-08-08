// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// predeployActivityBotNames resolves and persists Activity Bot names before
// azd starts its dynamic deployment progress display. Prompts from a publish
// worker are otherwise overwritten by the repeatedly rendered progress table.
func predeployActivityBotNames(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
) error {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("getting current environment: %w", err)
	}
	if envResp == nil || envResp.Environment == nil || envResp.Environment.Name == "" {
		return errors.New("no current azd environment is selected")
	}
	envName := envResp.Environment.Name

	valuesResp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return fmt.Errorf("getting environment values: %w", err)
	}
	values := make(map[string]string, len(valuesResp.KeyValues))
	for _, pair := range valuesResp.KeyValues {
		values[pair.Key] = pair.Value
	}

	projectID, err := arm.ParseResourceID(values["AZURE_AI_PROJECT_ID"])
	if err != nil {
		return fmt.Errorf("parsing AZURE_AI_PROJECT_ID: %w", err)
	}
	endpoint := strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
	if endpoint == "" {
		return errors.New("FOUNDRY_PROJECT_ENDPOINT is required to prepare Activity Bot deployment")
	}
	subscriptionID := strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"])
	if subscriptionID == "" {
		return errors.New("AZURE_SUBSCRIPTION_ID is required to prepare Activity Bot deployment")
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   values["AZURE_TENANT_ID"],
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("creating Azure credential: %w", err)
	}
	agentClient := agent_api.NewAgentClient(endpoint, cred)

	for _, service := range args.Project.Services {
		if service.GetHost() != AiAgentHost {
			continue
		}
		agent, hosted, _, err := project.LoadAgentDefinition(service, args.Project.Path)
		if err != nil || !hosted || !project.ResolveActivityProfile(agent).IsActivity {
			continue
		}

		key := envkey.AgentBotName(service.Name)
		if strings.TrimSpace(values[key]) != "" {
			continue
		}

		defaultName := botservice.BotName(
			agent.Name,
			botservice.BotScopeSalt(subscriptionID, projectID.ResourceGroupName),
		)
		botName, err := predeployActivityBotName(
			ctx, azdClient, agentClient, cred, subscriptionID, key, agent.Name, defaultName,
		)
		if err != nil {
			return err
		}
		if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   botName,
		}); err != nil {
			return fmt.Errorf("saving Azure Bot name for service %q: %w", service.Name, err)
		}
		values[key] = botName
	}

	return nil
}

func predeployActivityBotName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentClient *agent_api.AgentClient,
	cred *azidentity.AzureDeveloperCLICredential,
	subscriptionID string,
	envKey string,
	agentName string,
	defaultName string,
) (string, error) {
	existingAgent, err := agentClient.GetAgent(ctx, agentName, agent_api.AgentEndpointAPIVersion)
	if err == nil {
		identityClientID := ""
		if existingAgent.InstanceIdentity != nil {
			identityClientID = strings.TrimSpace(existingAgent.InstanceIdentity.ClientID)
		}
		if identityClientID == "" && existingAgent.Versions.Latest.InstanceIdentity != nil {
			identityClientID = strings.TrimSpace(existingAgent.Versions.Latest.InstanceIdentity.ClientID)
		}
		if identityClientID != "" {
			botClient, clientErr := botservice.NewClient(subscriptionID, cred, nil)
			if clientErr != nil {
				return "", clientErr
			}
			bot, findErr := botClient.FindByMsaAppID(ctx, identityClientID)
			if findErr != nil {
				return "", findErr
			}
			if bot != nil {
				return bot.Name, nil
			}
		}
	} else if responseErr, ok := errors.AsType[*azcore.ResponseError](err); !ok || responseErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("checking existing agent %q: %w", agentName, err)
	}

	response, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Azure Bot name",
			HelpMessage:     "The Azure Bot resource used by this Activity agent.",
			DefaultValue:    defaultName,
			Required:        true,
			RequiredMessage: "Azure Bot name is required.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("prompting for Azure Bot name: %w", err)
	}
	name := strings.TrimSpace(response.Value)
	if name == "" {
		return "", fmt.Errorf("%s is required", envKey)
	}
	return name, nil
}
