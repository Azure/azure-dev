// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// provisionActivityBotNames resolves and persists Activity Bot names during
// provision. When the value is missing, it writes a deterministic default name
// so users can run provision/deploy without an extra prompt.
func provisionActivityBotNames(
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

		botName := strings.TrimSpace(botservice.DefaultBotName(agent.Name))
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
