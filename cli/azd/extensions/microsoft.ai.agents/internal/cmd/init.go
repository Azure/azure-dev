// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	validLanguages = []string{"python", "java"}
	validTypes     = []string{"code", "declarative"}
)

type initFlags struct {
	language string
	initType string
}

func newInitCommand() *cobra.Command {
	flags := &initFlags{}

	cmd := &cobra.Command{
		Use:   "init [--language <language>] [--type <type>]",
		Short: "Initialize a new AI agent project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			return runInitAction(ctx, azdClient, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.language, "language", "l", "",
		"Programming language for the agent (python, java)")
	cmd.Flags().StringVarP(&flags.initType, "type", "t", "",
		"Type of agent to create (code, declarative)")

	return cmd
}

func runInitAction(ctx context.Context, azdClient *azdext.AzdClient, flags *initFlags) error {
	color.Green("Initializing AI agent project...")
	fmt.Println()

	if err := validateFlags(flags); err != nil {
		return err
	}

	if err := promptForMissingValues(ctx, azdClient, flags); err != nil {
		return fmt.Errorf("collecting required information: %w", err)
	}

	fmt.Println("Configuration:")
	fmt.Printf("  Language: %s\n", flags.language)
	fmt.Printf("  Type: %s\n", flags.initType)
	fmt.Println()

	foundryResource, err := promptForFoundry(ctx, azdClient)
	if err != nil {
		return fmt.Errorf("selecting Foundry resource: %w", err)
	}

	if foundryResource != nil {
		fmt.Printf("\nSelected Foundry resource: %s\n", foundryResource.Id)
	}

	if err := addToProject(ctx, azdClient, "my-agent", flags); err != nil {
		return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
	}

	color.Green("\nAI agent project initialized successfully!")
	return nil
}

func addToProject(ctx context.Context, azdClient *azdext.AzdClient, agentName string, flags *initFlags) error {
	serviceConfig := &azdext.ServiceConfig{
		Name:         agentName,
		RelativePath: fmt.Sprintf("agents/%s", agentName),
		Language:     flags.language,
		Host:         "ai.endpoint",
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("Added service '%s' to azure.yaml\n", agentName)
	return nil
}

func validateFlags(flags *initFlags) error {
	if flags.language != "" {
		if !slices.Contains(validLanguages, flags.language) {
			return fmt.Errorf("invalid language '%s', valid options are: %v", flags.language, validLanguages)
		}
	}

	if flags.initType != "" {
		if !slices.Contains(validTypes, flags.initType) {
			return fmt.Errorf("invalid type '%s', valid options are: %v", flags.initType, validTypes)
		}
	}

	return nil
}

func promptForMissingValues(ctx context.Context, azdClient *azdext.AzdClient, flags *initFlags) error {
	if flags.language == "" {
		resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select the programming language for your agent:",
				Choices: []*azdext.SelectChoice{
					{Label: "Python", Value: "python"},
					{Label: "Java", Value: "java"},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for language: %w", err)
		}

		if resp.Value != nil {
			flags.language = validLanguages[*resp.Value]
		}
	}

	if flags.initType == "" {
		resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select the type of agent:",
				Choices: []*azdext.SelectChoice{
					{Label: "Code Agent", Value: "code"},
					{Label: "Declarative Agent", Value: "declarative"},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for agent type: %w", err)
		}

		if resp.Value != nil {
			flags.initType = validTypes[*resp.Value]
		}
	}

	return nil
}

func promptForFoundry(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.ResourceExtended, error) {
	selectedSubscription, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
	if err != nil {
		return nil, fmt.Errorf("prompting for subscription: %w", err)
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			SubscriptionId: selectedSubscription.Subscription.Id,
			TenantId:       selectedSubscription.Subscription.TenantId,
		},
	}

	selectedResourceResponse, err := azdClient.Prompt().PromptSubscriptionResource(ctx, &azdext.PromptSubscriptionResourceRequest{
		AzureContext: azureContext,
		Options: &azdext.PromptResourceOptions{
			ResourceType: "Microsoft.CognitiveServices/accounts",
			Kinds:        []string{"AIServices"},
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: to.Ptr(false),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for AI Services resource: %w", err)
	}

	return selectedResourceResponse.Resource, nil
}
