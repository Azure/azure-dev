// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	rm_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	azd_armmsi "github.com/azure/azure-dev/cli/azd/pkg/armmsi"

	_ "embed"
)

//go:embed templates/mcp.json
var mcpJson string

//go:embed templates/copilot-setup-steps.yml
var copilotSetupStepsYml string

const copilotEnv = "copilot"

type flagValues struct {
	RepoSlug  string
	RoleNames []string
}

func setupFlags(commandFlags *pflag.FlagSet) *flagValues {
	flagValues := &flagValues{}

	commandFlags.StringVar(
		&flagValues.RepoSlug,
		"remote-name",
		"",
		"The name of the git remote where the Copilot Coding Agent will run (ex: <owner>/<repo>)",
	)

	//nolint:lll
	commandFlags.StringArrayVar(
		&flagValues.RoleNames,
		"roles",
		[]string{"Reader"},
		"The roles to assign to the service principal or managed identity. By default, the service principal or managed identity will be granted the Reader role.",
	)

	return flagValues
}

func newConfigCommand() *cobra.Command {
	cc := &cobra.Command{
		Use:   "config",
		Short: "Configure the GitHub Copilot coding agent to access Azure resources via the Azure MCP",
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

		prompter := azdClient.Prompt()

		// Get the azd project to retrieve the project path
		getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})

		if err != nil {
			return fmt.Errorf("failed to get azd project: %w", err)
		}
		projectName := getProjectResponse.Project.Name
		if cmdFlags.RepoSlug == "" {
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
		subscriptionID := subscriptionResponse.Subscription.Id

		cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID: tenantID,
		})

		if err != nil {
			return err
		}

		cp := &credentialProviderAdapter{tokenCred: cred}

		msiService := azd_armmsi.NewArmMsiService(cp, nil)
		entraIDService := entraid.NewEntraIdService(cp, nil, nil)
		rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)

		if err != nil {
			return fmt.Errorf("failed to create the resource group client: %s", err)
		}

		authConfig, err := PickOrCreateMSI(ctx,
			prompter,
			msiService,
			entraIDService,
			rgClient,
			projectName, subscriptionID, cmdFlags.RoleNames)

		if err != nil {
			return err
		}

		err = CreateFederatedCredential(ctx,
			msiService,
			cmdFlags.RepoSlug, copilotEnv, subscriptionID, authConfig.ResourceID)

		if err != nil {
			return err
		}

		var msg = fmt.Sprintf("Setting identity variables in the GitHub Copilot environment(AZURE_CLIENT_ID=%s)", authConfig.ClientId)
		fmt.Printf("%s\n", msg)

		if err := setCopilotEnvVars(ctx, cmdFlags, authConfig); err != nil {
			return err
		}

		gitRepoRoot, err := getGitRoot(ctx)

		if err != nil {
			return err
		}

		workflowsDir := filepath.Join(gitRepoRoot, ".github", "workflows")

		if err := os.MkdirAll(workflowsDir, 0755); err != nil {
			return fmt.Errorf("failed to create the %s folder: %w", workflowsDir, err)
		}

		// Create the copilot-setup-steps.yml file
		copilotSetupStepsPath := filepath.Join(workflowsDir, "copilot-setup-steps.yml")

		// Write the setup file
		if err := os.WriteFile(copilotSetupStepsPath, []byte(copilotSetupStepsYml), 0644); err != nil {
			return fmt.Errorf("failed to write copilot setup file: %w", err)
		}

		// doPush, err := prompter.Confirm(ctx, &azdext.ConfirmRequest{
		// 	Options: &azdext.ConfirmOptions{
		// 		Message: "Would you like to push these changes?",
		// 	},
		// })

		// if err != nil {
		// 	return err
		// }

		// var gitError error

		// if *doPush.Value {
		// 	if err := gitPush(ctx, gitRepoRoot); err != nil {
		// 		// the user can still fix this on their own, so we'll add that they need to complete the push.
		// 		gitError = err
		// 	}
		// }

		fmt.Printf("\n\nNOTE: Some manual setup steps still need to be completed!\n")

		fmt.Printf("1. Merge the changes to %s to the main branch of your repository.\n", copilotSetupStepsPath)
		fmt.Printf("2. Visit https://github.com/%s/settings/copilot/coding_agent and paste the following into \"MCP configuration\" field:\n%s", cmdFlags.RepoSlug, mcpJson)

		return nil
	}

	return cc
}

func setCopilotEnvVars(ctx context.Context, cmdFlags *flagValues, authConfig *authConfiguration) error {
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

	if err := cli.CreateEnvironmentIfNotExist(ctx, cmdFlags.RepoSlug, copilotEnv); err != nil {
		return err
	}

	varsToSet := map[string]string{
		"AZURE_CLIENT_ID":       authConfig.ClientId,
		"AZURE_TENANT_ID":       authConfig.TenantId,
		"AZURE_SUBSCRIPTION_ID": authConfig.SubscriptionId,
	}

	for name, value := range varsToSet {
		if err := cli.SetVariable(ctx, cmdFlags.RepoSlug, name, value, &github.SetVariableOptions{
			Environment: copilotEnv,
		}); err != nil {
			return err
		}
	}

	return nil
}

type credentialProviderAdapter struct {
	tokenCred azcore.TokenCredential
}

func (cp *credentialProviderAdapter) CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error) {
	return cp.tokenCred, nil
}

