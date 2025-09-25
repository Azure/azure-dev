// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	rm_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	azd_armmsi "github.com/azure/azure-dev/cli/azd/pkg/armmsi"

	_ "embed"
)

//go:embed templates/mcp.json
var mcpJson string

//go:embed templates/copilot-setup-steps.yml
var copilotSetupStepsYml string

type flagValues struct {
	Subscription string
	RoleNames    []string
	CopilotEnv   string
	RepoSlug     string
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
		"remote-name",
		"",
		"The name of the git remote where the Copilot Coding Agent will run (ex: <owner>/<repo>)",
	)

	// //nolint, lll
	// commandFlags.StringVar(
	// 	&flagValues.Subscription,
	// 	"subscription",
	// 	"",
	// 	"ID of an Azure Subscription which the coding agent will access",
	// )

	return flagValues
}

func newConfigCommand() *cobra.Command {
	cc := &cobra.Command{
		Use:   "config",
		Short: "Configure the GitHub Copiot coding agent to access Azure resources via the Azure MCP",
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
		prompter := azdClient.Prompt()

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

		subscriptionResponse, err := prompter.PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})

		if err != nil {
			return err
		}

		tenantID := subscriptionResponse.Subscription.TenantId
		subscriptionId := subscriptionResponse.Subscription.Id

		cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID: tenantID,
		})

		if err != nil {
			return err
		}

		cp := &credentialProviderAdapter{tokenCred: cred}

		msiService := azd_armmsi.NewArmMsiService(cp, nil)
		entraIDService := entraid.NewEntraIdService(cp, nil, nil)

		authConfig, err := PickOrCreateMSI(ctx,
			prompter,
			msiService,
			entraIDService,
			project.Name, subscriptionId, cmdFlags.RoleNames)

		if err != nil {
			return err
		}

		err = SetCopilotCodingAgentFederation(ctx,
			msiService,
			cmdFlags.RepoSlug, cmdFlags.CopilotEnv, subscriptionId, *authConfig.MSI.ID)

		if err != nil {
			return err
		}

		var msg = fmt.Sprintf("Setting identity variables in the GitHub Copilot environment(AZURE_CLIENT_ID=%s)", authConfig.AzureCredentials.ClientId)

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: msg,
		})

		err = spinner.Run(ctx, func(ctx context.Context) error {
			commandRunner := azdexec.NewCommandRunner(nil)

			console := input.NewConsole(true, true, input.Writers{
				Output:  os.Stdout,
				Spinner: os.Stdout,
			}, input.ConsoleHandles{
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			}, &output.NoneFormatter{}, nil)

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
		})
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

type credentialProviderAdapter struct {
	tokenCred azcore.TokenCredential
}

func (cp *credentialProviderAdapter) CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error) {
	return cp.tokenCred, nil
}

func SetCopilotCodingAgentFederation(ctx context.Context,
	msiService azd_armmsi.ArmMsiService,
	repoSlug string,
	copilotEnvName string,
	subscriptionId string,
	msiId string, // was *authConfig.msi.ID
) error {
	credentialSafeName := strings.ReplaceAll(repoSlug, "/", "-")

	federatedCredentialOptions := []*graphsdk.FederatedIdentityCredential{
		{
			Name:        url.PathEscape(fmt.Sprintf("%s-copilot-coding-agent-env", credentialSafeName)),
			Issuer:      pipeline.CheatCodeIssuer,
			Subject:     fmt.Sprintf("repo:%s:environment:%s", repoSlug, copilotEnvName),
			Description: to.Ptr("Created by Azure Developer CLI"),
			Audiences:   []string{pipeline.CheatCodeFederatedIdentityAudience},
		},
	}

	// Enable federated credentials if requested
	type fedCredentialData struct{ Name, Subject, Issuer string }

	// TODO: for now, assuming MSI

	// convert fedCredentials from msGraph to armmsi.FederatedIdentityCredential
	armFedCreds := make([]rm_armmsi.FederatedIdentityCredential, len(federatedCredentialOptions))
	for i, fedCred := range federatedCredentialOptions {
		armFedCreds[i] = rm_armmsi.FederatedIdentityCredential{
			Name: to.Ptr(fedCred.Name),
			Properties: &rm_armmsi.FederatedIdentityCredentialProperties{
				Subject:   to.Ptr(fedCred.Subject),
				Issuer:    to.Ptr(fedCred.Issuer),
				Audiences: to.SliceOfPtrs(fedCred.Audiences...),
			},
		}
	}

	if _, err := msiService.ApplyFederatedCredentials(ctx, subscriptionId, msiId, armFedCreds); err != nil {
		return fmt.Errorf("failed to create federated credentials: %w", err)
	}

	return nil
}

