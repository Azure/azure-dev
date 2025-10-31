// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "listen",
		Short: "Starts the extension and listens for events.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			projectParser := &project.FoundryParser{AzdClient: azdClient}
			// IMPORTANT: service target name here must match the name used in the extension manifest.
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget(AiAgentHost, func() azdext.ServiceTargetProvider {
					return project.NewAgentServiceTargetProvider(azdClient)
				}).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return preprovisionHandler(ctx, azdClient, projectParser, args)
				}).
				WithProjectEventHandler("postdeploy", projectParser.CoboPostDeploy)

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}

			return nil
		},
	}
}

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, projectParser *project.FoundryParser, args *azdext.ProjectEventArgs) error {
	if err := projectParser.SetIdentity(ctx, args); err != nil {
		return fmt.Errorf("failed to set identity: %w", err)
	}

	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}

		return preprovisionEnvUpdate(ctx, azdClient, args.Project, svc)
	}

	return nil
}

func preprovisionEnvUpdate(ctx context.Context, azdClient *azdext.AzdClient, azdProject *azdext.ProjectConfig, svc *azdext.ServiceConfig) error {

	var foundryAgentConfig *project.ServiceTargetAgentConfig

	if err := project.UnmarshalStruct(svc.Config, &foundryAgentConfig); err != nil {
		return fmt.Errorf("failed to parse foundry agent config: %w", err)
	}

	currentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	if err := kindEnvUpdate(ctx, azdClient, azdProject, svc, currentEnvResponse.Environment.Name); err != nil {
		return err
	}

	if err := deploymentEnvUpdate(ctx, foundryAgentConfig.Deployments, azdClient, currentEnvResponse.Environment.Name); err != nil {
		return err
	}

	return nil
}

func kindEnvUpdate(ctx context.Context, azdClient *azdext.AzdClient, project *azdext.ProjectConfig, svc *azdext.ServiceConfig, envName string) error {
	servicePath := svc.RelativePath
	fullPath := filepath.Join(project.Path, servicePath)
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")

	data, err := os.ReadFile(agentYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %w", err)
	}

	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(data)
	if err != nil {
		return fmt.Errorf("failed to parse and validate YAML: %w", err)
	}

	// Convert the template to bytes
	templateBytes, err := json.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal agent template to JSON: %w", err)
	}

	// Convert the bytes to a dictionary
	var templateDict map[string]interface{}
	if err := json.Unmarshal(templateBytes, &templateDict); err != nil {
		return fmt.Errorf("failed to unmarshal agent template from JSON: %w", err)
	}

	// Convert the dictionary to bytes
	dictJsonBytes, err := json.Marshal(templateDict)
	if err != nil {
		return fmt.Errorf("failed to marshal templateDict to JSON: %w", err)
	}

	// Convert the bytes to an Agent Definition
	var agentDef agent_yaml.AgentDefinition
	if err := json.Unmarshal(dictJsonBytes, &agentDef); err != nil {
		return fmt.Errorf("failed to unmarshal JSON to AgentDefinition: %w", err)
	}

	// Set environment variables based on agent kind
	switch agentDef.Kind {
	case agent_yaml.AgentKindHosted:
		if err := setEnvVar(ctx, azdClient, envName, "ENABLE_HOSTED_AGENTS", "true"); err != nil {
			return err
		}
	}

	return nil
}

func deploymentEnvUpdate(ctx context.Context, deployments []project.Deployment, azdClient *azdext.AzdClient, envName string) error {
	deploymentsJson, err := json.Marshal(deployments)
	if err != nil {
		return fmt.Errorf("failed to marshal deployment details to JSON: %w", err)
	}

	return setEnvVar(ctx, azdClient, envName, "AI_PROJECT_DEPLOYMENTS", string(deploymentsJson))
}

func setEnvVar(ctx context.Context, azdClient *azdext.AzdClient, envName string, key string, value string) error {
	_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	fmt.Printf("Set environment variable: %s=%s\n", key, value)
	return nil
}