// CreateFederatedCredential creates a federated credential (allowing Copilot to authenticate and use Azure)
func CreateFederatedCredential(ctx context.Context,
	msiService azd_armmsi.ArmMsiService,
	repoSlug string,
	copilotEnvName string,
	subscriptionId string,
	msiResourceID string) error {
	// copied from azd's github_provider.go
	const (
		federatedIdentityIssuer   = "https://token.actions.githubusercontent.com"
		federatedIdentityAudience = "api://AzureADTokenExchange"
	)

	credentialSafeName := strings.ReplaceAll(repoSlug, "/", "-")

	armFedCreds := []rm_armmsi.FederatedIdentityCredential{
		{
			Name: to.Ptr(url.PathEscape(fmt.Sprintf("%s-copilot-env", credentialSafeName))),
			Properties: &rm_armmsi.FederatedIdentityCredentialProperties{
				Subject:   to.Ptr(fmt.Sprintf("repo:%s:environment:%s", repoSlug, copilotEnvName)),
				Issuer:    to.Ptr(federatedIdentityIssuer),
				Audiences: []*string{to.Ptr(federatedIdentityAudience)},
			},
		},
	}

	if _, err := msiService.ApplyFederatedCredentials(ctx, subscriptionId, msiResourceID, armFedCreds); err != nil {
		return fmt.Errorf("failed to create federated credentials: %w", err)
	}

	return nil
}

func GetExistingMSIByResourceID(ctx context.Context,
	msiService azd_armmsi.ArmMsiService,
	managedIdentityResourceID string) (*authConfiguration, error) {
	id, err := msiService.GetUserIdentity(ctx, managedIdentityResourceID)

	if err != nil {
		return nil, fmt.Errorf("failed to get user identity for resource ID %s: %w", managedIdentityResourceID, err)
	}

	return &authConfiguration{
		ClientId:       *id.Properties.ClientID,
		SubscriptionId: *id.Properties.TenantID,
		TenantId:       *id.Properties.TenantID,
		ResourceID:     *id.ID,
	}, nil
}

// PickOrCreateMSI walks the user through creating an MSI
func PickOrCreateMSI(ctx context.Context,
	prompter azdext.PromptServiceClient,
	msiService azd_armmsi.ArmMsiService,
	entraIDService entraid.EntraIdService,
	resourceService interface {
		CreateOrUpdate(ctx context.Context, resourceGroupName string, parameters armresources.ResourceGroup, options *armresources.ResourceGroupsClientCreateOrUpdateOptions) (armresources.ResourceGroupsClientCreateOrUpdateResponse, error)
	},
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

		resourceGroupName, err := GetOrCreateResourceGroup(ctx, prompter, subscriptionId, location.Location.Name, resourceService)

		if err != nil {
			return nil, err
		}

		displayMsg := fmt.Sprintf("Creating User Managed Identity (MSI) for %s", projectName)

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: displayMsg,
		})

		err = spinner.Run(ctx, func(ctx context.Context) error {
			newMSI, err := msiService.CreateUserIdentity(ctx, subscriptionId, resourceGroupName, location.Location.Name, "msi-copilot-"+projectName)

			if err != nil {
				return err
			}

			msIdentity = newMSI
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
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
		TenantId:       *msIdentity.Properties.TenantID,
		SubscriptionId: subscriptionId,
		ResourceID:     *msIdentity.ID,
		ClientId:       *msIdentity.Properties.ClientID,
	}, nil
}

func GetOrCreateResourceGroup(ctx context.Context,
	prompter azdext.PromptServiceClient,
	subscriptionId string, locationName string, resourceService interface {
		CreateOrUpdate(ctx context.Context, resourceGroupName string, parameters armresources.ResourceGroup, options *armresources.ResourceGroupsClientCreateOrUpdateOptions) (armresources.ResourceGroupsClientCreateOrUpdateResponse, error)
	}) (resourceGroupName string, err error) {
	rg, err := prompter.PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: subscriptionId,
				Location:       locationName,
			},
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed trying to get a resource group name from prompt: %w", err)
	}

	// create resource group returns a sentinel value if the user chooses to create a resource group
	// but does NOT create it, so we'll have to do that here.

	if rg.ResourceGroup.Id != "new" {
		resourceGroupName = rg.ResourceGroup.Name
	} else {
		// user chose to create a group, let's take them through that flow
		rgPrompt, err := prompter.Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: "Enter a name for the new resource group",
			},
		})

		if err != nil {
			return "", err
		}

		msg := fmt.Sprintf("Creating resource group %s in subscription %s", rgPrompt.Value, subscriptionId)
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: msg,
		})

		err = spinner.Run(ctx, func(ctx context.Context) error {
			createRGResp, err := resourceService.CreateOrUpdate(ctx, rgPrompt.Value, armresources.ResourceGroup{
				Location: &locationName,
			}, nil)

			if err != nil {
				return err
			}

			resourceGroupName = *createRGResp.Name
			return nil
		})

		if err != nil {
			return "", fmt.Errorf("failed to create resource group: %w", err)
		}
	}

	return resourceGroupName, nil
}

type authConfiguration struct {
	ClientId       string
	SubscriptionId string
	TenantId       string
	ResourceID     string
}

func getGitRoot(ctx context.Context) (string, error) {
	gitRevParseCmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")

	buff := &bytes.Buffer{}
	gitRevParseCmd.Stdout = buff

	if err := gitRevParseCmd.Run(); err != nil {
		return "", fmt.Errorf("failed using git rev-parse to get the top level directory for this repo: %w", err)
	}

	gitRoot := strings.TrimSpace(buff.String())

	if runtime.GOOS == "windows" {
		// even on Windows, git will return unix style paths.
		gitRoot = strings.ReplaceAll(gitRoot, "/", string(os.PathSeparator))
	}

	return gitRoot, nil
}