func PickOrCreateMSI(ctx context.Context,
	prompter azdext.PromptServiceClient,
	msiService azd_armmsi.ArmMsiService,
	entraIDService entraid.EntraIdService,
	projectName string, subscriptionId string, roleNames []string) (*authConfiguration, error) {

	// ************************** Pick or create a new MSI **************************

	// Prompt for pick or create a new MSI
	selectedOption, err := prompter.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Do you want to create a new User Managed Identity (MSI) or use an existing one?",
			Choices: []*azdext.SelectChoice{
				{Label: "Create new User Managed Identity (MSI)"},
				{Label: "Use existing User Managed Identity (MSI)"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for MSI option: %w", err)
	}

	var msIdentity rm_armmsi.Identity

	if *selectedOption.Value == 0 {
		// pick a resource group and location for the new MSI
		location, err := prompter.PromptLocation(ctx, &azdext.PromptLocationRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: subscriptionId,
				},
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting for MSI location: %w", err)
		}

		rg, err := prompter.PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: subscriptionId,
					Location:       location.Location.Name,
				},
			},
		})

		if err != nil {
			return nil, fmt.Errorf("failed trying to get a resource group name: %w", err)
		}

		displayMsg := fmt.Sprintf("Creating User Managed Identity (MSI) for %s", projectName)

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: displayMsg,
		})

		err = spinner.Run(ctx, func(ctx context.Context) error {
			// Create a new MSI
			newMSI, err := msiService.CreateUserIdentity(ctx, subscriptionId, rg.ResourceGroup.Name, location.Location.Name, "msi-"+projectName)

			if err != nil {
				return err
			}

			msIdentity = newMSI
			return nil
		})

		if err != nil {
			return &authConfiguration{}, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
		}
	} else {
		// List existing MSIs and let the user select one
		msIdentities, err := msiService.ListUserIdentities(ctx, subscriptionId)
		if err != nil {
			return nil, fmt.Errorf("failed to list User Managed Identities (MSI): %w", err)
		}
		if len(msIdentities) == 0 {
			return nil, fmt.Errorf("no User Managed Identities (MSI) found in subscription %s", subscriptionId)
		}
		// Prompt the user to select an existing MSI
		msiOptions := make([]string, len(msIdentities))
		choices := make([]*azdext.SelectChoice, len(msIdentities))

		for i, msi := range msIdentities {
			msiData, err := arm.ParseResourceID(*msi.ID)
			if err != nil {
				return nil, fmt.Errorf("parsing MSI resource id: %w", err)
			}
			msiOptions[i] = fmt.Sprintf("%2d. %s (%s)", i+1, *msi.Name, msiData.ResourceGroupName)
			choices[i] = &azdext.SelectChoice{
				Label: msiOptions[i],
			}
		}

		selectedOption, err := prompter.Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select an existing User Managed Identity (MSI) to use:",
				Choices: choices,
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting for existing MSI: %w", err)
		}
		msIdentity = msIdentities[*selectedOption.Value]
	}

	roleNameStrings := strings.Join(roleNames, ", ")

	displayMsg := fmt.Sprintf("Assigning roles (%s) to User Managed Identity (MSI) %s", roleNameStrings, *msIdentity.Name)
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: displayMsg,
	})

	err = spinner.Run(ctx, func(ctx context.Context) error {
		// ************************** Role Assign **************************
		return entraIDService.EnsureRoleAssignments(
			ctx,
			subscriptionId,
			roleNames,
			// EnsureRoleAssignments uses the ServicePrincipal ID and the DisplayName.
			// We are adapting the MSI to work with the same method as a regular Service Principal, by pulling name and ID.
			&graphsdk.ServicePrincipal{
				Id:          msIdentity.Properties.PrincipalID,
				DisplayName: *msIdentity.Name,
			},
		)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to assign role to User Managed Identity (MSI): %w", err)
	}

	return &authConfiguration{
		AzureCredentials: &entraid.AzureCredentials{
			ClientId:       *msIdentity.Properties.ClientID,
			TenantId:       *msIdentity.Properties.TenantID,
			SubscriptionId: subscriptionId,
		},
		MSI: &msIdentity,
	}, nil
}

type authConfiguration struct {
	*entraid.AzureCredentials
	// SP  *graphsdk.ServicePrincipal
	MSI *armmsi.Identity
}
