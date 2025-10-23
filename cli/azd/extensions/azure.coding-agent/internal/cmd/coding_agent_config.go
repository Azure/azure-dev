// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	azd_exec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	azd_github "github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	azd_git "github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	azd_tools_github "github.com/azure/azure-dev/cli/azd/pkg/tools/github"
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

//go:embed templates/pr-body.md
var prBodyMD string

const copilotEnv = "copilot"
const readmeURL = "https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.coding-agent/README.md"

type flagValues struct {
	Debug               bool
	ManagedIdentityName string
	RepoSlug            string
	RoleNames           []string
	BranchName          string
	GitHubHostName      string
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

	//nolint:lll
	commandFlags.StringVar(
		&flagValues.BranchName,
		"branch-name",
		"azd-enable-copilot-coding-agent-with-azure",
		"The branch name to use when pushing changes to the copilot-setup-steps.yml",
	)

	commandFlags.StringVar(
		&flagValues.ManagedIdentityName,
		"managed-identity-name",
		"mi-copilot-coding-agent",
		"The name to use for the managed identity, if created.",
	)

	commandFlags.StringVar(
		&flagValues.GitHubHostName,
		"github-host-name",
		"github.com",
		"The hostname to use with GitHub commands",
	)

	commandFlags.BoolVar(
		&flagValues.Debug,
		"debug",
		false,
		"Enables debugging and diagnostics logging.")

	return flagValues
}

func newConfigCommand() *cobra.Command {
	cc := &cobra.Command{
		Use:   "config",
		Short: "Configure the GitHub Copilot coding agent to access Azure resources via the Azure MCP",
		Long:  "Configure the GitHub Copilot coding agent to access Azure resources via the Azure MCP.\n\nFor more information about this command, including prerequisites and troubleshooting, view the readme at " + ux.Hyperlink("https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.coding-agent/README.md"),
	}

	flagValues := setupFlags(cc.Flags())

	cc.RunE = func(cmd *cobra.Command, args []string) error {
		if err := runConfigCommand(cmd, flagValues); err != nil {
			message := fmt.Sprintf("(!) An error occurred, see the readme for troubleshooting and prerequisites:\n    %s", ux.Hyperlink(readmeURL)) //nolint:lll
			fmt.Println(ux.BoldString(message))
			return err
		}

		return nil
	}

	return cc
}

