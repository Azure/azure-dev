// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/cheatcode"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	_ "embed"
)

//go:embed templates/mcp.json
var mcpJson string

//go:embed templates/copilot-setup-steps.yml
var copilotSetupStepsYml string

type flagValues struct {
	RoleNames  []string
	CopilotEnv string
	RepoSlug   string
}

func setupFlags(commandFlags *pflag.FlagSet) *flagValues {
	flagValues := &flagValues{}

	//nolint:lll
	commandFlags.StringArrayVar(
		&flagValues.RoleNames,
		"roles",
		[]string{"Reader"},
		"The roles to assign to the service principal or managed identity. By default, the service principal or managed identity will be granted the Reader role.",
	)

	//, The documentation makes it seem like you can only use 'copilot' as the environment, so we'll leave this as non-configurable for now.
	flagValues.CopilotEnv = "copilot"

	//nolint, lll
	commandFlags.StringVar(
		&flagValues.RepoSlug,
		"repo",
		"",
		"The GitHub repo which will be authorized to use the federated credential (ex: <owner>/<repo>)",
	)

	return flagValues
}

func newConfigCommand() *cobra.Command {
	cc := &cobra.Command{
		Use:   "config",
		Short: "Configure a GitHub repo to support the GitHub coding agent",
	}

	cmdFlags := setupFlags(cc.Flags())

	cc.RunE = func(cmd *cobra.Command, args []string) error {
		// Create a new context that includes the AZD access token
		ctx := azdext.WithAccessToken(cmd.Context())

		// Create a new AZD client
		azdClient, err := azdext.NewAzdClient()

		if err != nil {
			return fmt.Errorf("failed to create azd client: %w", err)
		}

		defer azdClient.Close()

		// Get the azd project to retrieve the project path
		getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})

		if err != nil {
			return fmt.Errorf("failed to get azd project: %w", err)
		}

		project := getProjectResponse.Project

		rootContainer, err := cheatcode.NewRootContainer(ctx, ".")

		if err != nil {
			return err
		}

		if cmdFlags.RepoSlug == "" {
			// this has to be filled out - when we create/set federated credentials it's part of the subject.
			res, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:     "Enter the <owner>/<repository> where the Copilot Coding Agent will run",
					Placeholder: "<owner>/<repository>",
				},
			})

			if err != nil {
				return err
			}

			cmdFlags.RepoSlug = res.Value
		}

		console := input.NewConsole(true, true, input.Writers{
			Output:  os.Stdout,
			Spinner: os.Stdout,
		}, input.ConsoleHandles{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}, &output.NoneFormatter{}, nil)

		rootContainer.MustRegisterSingleton(func() azdext.PromptServiceClient {
			return azdClient.Prompt()
		})

		rootContainer.MustRegisterSingleton(func() input.Console {
			return console
		})

		subscriptionResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})

		if err != nil {
			return err
		}

		subscriptionId := subscriptionResponse.Subscription.Id

		authConfig, err := cheatcode.PickOrCreateMSI(ctx, rootContainer, project.Name, subscriptionId, cmdFlags.RoleNames)

		if err != nil {
			return err
		}

		err = cheatcode.SetCopilotCodingAgentFederation(ctx, rootContainer, cmdFlags.RepoSlug, cmdFlags.CopilotEnv, subscriptionId, *authConfig.MSI.ID)

		if err != nil {
			return err
		}

		err = func() error {
			var msg = fmt.Sprintf("Setting variables in the GitHub Copilot environment\n  AZURE_CLIENT_ID=%s\n  AZURE_TENANT_ID=%s\n  AZURE_SUBSCRIPTION_ID=%s\n", authConfig.AzureCredentials.ClientId, authConfig.AzureCredentials.TenantId, authConfig.AzureCredentials.SubscriptionId)

			console.ShowSpinner(ctx, msg, input.Step)
			defer console.StopSpinner(ctx, msg, input.StepDone)

			commandRunner := azdexec.NewCommandRunner(nil)

			cli, err := github.NewGitHubCli(ctx, console, commandRunner)

			if err != nil {
				return err
			}

			if err := cli.CreateEnvironmentIfNotExist(ctx, cmdFlags.RepoSlug, cmdFlags.CopilotEnv); err != nil {
				return err
			}

			varsToSet := map[string]string{
				"AZURE_CLIENT_ID":       authConfig.AzureCredentials.ClientId,
				"AZURE_TENANT_ID":       authConfig.AzureCredentials.TenantId,
				"AZURE_SUBSCRIPTION_ID": authConfig.AzureCredentials.SubscriptionId,
			}

			for name, value := range varsToSet {
				if err := cli.SetVariable(ctx, cmdFlags.RepoSlug, name, value, &github.SetVariableOptions{
					Environment: cmdFlags.CopilotEnv,
				}); err != nil {
					return err
				}
			}

			return nil
		}()

		if err != nil {
			return err
		}

		// Create the .github/workflows directory if it doesn't exist
		workflowsDir := filepath.Join(project.Path, ".github", "workflows")

		// TODO: mask?
		if err := os.MkdirAll(workflowsDir, 0600); err != nil {
			return fmt.Errorf("failed to create the .github/workflows folder")
		}

		// Create the copilot-setup-steps.yml file
		copilotSetupStepsPath := filepath.Join(workflowsDir, "copilot-setup-steps.yml")

		// fmt.Printf("// TODO: if copilot-setup-steps.yml already exists then don't just overwrite it\n")
		// TODO: create the copilot enviroment and populate it's values with the chosen identity.

		// Basic content for copilot setup steps with Azure login
		actualContent := fmt.Sprintf(copilotSetupStepsYml, cmdFlags.CopilotEnv)

		// Write the setup file
		if err := os.WriteFile(copilotSetupStepsPath, []byte(actualContent), 0644); err != nil {
			return fmt.Errorf("failed to write copilot setup file: %w", err)
		}

		// TODO: git CLI code. There's better stuff in pipeline_manager.go
		fmt.Printf("Pushing changes\n")
		{
			gitAddCmd := exec.CommandContext(ctx, "git", "add", ".github/workflows/copilot-setup-steps.yml")
			gitAddCmd.Dir = project.Path
			gitAddCmd.Stdout = os.Stdout
			gitAddCmd.Stderr = os.Stderr

			if err := gitAddCmd.Run(); err != nil {
				return fmt.Errorf("failed to add changes: %w", err)
			}

			gitCommitCmd := exec.CommandContext(ctx, "git", "commit", "-m", "adding copilot-setup-steps.yml", ".github/workflows/copilot-setup-steps.yml")
			gitCommitCmd.Dir = project.Path
			gitCommitCmd.Stdout = os.Stdout
			gitCommitCmd.Stderr = os.Stderr

			if err := gitCommitCmd.Run(); err != nil {
				return fmt.Errorf("failed to commit changes: %w", err)
			}

			gitPushCmd := exec.CommandContext(ctx, "git", "push")
			gitPushCmd.Dir = project.Path
			gitPushCmd.Stdout = os.Stdout
			gitPushCmd.Stderr = os.Stderr

			if err := gitPushCmd.Run(); err != nil {
				return fmt.Errorf("failed to push changes: %w", err)
			}
		}

		fmt.Printf("\nNOTE: to enable the Azure MCP with Copilot visit https://github.com/%s/settings/copilot/coding_agent and paste the following into MCP configuration field:\n%s", cmdFlags.RepoSlug, mcpJson)
		// fmt.Printf("\nNOTE: Additional setup is required.\n  See https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/extend-coding-agent-with-mcp for instructions on enabling specific MCP servers for Copilot Coding Agent\n")
		return nil
	}

	return cc
}
