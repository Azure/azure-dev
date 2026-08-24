// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// provisionActivityBotNames resolves and persists Activity Bot names during
// provision when the deployment scope is already known. Deploy resolves the
// final bot name again using the deployed agent identity as the source of truth.
func shouldProvisionActivityBot(service *azdext.ServiceConfig, projectRoot string) (bool, error) {
	profile, err := resolveServiceActivityProfile(service, projectRoot)
	if err != nil {
		return false, err
	}
	if !profile.IsActivity {
		return false, nil
	}
	return profile.UseCase == project.ActivityUseCaseSimple, nil
}

func provisionActivityBotNames(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	environmentName string,
	args *azdext.ProjectEventArgs,
) error {
	envName, err := resolveActivityEnvironmentName(ctx, azdClient, environmentName)
	if err != nil {
		return err
	}

	valuesResp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return fmt.Errorf("getting environment values: %w", err)
	}
	values := make(map[string]string, len(valuesResp.KeyValues))
	for _, pair := range valuesResp.KeyValues {
		values[pair.Key] = pair.Value
	}

	for _, service := range args.Project.Services {
		if service.GetHost() != AiAgentHost {
			continue
		}
		shouldProvision, err := shouldProvisionActivityBot(service, args.Project.Path)
		if err != nil {
			return fmt.Errorf("resolving activity bot provisioning for service %q: %w", service.Name, err)
		}
		if !shouldProvision {
			continue
		}

		agent, hosted, _, err := project.LoadAgentDefinition(service, args.Project.Path)
		if err != nil || !hosted {
			continue
		}

		key := envkey.AgentBotName(service.Name)
		if strings.TrimSpace(values[key]) != "" {
			continue
		}

		subscriptionID := strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"])
		resourceGroup := strings.TrimSpace(values["AZURE_RESOURCE_GROUP"])
		if subscriptionID == "" || resourceGroup == "" {
			continue
		}

		botName := strings.TrimSpace(botservice.BotName(agent.Name, botservice.BotScopeSalt(subscriptionID, resourceGroup)))
		if botName == "" {
			return fmt.Errorf("%s is required", key)
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

func resolveActivityEnvironmentName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	environmentName string,
) (string, error) {
	if envName := strings.TrimSpace(environmentName); envName != "" {
		return envName, nil
	}

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", missingActivityEnvironmentError(err)
	}
	if envResp == nil || envResp.Environment == nil || strings.TrimSpace(envResp.Environment.Name) == "" {
		return "", missingActivityEnvironmentError(nil)
	}
	return strings.TrimSpace(envResp.Environment.Name), nil
}

func missingActivityEnvironmentError(cause error) error {
	return exterrors.MissingInputValidation(
		exterrors.CodeEnvironmentNotFound,
		"no azd environment is selected for activity bot provisioning",
		exterrors.RequiredInput{
			Name:        "azd environment",
			Description: "Select the environment used by this provision command.",
			Sources: []exterrors.InputSource{
				{
					Kind:         exterrors.InputSourceFlag,
					Name:         "-e/--environment",
					ExampleValue: "dev",
					Example:      "azd -e dev provision",
				},
				{
					Kind:         exterrors.InputSourceEnvironment,
					Name:         "AZD_ENVIRONMENT",
					ExampleValue: "dev",
					Example:      `$env:AZD_ENVIRONMENT = "dev"; azd provision`,
				},
				{
					Kind:         exterrors.InputSourceConfig,
					Name:         "azd env select <name>",
					ExampleValue: "dev",
					Example:      "azd env select dev; azd provision",
				},
			},
		},
	).WithCause(cause)
}