func runConfigCommand(cmd *cobra.Command, flagValues *flagValues) error {
	if flagValues.Debug {
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.Discard)
	}

	// Create a new context that includes the AZD access token
	ctx := azdext.WithAccessToken(cmd.Context())

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()

	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	promptClient := azdClient.Prompt()

	// the defaults follow along with whatever the user has chosen for --debug. So if --debug is
	// _off_ then you don't see all the console output from sub-commands.
	defaultCommandRunner, defaultConsole := newCommandRunner(flagValues.Debug)
	defaultGitHubCLI, err := azd_tools_github.NewGitHubCli(ctx, defaultConsole, defaultCommandRunner)

	if err != nil {
		return fmt.Errorf("failed to get the github CLI: %w", err)
	}

	gitCLI := newInternalGitCLI(defaultCommandRunner)
	gitRepoRoot, err := gitCLI.GetRepoRoot(ctx, ".")

	if err != nil {
		return fmt.Errorf("failed to get git repository root: %w", err)
	}

	if _, err := listRemotes(ctx, gitCLI, gitRepoRoot); err != nil {
		return err
	}

	if err := loginToGitHubIfNeeded(ctx, flagValues.GitHubHostName, newCommandRunner, newGitHubCLI); err != nil {
		return fmt.Errorf("failed to log in to GitHub. Login manually using `gh auth login`: %w", err)
	}

	// this spot also serves as the "the user has `azd auth login`'d into Azure already"
	subscriptionResponse, err := promptClient.PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{
		Message:     "Select an Azure subscription to use with the Copilot coding agent",
		HelpMessage: "The Copilot coding agent will only be given access to a resource group within the Azure subscription you choose",
	})

	if err != nil {
		//nolint:lll
		return fmt.Errorf("failed getting a subscription from prompt. Try logging in manually with 'azd auth login' before running this command %w", err)
	}

	tenantID := subscriptionResponse.Subscription.TenantId
	subscriptionID := subscriptionResponse.Subscription.Id

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: tenantID,
	})

	if err != nil {
		return fmt.Errorf("failed to get the Azure Developer CLI credential: %w", err)
	}

	cp := &credentialProviderAdapter{tokenCred: cred}

	msiService := azd_armmsi.NewArmMsiService(cp, nil)
	entraIDService := entraid.NewEntraIdService(cp, nil, nil)
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)

	if err != nil {
		return fmt.Errorf("failed to create the resource group client: %w", err)
	}

	repoSlug, err := promptForCodingAgentRepoSlug(ctx, promptClient, gitCLI, gitRepoRoot, flagValues.RepoSlug)

	if err != nil {
		return fmt.Errorf("failed getting the <owner>/<repository>: %w", err)
	}

	authConfig, err := pickOrCreateMSI(ctx,
		promptClient,
		&msiService,
		entraIDService,
		rgClient,
		flagValues.ManagedIdentityName, subscriptionID, flagValues.RoleNames)

	if err != nil {
		return err
	}

	if err := createFederatedCredential(ctx,
		&msiService,
		repoSlug, copilotEnv, subscriptionID, authConfig.ResourceID); err != nil {
		return err
	}

	if err := setCopilotEnvVars(ctx, defaultGitHubCLI, repoSlug, *authConfig); err != nil {
		return err
	}

	if err := writeCopilotSetupStepsYaml(gitRepoRoot); err != nil {
		return err
	}

	remote, err := gitPushChanges(ctx, promptClient, gitCLI, defaultCommandRunner,
		gitRepoRoot,
		repoSlug,
		flagValues.BranchName)

	if err != nil {
		return fmt.Errorf("failed to push files to git: %w", err)
	}

	codingAgentURL := fmt.Sprintf("https://github.com/%s/settings/copilot/coding_agent#:~:text=JSON%%20MCP%%20configuration-,MCP%%20configuration,-1", repoSlug) //nolint:lll
	managedIdentityPortalURL := formatPortalLinkForManagedIdentity(tenantID, subscriptionID, authConfig.ResourceGroup, authConfig.Name)                          //nolint:lll

	fmt.Println("")
	fmt.Println(output.WithHighLightFormat("(!)"))
	fmt.Println(output.WithHighLightFormat("(!) NOTE: Some tasks must still be completed, manually:"))
	fmt.Println(output.WithHighLightFormat("(!)"))
	fmt.Println("")
	fmt.Printf("1. The branch created at %s/%s must be merged to %s/main\n", remote, flagValues.BranchName, repoSlug)
	fmt.Printf("2. Configure Copilot coding agent's managed identity roles in the Azure portal: %s\n", ux.Hyperlink(managedIdentityPortalURL)) // nolint:lll
	fmt.Printf("3. Visit '%s' and update the \"MCP configuration\" field with this JSON:\n\n", ux.Hyperlink(codingAgentURL))

	fmt.Println(mcpJson)

	if err := openBrowserWindows(ctx,
		promptClient,
		defaultConsole,
		codingAgentURL,
		repoSlug); err != nil {
		return err
	}

	return nil
}

func openBrowserWindows(ctx context.Context,
	prompter azdext.PromptServiceClient,
	defaultConsole input.Console,
	codingAgentURL string,
	repoSlug string) error {
	resp, err := prompter.Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Open browser window to create a pull request?",
			DefaultValue: to.Ptr(true),
		},
	})

	if err != nil {
		return fmt.Errorf("failed to get confirm response for browser/pr option: %w", err)
	}

	if !*resp.Value {
		return nil
	}

	fullURL := fmt.Sprintf("https://github.com/%s/compare/main...azd-enable-copilot-coding-agent-with-azure?body=%s&expand=1&title=%s",
		repoSlug,
		url.QueryEscape(fmt.Sprintf(prBodyMD, codingAgentURL, mcpJson)),
		url.QueryEscape("Updating/adding copilot-setup-steps.yaml to enable the Copilot coding agent to access Azure"),
	)

	openWithDefaultBrowser(ctx, defaultConsole, fullURL)

	// if we don't pause here, on Windows, it can kill the child process that's actually starting up the browser.
	time.Sleep(5 * time.Second)

	return nil
}

