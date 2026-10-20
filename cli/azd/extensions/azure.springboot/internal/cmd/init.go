// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azure.springboot/internal/spring"
)

func newInitCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInitCommandWithServices(extCtx, spring.NewModelResolver(io.Discard), newHostPrompter)
}

func newInitCommandWithServices(
	extCtx *azdext.ExtensionContext, resolver spring.ModelResolver, openPrompts func() (initPrompter, error),
) *cobra.Command {
	var yes bool
	var profiles []string
	command := &cobra.Command{
		Use:   "init",
		Short: "Infer active Spring dependencies and generate an azd project",
		Long: `Inspect a trusted single-module Spring Boot Maven application and generate
azure.yaml, Container Apps Bicep, and deployment-only container/configuration files.
Application configuration supplies defaults. Use --profiles to select deployment
profiles. Missing datasource URL or server port inputs use targeted prompts.
Interactive initialization can offer to replace packaged datasource password or
Azure OpenAI API-key defaults in properties files with runtime-only placeholders.
Ambient Spring environment variables are not deployment inputs. Questions are
asked only for unresolved decisions; --yes does not supply missing choices.
Embedded databases do not create a cloud database.

Maven inspection executes project-controlled code. Generated SQL infrastructure
uses an extension-owned template with public database access restricted to reported
Container Apps outbound IPs. Foundry chat bindings use the same outbound-IP
restriction and inject the account key only through a Container Apps secret.
PostgreSQL managed identity temporarily allows public bootstrap access and requires
Azure Identity Extensions in the app and an explicit administrator-run schema/grant
script before provisioning.
This command does not provision Azure resources or create an azd environment.
Existing deployment files are not overwritten.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, flag := range []string{"environment", "output"} {
				if cmd.Flags().Changed(flag) {
					return fmt.Errorf("--%s cannot be used with spring init; "+
						"no environment or output format is selected", flag)
				}
			}
			if extCtx.NoPrompt && !yes {
				return errors.New("--no-prompt requires --yes to trust Maven inspection and accept the generated plan")
			}
			inputs := spring.DeploymentInputs{}
			if cmd.Flags().Changed("profiles") {
				if len(profiles) == 0 {
					return errors.New("--profiles requires at least one profile name")
				}
				inputs.Profiles = profiles
			}
			directory, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := spring.CheckDestination(directory); err != nil {
				return err
			}
			var prompts initPrompter
			defer func() {
				if prompts != nil {
					prompts.Close()
				}
			}()
			getPrompts := func() (initPrompter, error) {
				if prompts == nil {
					var err error
					prompts, err = openPrompts()
					if err != nil {
						return nil, fmt.Errorf("interactive prompts unavailable; "+
							"use --yes --no-prompt with resolved application configuration "+
							"and --profiles if needed: %w", err)
					}
				}
				return prompts, nil
			}
			if !yes {
				question := "Trust this project's Maven wrapper and build extensions?"
				if err := confirm(cmd.Context(), getPrompts, question); err != nil {
					return err
				}
			}
			// The reused Maven runner logs captured effective models, which may contain credentials.
			previous := log.Writer()
			log.SetOutput(io.Discard)
			defer log.SetOutput(previous)
			application, err := resolveDeployment(
				cmd.Context(), directory, resolver, inputs, extCtx.NoPrompt, getPrompts)
			if err != nil {
				return err
			}
			plan, err := spring.Generate(cmd.Context(), application)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Application: %s; Java %d; profiles: %s\n",
				application.Name, application.JavaVersion, strings.Join(application.Profiles, ", "))
			fmt.Fprintln(cmd.OutOrStdout(), "Hosting: Azure Container Apps, container registry, and Log Analytics.")
			if application.Dockerfile != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Container build: application-owned Dockerfile.")
			}
			if len(application.Requirements) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Database: no cloud database.")
			}
			for _, requirement := range application.Requirements {
				fmt.Fprintf(cmd.OutOrStdout(), "Database: %s server and %s database (%s authentication).\n",
					requirement.Kind, requirement.Name, requirement.Authentication)
				fmt.Fprintln(cmd.OutOrStdout(),
					"Database network: public endpoint restricted to reported Container Apps outbound IPs.")
				if requirement.Authentication == "managedIdentity" {
					fmt.Fprintln(cmd.OutOrStdout(),
						"Required: Entra administrator and reviewed SQL schema/grant script.")
				}
			}
			if application.Foundry != nil {
				authentication := "API-key"
				if application.Foundry.Authentication == "managedIdentity" {
					authentication = "managed identity"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Foundry: AIServices account (%s authentication).\n", authentication)
				if application.Foundry.ChatDeploymentName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Foundry chat deployment: %s.\n",
						application.Foundry.ChatDeploymentName)
				}
				if application.Foundry.EmbeddingDeploymentName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Foundry embedding deployment: %s.\n",
						application.Foundry.EmbeddingDeploymentName)
				}
				fmt.Fprintln(cmd.OutOrStdout(),
					"Foundry network: public endpoint restricted to reported Container Apps outbound IPs.")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Files:")
			for _, name := range sortedFiles(plan) {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", name)
			}
			if !yes {
				if err := confirm(cmd.Context(), getPrompts, "Create these deployment files?"); err != nil {
					return err
				}
			}
			if err := plan.Write(cmd.Context(), directory); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Initialization complete. Run azd up to provision and deploy.")
			return nil
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "Trust Maven inspection and accept generation without prompting")
	command.Flags().StringSliceVar(&profiles, "profiles", nil,
		"Spring deployment profiles (comma-separated); otherwise use application defaults")
	return command
}

func sortedFiles(plan *spring.Plan) []string {
	return slices.Sorted(maps.Keys(plan.Files))
}
