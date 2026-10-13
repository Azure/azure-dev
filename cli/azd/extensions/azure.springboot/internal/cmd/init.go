// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
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
	var yes bool
	command := &cobra.Command{
		Use:   "init",
		Short: "Infer active Spring dependencies and generate an azd project",
		Long: `Inspect a trusted single-module Spring Boot Maven application and generate
azure.yaml, Container Apps Bicep, and deployment-only container/configuration files.
Use standard Spring configuration, including SPRING_PROFILES_ACTIVE, to activate
database alternatives. Embedded databases do not create a cloud database.

Maven inspection executes project-controlled code. Generated SQL infrastructure
uses password authentication and broad public firewall rules intended for development.
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
			directory, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := spring.CheckDestination(directory); err != nil {
				return err
			}
			reader := bufio.NewReader(cmd.InOrStdin())
			if !yes {
				question := "Trust this project's Maven wrapper and build extensions?"
				if err := confirm(cmd.OutOrStdout(), reader, question); err != nil {
					return err
				}
			}
			// The reused Maven runner logs captured effective models, which may contain credentials.
			previous := log.Writer()
			log.SetOutput(io.Discard)
			defer log.SetOutput(previous)
			application, err := spring.Analyze(
				cmd.Context(), directory, spring.NewModelResolver(io.Discard), os.LookupEnv)
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
			if len(application.Requirements) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Database: no cloud database.")
			}
			for _, requirement := range application.Requirements {
				fmt.Fprintf(cmd.OutOrStdout(), "Database: %s server and %s database (password authentication).\n",
					requirement.Kind, requirement.Name)
				fmt.Fprintln(cmd.OutOrStdout(), "Warning: generated database firewall allows all public IP addresses.")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Files:")
			for _, name := range sortedFiles(plan) {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", name)
			}
			if !yes {
				if err := confirm(cmd.OutOrStdout(), reader, "Create these deployment files?"); err != nil {
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
	return command
}

func confirm(writer io.Writer, reader *bufio.Reader, question string) error {
	fmt.Fprintf(writer, "? %s [y/N]: ", question)
	text, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	if !slices.Contains([]string{"y", "yes"}, strings.ToLower(strings.TrimSpace(text))) {
		return errors.New("initialization cancelled; no deployment files were written")
	}
	return nil
}

func sortedFiles(plan *spring.Plan) []string {
	return slices.Sorted(maps.Keys(plan.Files))
}