// promptForCodingAgentRepoSlug gets the repo slug (<owner>/<repository>) for the repository
// where the coding agent will run. This isn't necessarily the same as the repository the user
// normally works in if, for instance, they're working in a fork.
func promptForCodingAgentRepoSlug(ctx context.Context,
	promptClient azdext.PromptServiceClient,
	gitCLI gitCLI,
	gitRepoRoot string,
	repoSlug string,
) (string, error) {
	if repoSlug != "" {
		return repoSlug, nil
	}

	var choices []*azdext.SelectChoice

	remotes, err := listRemotes(ctx, gitCLI, gitRepoRoot)

	if err != nil {
		return "", err
	}

	var repoSlugs []string

	for _, remote := range remotes {
		remoteURL, err := gitCLI.GetRemoteUrl(context.Background(), gitRepoRoot, remote)

		if err != nil {
			return "", err
		}

		repoSlug, err := azd_github.GetSlugForRemote(remoteURL)

		if err != nil {
			return "", err
		}

		choices = append(choices, &azdext.SelectChoice{
			Label: remote + ": " + repoSlug,
		})

		repoSlugs = append(repoSlugs, repoSlug)
	}

	resp, err := promptClient.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Which GitHub repository will use the Copilot coding agent?",
			Choices: choices,
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to get selection: %w", err)
	}

	return repoSlugs[*resp.Value], nil
}

func listRemotes(ctx context.Context, gitCLI gitCLI, gitRepoRoot string) ([]string, error) {
	remotes, err := gitCLI.ListRemotes(ctx, gitRepoRoot)

	if err != nil {
		return nil, err
	}

	if len(remotes) == 0 {
		return nil, fmt.Errorf("no git remotes are configured")

	}

	return remotes, nil
}

func writeCopilotSetupStepsYaml(gitRepoRoot string) error {
	workflowsDir := filepath.Join(gitRepoRoot, ".github", "workflows")

	if err := os.MkdirAll(workflowsDir, 0755); err != nil {
		return fmt.Errorf("failed to create the %s folder: %w", workflowsDir, err)
	}

	// Create the copilot-setup-steps.yml file
	copilotSetupStepsPath := filepath.Join(workflowsDir, "copilot-setup-steps.yml")

	// Write the setup file
	//nolint:gosec // permissions are correct - owner can read and change the file, others can read it (it's not secret)
	if err := os.WriteFile(copilotSetupStepsPath, []byte(copilotSetupStepsYml), 0644); err != nil {
		return fmt.Errorf("failed to write copilot setup file: %w", err)
	}

	return nil
}

// newGitHubCLI is a thin wrapper around [azd_tools_github.NewGitHubCli], for testing
func newGitHubCLI(ctx context.Context, console input.Console, commandRunner azd_exec.CommandRunner) (githubCLI, error) {
	cli, err := azd_tools_github.NewGitHubCli(ctx, console, commandRunner)
	return cli, err
}

func newCommandRunner(showOutput bool) (azd_exec.CommandRunner, input.Console) {
	var commandRunner azd_exec.CommandRunner

	if showOutput {
		commandRunner = azd_exec.NewCommandRunner(&azd_exec.RunnerOptions{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		})
	} else {
		commandRunner = azd_exec.NewCommandRunner(&azd_exec.RunnerOptions{
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}

	console := input.NewConsole(true, true, input.Writers{
		Output:  os.Stdout,
		Spinner: os.Stdout,
	}, input.ConsoleHandles{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, &output.NoneFormatter{}, nil)

	return commandRunner, console
}

func setCopilotEnvVars(ctx context.Context, githubCLI githubCLI, repoSlug string, authConfig authConfiguration) error {
	taskList := ux.NewTaskList(&ux.TaskListOptions{
		Writer: os.Stdout,
	})

	if err := githubCLI.CreateEnvironmentIfNotExist(ctx, repoSlug, copilotEnv); err != nil {
		return fmt.Errorf("failed to create GitHub environment %s in repository %s: %w", copilotEnv, repoSlug, err)
	}

	varsToSet := map[string]string{
		"AZURE_CLIENT_ID":       authConfig.ClientId,
		"AZURE_TENANT_ID":       authConfig.TenantId,
		"AZURE_SUBSCRIPTION_ID": authConfig.SubscriptionId,
	}

	for name, value := range varsToSet {
		taskList.AddTask(ux.TaskOptions{
			Title: fmt.Sprintf("Set %s in copilot environment", name),
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if err := githubCLI.SetVariable(ctx,
					repoSlug,
					name,
					value,
					&azd_tools_github.SetVariableOptions{Environment: copilotEnv}); err != nil {
					return ux.Error, err
				}

				return ux.Success, nil
			},
		})
	}

	fmt.Println(output.WithHighLightFormat("Storing identity values in 'copilot' environment"))
	return taskList.Run()
}

type credentialProviderAdapter struct {
	tokenCred azcore.TokenCredential
}

//
//nolint:lll
func (cp *credentialProviderAdapter) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	return cp.tokenCred, nil
}

// createFederatedCredential creates a federated credential (allowing Copilot to authenticate and use Azure)
func createFederatedCredential(ctx context.Context,
	msiService azdMSIService,
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

// pickOrCreateMSI walks the user through creating an MSI
func pickOrCreateMSI(ctx context.Context,
	prompter azdext.PromptServiceClient,
	msiService azdMSIService,
	entraIDService entraid.EntraIdService,
	resourceService resourceService,
	identityName string, subscriptionId string, roleNames []string) (*authConfiguration, error) {

	// ************************** Pick or create a new MSI **************************

	// Prompt for pick or create a new MSI
	selectedOption, err := prompter.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Do you want to create a new Azure user-assigned managed identity or use an existing one?",
			Choices: []*azdext.SelectChoice{
				{Label: "Create new user-assigned managed identity"},
				{Label: "Use existing user-assigned managed identity"},
			},
			HelpMessage: "",
		},
	})
	if err != nil {
		//nolint:lll
		return nil, fmt.Errorf(
			"failed when prompting for managed identity option. Try logging in manually with 'azd auth login' before running this command. Error: %w",
			err,
		)
	}

	taskList := ux.NewTaskList(nil)

	var managedIdentity rm_armmsi.Identity

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
			//nolint:lll
			return nil, fmt.Errorf(
				"failed when prompting for MSI location. Try logging in manually with 'azd auth login' before running this command. Error: %w",
				err,
			)
		}

		shouldCreate, rgName, err := promptForResourceGroup(ctx, prompter, subscriptionId, location.Location.Name)

		if err != nil {
			return nil, err
		}

		var resourceGroupName string

		if shouldCreate {
			taskList.AddTask(ux.TaskOptions{
				Title: fmt.Sprintf("Creating resource group %s", rgName),
				Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
					createRGResp, err := resourceService.CreateOrUpdate(ctx, rgName, armresources.ResourceGroup{
						Location: &location.Location.Name,
					}, nil)

					if err != nil {
						return ux.Error, err
					}

					resourceGroupName = *createRGResp.Name
					return ux.Success, nil
				},
			})
		} else {
			resourceGroupName = rgName
		}

		taskList.AddTask(ux.TaskOptions{
			Title: fmt.Sprintf("Creating User Managed Identity (MSI) '%s'", identityName),
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				newMSI, err := msiService.CreateUserIdentity(ctx,
					subscriptionId,
					resourceGroupName,
					location.Location.Name,
					identityName)

				if err != nil {
					return ux.Error, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
				}

				managedIdentity = newMSI
				return ux.Success, nil
			},
		})
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
				Message: "Select an existing User Managed Identity (MSI) to use",
				Choices: choices,
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting for existing MSI: %w", err)
		}
		managedIdentity = msIdentities[*selectedOption.Value]
	}

	if err := taskList.Run(); err != nil {
		return nil, err
	}

	roleNameStrings := strings.Join(roleNames, ", ")
	parsedID, err := arm.ParseResourceID(*managedIdentity.ID)

	if err != nil {
		return nil, fmt.Errorf("invalid format for managed identity resource id: %w", err)
	}

	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Assigning roles (%s) to User Managed Identity (MSI)", roleNameStrings),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			err := entraIDService.EnsureRoleAssignments(
				ctx,
				subscriptionId,
				roleNames,
				&graphsdk.ServicePrincipal{
					Id:          managedIdentity.Properties.PrincipalID,
					DisplayName: *managedIdentity.Name,
				},
				&entraid.EnsureRoleAssignmentsOptions{
					Scope: to.Ptr(azure.ResourceGroupRID(subscriptionId, parsedID.ResourceGroupName)),
				},
			)

			if err != nil {
				return ux.Error, err
			}

			return ux.Success, nil
		},
	})

	if err := taskList.Run(); err != nil {
		return nil, fmt.Errorf("failed during identity creation: %w", err)
	}

	return &authConfiguration{
		Name:           *managedIdentity.Name,
		ResourceGroup:  parsedID.ResourceGroupName,
		TenantId:       *managedIdentity.Properties.TenantID,
		SubscriptionId: subscriptionId,
		ResourceID:     *managedIdentity.ID,
		ClientId:       *managedIdentity.Properties.ClientID,
	}, nil
}

func promptForResourceGroup(ctx context.Context,
	prompter azdext.PromptServiceClient,
	subscriptionId string, locationName string) (mustCreate bool, resourceGroupName string, err error) {
	rg, err := prompter.PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: subscriptionId,
				Location:       locationName,
			},
		},
	})

	if err != nil {
		return false, "", fmt.Errorf("failed trying to get a resource group name from prompt: %w", err)
	}

	// create resource group returns a sentinel value if the user chooses to create a resource group
	// but does NOT create it, so we'll have to do that here.

	if rg.ResourceGroup.Id != "new" {
		return false, rg.ResourceGroup.Name, nil
	} else {
		// user chose to create a group, let's take them through that flow
		rgPrompt, err := prompter.Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: "Enter a name for the new resource group",
			},
		})

		if err != nil {
			return false, "", err
		}

		return true, rgPrompt.Value, nil
	}
}

type authConfiguration struct {
	Name           string
	ResourceGroup  string
	ClientId       string
	SubscriptionId string
	TenantId       string
	ResourceID     string
}

// gitPushChanges walks the user through pushing a branch with their changes to git.
func gitPushChanges(ctx context.Context,
	prompter azdext.PromptServiceClient, gitCLI gitCLI, commandRunner azd_exec.CommandRunner,
	gitRepoRoot string, repoSlug string, branchName string,
) (remote string, err error) {
	copilotFileRelative := ".github/workflows/copilot-setup-steps.yml"

	chosenRemote := ""

	remotes, err := listRemotes(ctx, gitCLI, gitRepoRoot)

	if err != nil {
		return "", fmt.Errorf("failed to list git remotes for this repository: %w", err)
	}

	var choices []*azdext.SelectChoice

	for _, remote := range remotes {
		remoteURL, err := gitCLI.GetRemoteUrl(context.Background(), gitRepoRoot, remote)

		if err != nil {
			return "", err
		}

		choices = append(choices, &azdext.SelectChoice{
			Label: remote + ": " + remoteURL,
		})
	}

	choices = append(choices, &azdext.SelectChoice{
		Label: "None, I will push the changes manually",
	})

	resp, err := prompter.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: fmt.Sprintf("Which git repository should we push the '%s' branch to?", branchName),
			Choices: choices,
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to get selection: %w", err)
	}

	if int64(*resp.Value) == int64(len(choices)-1) {
		// they're going to do the push themselves.
		fmt.Println(
			output.WithWarningFormat(
				"(!) NOTE: copilot-setup-steps.yml must be committed to the main branch of %s before it will take effect!",
				repoSlug,
			),
		) //nolint:lll
		return "", nil
	}

	chosenRemote = remotes[*resp.Value]

	taskList := ux.NewTaskList(nil)

	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Creating/switch to branch (%s)", branchName),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			_, err := commandRunner.Run(ctx, azd_exec.RunArgs{
				Cmd:  "git",
				Args: []string{"checkout", "-B", branchName},
				Cwd:  gitRepoRoot,
			})

			if err != nil {
				return ux.Error, err
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Adding %s", copilotFileRelative),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			if err := gitCLI.AddFile(ctx, gitRepoRoot, copilotFileRelative); err != nil {
				return ux.Error, err
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Committing changes",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			if err := gitCLI.Commit(ctx, gitRepoRoot, "add copilot-setup-steps.yml"); err != nil {
				return ux.Error, err
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Pushing changes to %s/%s", chosenRemote, branchName),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			var lastErr error

			for range 3 {
				// copying this idea from azd pipeline config, which pushes multiple times
				// to allow for the "push once, fail, authenticate" workflow.
				if err := gitCLI.PushUpstream(ctx, gitRepoRoot, chosenRemote, branchName); err != nil {
					lastErr = err
					continue
				}

				return ux.Success, nil
			}

			return ux.Error, lastErr
		},
	})

	fmt.Printf("Committing and pushing changes to %s to git", copilotFileRelative)

	if err := taskList.Run(); err != nil {
		return "", err
	}

	return chosenRemote, nil
}

func loginToGitHubIfNeeded(
	ctx context.Context,
	githubHostName string,
	//nolint:lll
	newCommandRunnerFn func(showOutput bool) (azd_exec.CommandRunner, input.Console), // Just an alias of [newCommandRunner], for testing
	//nolint:lll
	newGitHubCLIFn func(ctx context.Context, console input.Console, commandRunner azd_exec.CommandRunner) (githubCLI, error), // Just an alias of [newGitHubCLI], for testing
) error {
	fmt.Println(output.WithBold("Checking if GitHub CLI is logged in..."))

	// when we're logging in we do actually need to show the output from the Github CLI command so we'll
	// use the console/commandRunner that's hooked up stdout and stderr.
	commandRunner, console := newCommandRunnerFn(true)
	githubCLI, err := newGitHubCLIFn(ctx, console, commandRunner)

	if err != nil {
		return fmt.Errorf("failed to get the interactive github CLI: %w", err)
	}

	authStatus, err := githubCLI.GetAuthStatus(ctx, githubHostName)

	if err != nil {
		return fmt.Errorf("failed when checking auth status for GitHub CLI: %w", err)
	}

	if !authStatus.LoggedIn {
		//nolint:lll
		fmt.Println(
			output.WithWarningFormat("(!) Not currently logged in GitHub CLI, attempting to login using `gh auth login`"),
		)

		if err := githubCLI.Login(context.Background(), githubHostName); err != nil {
			return err
		}
	}

	fmt.Println(output.WithSuccessFormat("✓ GitHub CLI is logged in"))
	return nil
}

type internalGitCLI struct {
	*azd_git.Cli
	commandRunner azd_exec.CommandRunner
}

var _ gitCLI = &internalGitCLI{}

func newInternalGitCLI(commandRunner azd_exec.CommandRunner) *internalGitCLI {
	gitCLI := azd_git.NewCli(commandRunner)

	return &internalGitCLI{
		Cli:           gitCLI,
		commandRunner: commandRunner,
	}
}

func (cli *internalGitCLI) ListRemotes(ctx context.Context, gitRepoRoot string) ([]string, error) {
	runResult, err := cli.commandRunner.Run(ctx, azd_exec.RunArgs{
		Cmd:  "git",
		Args: []string{"remote"},
		Cwd:  gitRepoRoot,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get list of git remotes for the current repository: %w", err)
	}

	remotes := strings.Split(strings.TrimSpace(runResult.Stdout), "\n")

	if len(remotes) == 1 && remotes[0] == "" {
		return nil, nil
	}

	return remotes, nil
}

// formatPortalLinkForManagedIdentity takes you to the Azure portal blade, for your managed identity,
// that lets you see its role assignments.
func formatPortalLinkForManagedIdentity(tenantID string,
	subscriptionID string,
	resourceGroupName string,
	managedIdentityName string) string {
	//nolint:lll
	return fmt.Sprintf("https://portal.azure.com/#@%s/resource/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s/azure_resources",
		tenantID,
		subscriptionID,
		resourceGroupName,
		managedIdentityName)
}
