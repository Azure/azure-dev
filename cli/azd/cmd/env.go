// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func envActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("env", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "env",
			Short: "Manage environments (ex: default environment, environment variables).",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdEnvHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	group.Add("set", &actions.ActionDescriptorOptions{
		Command:        newEnvSetCmd(),
		FlagsResolver:  newEnvSetFlags,
		ActionResolver: newEnvSetAction,
	})

	group.Add("set-secret", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "set-secret <name>",
			Short: "Set a name as a reference to a Key Vault secret in the environment.",
			Long: "You can either create a new Key Vault secret or select an existing one.\n" +
				"The provided name is the key for the .env file which holds the secret reference to the Key Vault secret.",
		},
		FlagsResolver:  newEnvSetSecretFlags,
		ActionResolver: newEnvSetSecretAction,
	})

	group.Add("select", &actions.ActionDescriptorOptions{
		Command:        newEnvSelectCmd(),
		ActionResolver: newEnvSelectAction,
	})

	group.Add("new", &actions.ActionDescriptorOptions{
		Command:        newEnvNewCmd(),
		FlagsResolver:  newEnvNewFlags,
		ActionResolver: newEnvNewAction,
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command:        newEnvListCmd(),
		ActionResolver: newEnvListAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	group.Add("refresh", &actions.ActionDescriptorOptions{
		Command:        newEnvRefreshCmd(),
		FlagsResolver:  newEnvRefreshFlags,
		ActionResolver: newEnvRefreshAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("get-values", &actions.ActionDescriptorOptions{
		Command:        newEnvGetValuesCmd(),
		FlagsResolver:  newEnvGetValuesFlags,
		ActionResolver: newEnvGetValuesAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.EnvVarsFormat},
		DefaultFormat:  output.EnvVarsFormat,
	})

	group.Add("get-value", &actions.ActionDescriptorOptions{
		Command:        newEnvGetValueCmd(),
		FlagsResolver:  newEnvGetValueFlags,
		ActionResolver: newEnvGetValueAction,
	})

	return group
}

func newEnvSetFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envSetFlags {
	flags := &envSetFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [<key> <value>] | [<key>=<value> ...] | [--file <filepath>]",
		Short: "Set one or more environment values.",
		Long:  "Set one or more environment values using key-value pairs or by loading from a .env formatted file.",
		Args:  cobra.ArbitraryArgs,
		// Sample arguments used in tests
		Annotations: map[string]string{
			"azdtest.use": "set key value",
		},
	}
}

type envSetFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
	file   string
}

func (f *envSetFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	local.StringVar(&f.file, "file", "", "Path to .env formatted file to load environment values from.")
	f.global = global
}

type envSetAction struct {
	console    input.Console
	azdCtx     *azdcontext.AzdContext
	env        *environment.Environment
	envManager environment.Manager
	flags      *envSetFlags
	args       []string
}

func newEnvSetAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	flags *envSetFlags,
	args []string,
) actions.Action {
	return &envSetAction{
		console:    console,
		azdCtx:     azdCtx,
		env:        env,
		envManager: envManager,
		flags:      flags,
		args:       args,
	}
}

func (e *envSetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// To track case conflicts
	dotEnv := e.env.Dotenv()
	keyValues := make(map[string]string)

	// Handle file input if specified
	if e.flags.file != "" {
		if len(e.args) > 0 {
			return nil, fmt.Errorf("cannot combine --file flag with key-value arguments")
		}
		filename := e.flags.file
		file, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
		}
		defer file.Close()

		keyValues, err = godotenv.Parse(file)
		if err != nil {
			return nil, fmt.Errorf("failed to parse file %s: %w", filename, err)
		}
	} else if len(e.args) == 0 {
		//nolint:lll
		return nil, fmt.Errorf("no environment values provided. Use '<key> <value>', '<key>=<value>', or '--file <filepath>'")
	} else if len(e.args) == 2 && !strings.Contains(e.args[0], "=") {
		// Handle single key-value pair format: azd env set key value
		key := e.args[0]
		value := e.args[1]
		keyValues[key] = value
	} else {
		// Handle key=value format: azd env set key=value [key2=value2 ...]
		for _, arg := range e.args {
			key, value, err := parseKeyValue(arg)
			if err != nil {
				return nil, err
			}
			keyValues[key] = value
		}
	}

	// No environment values to set
	if len(keyValues) == 0 {
		return nil, fmt.Errorf("no environment values to set")
	}

	// Apply the values
	for key, value := range keyValues {
		warnKeyCaseConflicts(ctx, e.console, dotEnv, key)
		e.env.DotenvSet(key, value)
		// Update to check case conflicts in subsequent keys
		dotEnv[key] = value
	}

	if err := e.envManager.Save(ctx, e.env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return nil, nil
}

// parseKeyValue parses a key=value string and returns the key and value parts
func parseKeyValue(arg string) (string, string, error) {
	parts := strings.SplitN(arg, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid key=value format: %s", arg)
	}
	key := parts[0]
	value := parts[1]
	return key, value, nil
}

// Prints a warning message if there are any case-insensitive conflicts with the provided key
func warnKeyCaseConflicts(
	ctx context.Context,
	console input.Console,
	dotEnv map[string]string,
	key string) {
	var conflicts []string
	for k := range dotEnv {
		if strings.EqualFold(k, key) && k != key {
			conflicts = append(conflicts, "'"+k+"'")
		}
	}

	if len(conflicts) == 1 {
		console.MessageUxItem(ctx,
			&ux.WarningMessage{
				Description: fmt.Sprintf(
					"'%s' already exists as %s. Did you mean to set %s instead?",
					key,
					conflicts[0],
					conflicts[0]),
			})
	} else if len(conflicts) > 1 {
		slices.Sort(conflicts)

		console.MessageUxItem(ctx,
			&ux.WarningMessage{
				Description: fmt.Sprintf(
					"'%s' already exists as %s",
					key,
					ux.ListAsText(conflicts)),
			})
	}
}

func newEnvSetSecretFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envSetSecretFlags {
	flags := &envSetSecretFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type envSetSecretFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (f *envSetSecretFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global
}

type envSetSecretAction struct {
	console             input.Console
	azdCtx              *azdcontext.AzdContext
	env                 *environment.Environment
	envManager          environment.Manager
	flags               *envSetFlags
	args                []string
	prompter            prompt.Prompter
	kvService           keyvault.KeyVaultService
	entraIdService      entraid.EntraIdService
	subResolver         account.SubscriptionTenantResolver
	userProfileService  *azapi.UserProfileService
	alphaFeatureManager *alpha.FeatureManager
	projectConfig       *project.ProjectConfig
}

func (e *envSetSecretAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(e.args) < 1 {
		return nil, fmt.Errorf(
			"no <name> provided. Please provide a name as argument like: 'azd env set-secret <name>'")
	}
	secretName := e.args[0]

	// When no interactive is supported in the terminal azd will not add numbers to the list when
	// asking to select options. For example, instead of showing "1. Option 1", it will show "Option 1". This is useful
	// when the user wants to prefill the selection in stdin before calling azd env set-secret (e.g. in a script).
	listWithoutNumbers := !e.console.IsSpinnerInteractive()

	createNewStrategy := "Create a new Key Vault secret"
	selectExistingStrategy := "Select an existing Key Vault secret"
	setSecretStrategies := []string{createNewStrategy, selectExistingStrategy}
	selectedStrategyIndex, err := e.console.Select(
		ctx,
		input.ConsoleOptions{
			Message:      "Select how you want to set " + secretName,
			Options:      setSecretStrategies,
			DefaultValue: createNewStrategy,
			Help: "When creating a new Key Vault secret, you can either create a new Key Vault or" +
				" pick an existing one. A Key Vault secret belongs to a Key Vault.",
		})
	if err != nil {
		return nil, fmt.Errorf("selecting secret setting strategy: %w", err)
	}

	willCreateNewSecret := setSecretStrategies[selectedStrategyIndex] == createNewStrategy

	createSuccessResult := func(secretName, kvSecretName, kvName string) *actions.ActionResult {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: fmt.Sprintf("The key %s was saved in the environment as a reference to the"+
					" Key Vault secret %s from the Key Vault %s",
					output.WithBackticks(secretName),
					output.WithBackticks(kvSecretName),
					output.WithBackticks(kvName)),
				FollowUp: fmt.Sprintf("Learn how to use Key Vault secrets with azd and more: %s",
					output.WithLinkFormat("https://aka.ms/azd-env-set-secret")),
			},
		}
	}

	// Provide shortcuts for using the Key Vault created by composability (azd add)
	if kvId, hasComposeKv := e.env.LookupEnv("AZURE_RESOURCE_VAULT_ID"); hasComposeKv { // KV is provisioned
		resId, err := arm.ParseResourceID(kvId)
		if err != nil {
			return nil, fmt.Errorf("parsing key vault resource id: %w", err)
		}
		kvName := resId.Name
		kvSubId := resId.SubscriptionID
		subscriptionOptions := []string{"Yes", "No, use different key vault"}
		useProjectKvPrompt, err := e.console.Select(
			ctx,
			input.ConsoleOptions{
				Message:      "Key vault detected in this project. Use this key vault?",
				Options:      subscriptionOptions,
				DefaultValue: subscriptionOptions[0],
			})

		if err != nil {
			return nil, fmt.Errorf("selecting key vault option: %w", err)
		}

		if useProjectKvPrompt == 0 { // Use project Key Vault
			kvAccount := keyvault.Vault{
				Name: kvName,
				Id:   kvId,
			}

			var kvSecretName string
			if willCreateNewSecret {
				kvSecretName, err = e.createNewKeyVaultSecret(ctx, secretName, kvSubId, kvAccount.Name)

			} else {
				kvSecretName, err = e.selectKeyVaultSecret(ctx, kvSubId, kvAccount.Name)
			}
			if err != nil {
				return nil, err
			}

			envValue := keyvault.NewAzureKeyVaultSecret(kvSubId, kvAccount.Name, kvSecretName)
			e.env.DotenvSet(secretName, envValue)
			if err := e.envManager.Save(ctx, e.env); err != nil {
				return nil, fmt.Errorf("saving environment: %w", err)
			}

			return createSuccessResult(secretName, kvSecretName, kvAccount.Name), nil
		}
	} else if _, hasProjectKv := e.projectConfig.Resources["vault"]; hasProjectKv { // KV defined but not provisioned yet
		e.console.Message(ctx,
			output.WithWarningFormat("\nAn existing project key vault is defined but is not provisioned yet. ")+
				fmt.Sprintf("Run '%s' first to use it.\n", output.WithHighLightFormat("azd provision")))
		options := []string{"Use a different key vault", "Cancel"}
		useProjectKvPrompt, err := e.console.Select(
			ctx,
			input.ConsoleOptions{
				Message:      "How do you want to proceed?",
				Options:      options,
				DefaultValue: options[0],
			})

		if err != nil {
			return nil, fmt.Errorf("selecting key vault option: %w", err)
		}
		if useProjectKvPrompt == 1 { // Cancel
			return nil, fmt.Errorf("operation cancelled. Run 'azd provision' to provision the project Key Vault first")
		}
	}

	subscriptionNote := "\nYou can set the Key Vault secret from any Azure subscription where you have access to."
	e.console.Message(ctx, subscriptionNote)

	// default messages based on willCreateNewSecret == true
	pickSubscription := "Select the subscription where you want to create the Key Vault secret"
	pickKvAccount := "Select the Key Vault where you want to create the Key Vault secret"

	if !willCreateNewSecret {
		// reassign messages for selecting existing secret
		pickSubscription = "Select the subscription where the Key Vault secret is"
		pickKvAccount = "Select the Key Vault where the Key Vault secret is"
	}

	subId, err := e.prompter.PromptSubscription(ctx, pickSubscription)
	if err != nil {
		return nil, fmt.Errorf("prompting for subscription: %w", err)
	}
	tenantId, err := e.subResolver.LookupTenant(ctx, subId)
	if err != nil {
		return nil, fmt.Errorf("looking up tenant for subscription: %w", err)
	}

	e.console.ShowSpinner(ctx, "Finding Key Vaults from the selected subscription", input.Step)
	vaultsList, err := e.kvService.ListSubscriptionVaults(ctx, subId)
	if err != nil {
		return nil, fmt.Errorf("getting the list of Key Vaults: %w", err)
	}
	// prompt for vault selection
	e.console.StopSpinner(ctx, "", input.Step)

	atLeastOneKvAccountExists := len(vaultsList) > 0
	if !atLeastOneKvAccountExists && !willCreateNewSecret {
		e.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: "No Azure Key Vaults were found in the selected subscription",
		})
		// update the flow to offer creating a new Key Vault
		willCreateNewSecret = true
	}

	createNewKvAccountOption := "Create a new Key Vault"
	selectKvAccountOptions := []string{}

	// Create a combined list with "Create a new Key Vault" as the first option
	if willCreateNewSecret {
		if listWithoutNumbers {
			selectKvAccountOptions = append(selectKvAccountOptions, createNewKvAccountOption)
		} else {
			selectKvAccountOptions = append(selectKvAccountOptions, fmt.Sprintf("%2d. %s", 1, createNewKvAccountOption))
		}
	}

	// Add the existing vaults with adjusted numbering
	for index, vault := range vaultsList {
		if listWithoutNumbers {
			selectKvAccountOptions = append(selectKvAccountOptions, vault.Name)
		} else {
			offset := 1
			// Existing KVs start at #2 since #1 will be "Create a new Key Vault"
			if willCreateNewSecret {
				offset = 2
			}
			selectKvAccountOptions = append(selectKvAccountOptions, fmt.Sprintf("%2d. %s", index+offset, vault.Name))
		}
	}

	kvAccountSelectionIndex, err := e.console.Select(ctx, input.ConsoleOptions{
		Message:      pickKvAccount,
		Options:      selectKvAccountOptions,
		DefaultValue: selectKvAccountOptions[0],
	})
	if err != nil {
		return nil, fmt.Errorf("selecting Key Vault: %w", err)
	}

	willCreateNewKvAccount := false
	if willCreateNewSecret {
		willCreateNewKvAccount = kvAccountSelectionIndex == 0
		if !willCreateNewKvAccount {
			// when willCreateNewSecret is true, we added a new option at the beginning of the list
			// to recover the original kv account name
			kvAccountSelectionIndex--
		}
	}

	var kvAccount keyvault.Vault
	if atLeastOneKvAccountExists {
		kvAccount = vaultsList[kvAccountSelectionIndex]
	}

	if willCreateNewKvAccount {
		location, err := e.prompter.PromptLocation(
			ctx, subId, "Select the location to create the Key Vault", nil, nil)
		if err != nil {
			return nil, fmt.Errorf("prompting for Key Vault location: %w", err)
		}
		rg, err := e.prompter.PromptResourceGroupFrom(ctx, subId, location, prompt.PromptResourceGroupFromOptions{
			DefaultName:          "rg-for-my-key-vault",
			NewResourceGroupHelp: "The name of the new resource group where the Key Vault will be created.",
		})
		if err != nil {
			return nil, fmt.Errorf("prompting for resource group: %w", err)
		}

		kvAccountName := ""
		for {
			kvAccountNameInput, err := e.console.Prompt(ctx, input.ConsoleOptions{
				Message: "Enter a name for the Key Vault",
				Help:    "The name must be unique within the subscription and must be between 3 and 24 characters long",
			})
			if err != nil {
				return nil, fmt.Errorf("prompting for Key Vault name: %w", err)
			}
			if kvAccountNameInput == "" {
				e.console.Message(ctx, "Key Vault name cannot be empty")
				continue
			}
			kvAccountName = kvAccountNameInput
			break
		}

		e.console.ShowSpinner(ctx, "Creating Key Vault", input.Step)
		vault, err := e.kvService.CreateVault(ctx, tenantId, subId, rg, location, kvAccountName)
		e.console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("error creating Key Vault: %w", err)
		}
		kvAccount = vault

		// RBAC role assignment
		e.console.ShowSpinner(ctx, "Adding Administrator Role", input.Step)
		principalId, err := azureutil.GetCurrentPrincipalId(ctx, e.userProfileService, tenantId)
		if err != nil {
			return nil, fmt.Errorf("getting current principal ID: %w", err)
		}
		err = e.entraIdService.CreateRbac(
			ctx, subId, kvAccount.Id, keyvault.RoleIdKeyVaultAdministrator, principalId)
		if err != nil {
			return nil, fmt.Errorf("adding Administrator Role: %w", err)
		}
		e.console.StopSpinner(ctx, "", input.Step)
	}

	var kvSecretName string
	if willCreateNewSecret {
		kvSecretName, err = e.createNewKeyVaultSecret(ctx, secretName, subId, kvAccount.Name)
	} else {
		kvSecretName, err = e.selectKeyVaultSecret(ctx, subId, kvAccount.Name)
	}
	if err != nil {
		return nil, err
	}

	// akvs -> Azure Key Vault Secret (akvs://<subId>/<keyvault-name>/<secret-name>)
	envValue := keyvault.NewAzureKeyVaultSecret(subId, kvAccount.Name, kvSecretName)
	e.env.DotenvSet(secretName, envValue)
	if err := e.envManager.Save(ctx, e.env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return createSuccessResult(secretName, kvSecretName, kvAccount.Name), nil
}

// createNewKeyVaultSecret creates a new secret in an Azure Key Vault and returns the name of the created secret.
func (e *envSetSecretAction) createNewKeyVaultSecret(ctx context.Context, secretName, subId, kvName string) (string, error) {
	var kvSecretName string
	var err error

	for {
		kvSecretName, err = e.console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter a name for the Key Vault secret",
			DefaultValue: strings.ReplaceAll(secretName, "_", "-") + "-kv-secret",
		})
		if err != nil {
			return "", fmt.Errorf("prompting for Key Vault secret name: %w", err)
		}
		if keyvault.IsValidSecretName(kvSecretName) {
			break
		}
		e.console.Message(ctx, "Invalid Key Vault secret name. The name must be between 1 and 127 characters"+
			" long and can contain only alphanumeric characters and dashes.")
	}

	kvSecretValue, err := e.console.Prompt(ctx, input.ConsoleOptions{
		Message:    "Enter the value for the Key Vault secret",
		IsPassword: true,
	})
	if err != nil {
		return "", fmt.Errorf("prompting for secret value: %w", err)
	}

	// Creating a secret in a new account too soon can fail due to rbac role assignment not being ready
	err = retry.Do(
		ctx,
		retry.WithMaxRetries(3, retry.NewConstant(5*time.Second)),
		func(ctx context.Context) error {
			err = e.kvService.CreateKeyVaultSecret(ctx, subId, kvName, kvSecretName, kvSecretValue)
			if err != nil {
				return retry.RetryableError(fmt.Errorf("creating Key Vault secret: %w", err))
			}
			return nil
		},
	)
	if err != nil {
		return "", fmt.Errorf("setting Key Vault secret: %w", err)
	}

	return kvSecretName, nil
}

// selectKeyVaultSecret presents a selection list of secrets from the specified Key Vault and
// returns the selected secret name.
func (e *envSetSecretAction) selectKeyVaultSecret(ctx context.Context, subId string, kvName string) (string, error) {
	listWithoutNumbers := !e.console.IsSpinnerInteractive()

	secretsInKv, err := e.kvService.ListKeyVaultSecrets(ctx, subId, kvName)
	if err != nil {
		return "", fmt.Errorf("listing Key Vault secrets: %w", err)
	}
	if len(secretsInKv) == 0 {
		return "", fmt.Errorf("no Key Vault secrets were found in the selected Key Vault")
	}

	options := make([]string, len(secretsInKv))
	for i, secret := range secretsInKv {
		if listWithoutNumbers {
			options[i] = secret
		} else {
			options[i] = fmt.Sprintf("%2d. %s", i+1, secret)
		}
	}

	secretSelectionIndex, err := e.console.Select(ctx, input.ConsoleOptions{
		Message:      "Select the Key Vault secret",
		Options:      options,
		DefaultValue: options[0],
	})
	if err != nil {
		return "", fmt.Errorf("selecting Key Vault secret: %w", err)
	}

	return secretsInKv[secretSelectionIndex], nil
}

func newEnvSetSecretAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	flags *envSetFlags,
	args []string,
	prompter prompt.Prompter,
	kvService keyvault.KeyVaultService,
	entraIdService entraid.EntraIdService,
	subResolver account.SubscriptionTenantResolver,
	userProfileService *azapi.UserProfileService,
	alphaFeatureManager *alpha.FeatureManager,
	projectConfig *project.ProjectConfig,
) actions.Action {
	return &envSetSecretAction{
		console:             console,
		azdCtx:              azdCtx,
		env:                 env,
		envManager:          envManager,
		flags:               flags,
		args:                args,
		prompter:            prompter,
		kvService:           kvService,
		entraIdService:      entraIdService,
		subResolver:         subResolver,
		userProfileService:  userProfileService,
		alphaFeatureManager: alphaFeatureManager,
		projectConfig:       projectConfig,
	}
}

func newEnvSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select <environment>",
		Short: "Set the default environment.",
		Args:  cobra.ExactArgs(1),
	}
}

type envSelectAction struct {
	azdCtx     *azdcontext.AzdContext
	envManager environment.Manager
	args       []string
}

func newEnvSelectAction(azdCtx *azdcontext.AzdContext, envManager environment.Manager, args []string) actions.Action {
	return &envSelectAction{
		azdCtx:     azdCtx,
		envManager: envManager,
		args:       args,
	}
}

func (e *envSelectAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	_, err := e.envManager.Get(ctx, e.args[0])
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`environment '%s' does not exist. You can create it with "azd env new %s"`,
			e.args[0],
			e.args[0],
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	if err := e.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: e.args[0]}); err != nil {
		return nil, fmt.Errorf("setting default environment: %w", err)
	}

	return nil, nil
}

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List environments.",
		Aliases: []string{"ls"},
	}
}

type envListAction struct {
	envManager environment.Manager
	azdCtx     *azdcontext.AzdContext
	formatter  output.Formatter
	writer     io.Writer
}

func newEnvListAction(
	envManager environment.Manager,
	azdCtx *azdcontext.AzdContext,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &envListAction{
		envManager: envManager,
		azdCtx:     azdCtx,
		formatter:  formatter,
		writer:     writer,
	}
}

func (e *envListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	envs, err := e.envManager.List(ctx)

	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}

	if e.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "NAME",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "DEFAULT",
				ValueTemplate: "{{.IsDefault}}",
			},
			{
				Heading:       "LOCAL",
				ValueTemplate: "{{.HasLocal}}",
			},
			{
				Heading:       "REMOTE",
				ValueTemplate: "{{.HasRemote}}",
			},
		}

		err = e.formatter.Format(envs, e.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = e.formatter.Format(envs, e.writer, nil)
	}
	if err != nil {
		return nil, err
	}

	return nil, nil
}

type envNewFlags struct {
	subscription string
	location     string
	global       *internal.GlobalCommandOptions
}

func (f *envNewFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&f.subscription,
		"subscription",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&f.location, "location", "l", "", "Azure location for the new environment")

	f.global = global
}

func newEnvNewFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envNewFlags {
	flags := &envNewFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <environment>",
		Short: "Create a new environment and set it as the default.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type envNewAction struct {
	azdCtx     *azdcontext.AzdContext
	envManager environment.Manager
	flags      *envNewFlags
	args       []string
	console    input.Console
}

func newEnvNewAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	flags *envNewFlags,
	args []string,
	console input.Console,
) actions.Action {
	return &envNewAction{
		azdCtx:     azdCtx,
		envManager: envManager,
		flags:      flags,
		args:       args,
		console:    console,
	}
}

func (en *envNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	environmentName := ""
	if len(en.args) >= 1 {
		environmentName = en.args[0]
	}

	envSpec := environment.Spec{
		Name:         environmentName,
		Subscription: en.flags.subscription,
		Location:     en.flags.location,
	}

	env, err := en.envManager.Create(ctx, envSpec)
	if err != nil {
		return nil, fmt.Errorf("creating new environment: %w", err)
	}

	envs, err := en.envManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}

	if len(envs) == 1 {
		// If this is the only environment, set it as the default environment
		if err := en.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}); err != nil {
			return nil, fmt.Errorf("saving default environment: %w", err)
		}
		en.console.Message(ctx,
			fmt.Sprintf("New environment '%s' was set as default", env.Name()),
		)
	} else {
		// Ask the user if they want to set the new environment as the default environment
		msg := fmt.Sprintf("Set new environment '%s' as default environment?", env.Name())
		shouldSetDefault, promptErr := en.console.Confirm(ctx, input.ConsoleOptions{
			Message:      msg,
			DefaultValue: true,
		})

		if promptErr != nil {
			return nil, fmt.Errorf("prompting to set environment '%s' as default environment: %w", env.Name(), promptErr)
		}

		if shouldSetDefault {
			if err := en.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}); err != nil {
				return nil, fmt.Errorf("saving default environment: %w", err)
			}
			en.console.Message(ctx,
				fmt.Sprintf("\nNew environment '%s' created and set as default", env.Name()),
			)
		} else {
			defaultEnvironment, err := en.azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				return nil, fmt.Errorf("get default environment: %w", err)
			}
			en.console.Message(ctx,
				fmt.Sprintf("\nNew env '%s' created, default environment remains '%s'", env.Name(), defaultEnvironment),
			)
		}
	}

	return nil, nil
}

var featLayers = alpha.MustFeatureKey("layers")

type envRefreshFlags struct {
	hint   string
	layer  string
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (er *envRefreshFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(&er.hint, "hint", "", "", "Hint to help identify the environment to refresh")
	local.StringVarP(&er.layer, "layer", "", "", "Provisioning layer to refresh the environment from.")
	_ = local.MarkHidden("layer") // alpha: featLayers

	er.EnvFlag.Bind(local, global)
	er.global = global
}

func newEnvRefreshFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envRefreshFlags {
	flags := &envRefreshFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvRefreshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh <environment>",
		Short: "Refresh environment values by using information from a previous infrastructure provision.",

		// We want to support the usual -e / --environment arguments as all our commands which take environments do, but for
		// ergonomics, we'd also like you to be able to run `azd env refresh some-environment-name` to behave the same way as
		// `azd env refresh -e some-environment-name` would have.
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return err
			}

			if len(args) == 0 {
				return nil
			}

			if flagValue, err := cmd.Flags().GetString(internal.EnvironmentNameFlagName); err == nil {
				if flagValue != "" && args[0] != flagValue {
					return errors.New(
						"the --environment flag and an explicit environment name as an argument may not be used together")
				}
			}

			return cmd.Flags().Set(internal.EnvironmentNameFlagName, args[0])
		},
		Annotations: map[string]string{},
	}

	// This is like the Use property above, but does not include the hint to show an environment name is supported. This
	// is used by some tests which need to construct a valid command line to run `azd` and here using `<environment>` would
	// be invalid, since it is an invalid name.
	cmd.Annotations["azdtest.use"] = "refresh"
	return cmd
}

type envRefreshAction struct {
	provisionManager    *provisioning.Manager
	projectConfig       *project.ProjectConfig
	projectManager      project.ProjectManager
	env                 *environment.Environment
	envManager          environment.Manager
	prompters           prompt.Prompter
	flags               *envRefreshFlags
	console             input.Console
	formatter           output.Formatter
	writer              io.Writer
	importManager       *project.ImportManager
	alphaFeatureManager *alpha.FeatureManager
}

func newEnvRefreshAction(
	provisionManager *provisioning.Manager,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	env *environment.Environment,
	envManager environment.Manager,
	prompters prompt.Prompter,
	flags *envRefreshFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	importManager *project.ImportManager,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &envRefreshAction{
		provisionManager:    provisionManager,
		projectManager:      projectManager,
		env:                 env,
		envManager:          envManager,
		prompters:           prompters,
		console:             console,
		flags:               flags,
		formatter:           formatter,
		projectConfig:       projectConfig,
		writer:              writer,
		importManager:       importManager,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (ef *envRefreshAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	ef.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Refreshing environment %s (azd env refresh)", ef.env.Name()),
	})

	if err := ef.projectManager.Initialize(ctx, ef.projectConfig); err != nil {
		return nil, err
	}

	if err := ef.projectManager.EnsureAllTools(ctx, ef.projectConfig, nil); err != nil {
		return nil, err
	}

	infra, err := ef.importManager.ProjectInfrastructure(ctx, ef.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	if len(infra.Options.Layers) > 1 {
		if !ef.alphaFeatureManager.IsEnabled(featLayers) {
			return nil,
				fmt.Errorf("Layered provisioning is not enabled. Run '%s' to enable it.", alpha.GetEnableCommand(featLayers))
		}

		ef.console.WarnForFeature(ctx, featLayers)
	}

	layers := infra.Options.GetLayers()
	if ef.flags.layer != "" {
		layerOpt, err := infra.Options.GetLayer(ef.flags.layer)
		if err != nil {
			return nil, err
		}
		layers = []provisioning.Options{layerOpt}
	}

	// If resource group is defined within the project but not in the environment then
	// add it to the environment to support BYOI lookup scenarios like ADE
	// Infra providers do not currently have access to project configuration
	projectResourceGroup, _ := ef.projectConfig.ResourceGroupName.Envsubst(ef.env.Getenv)
	if _, has := ef.env.LookupEnv(environment.ResourceGroupEnvVarName); !has && projectResourceGroup != "" {
		ef.env.DotenvSet(environment.ResourceGroupEnvVarName, projectResourceGroup)
	}

	var state provisioning.State
	for _, layer := range layers {
		if ef.flags.layer != "" || len(layers) > 1 {
			ef.console.EnsureBlankLine(ctx)
			ef.console.Message(ctx, fmt.Sprintf("Layer: %s", output.WithHighLightFormat(layer.Name)))
			ef.console.Message(ctx, "")
		}

		// env refresh supports "BYOI" infrastructure where bicep isn't available
		err = ef.provisionManager.Initialize(ctx, ef.projectConfig.Path, layer)
		if errors.Is(err, bicep.ErrEnsureEnvPreReqBicepCompileFailed) {
			// If bicep is not available, we continue to prompt for subscription and location unfiltered
			err = provisioning.EnsureSubscriptionAndLocation(ctx, ef.envManager, ef.env, ef.prompters,
				provisioning.EnsureSubscriptionAndLocationOptions{})
			if err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, fmt.Errorf("initializing provisioning manager: %w", err)
		}

		stateOptions := provisioning.NewStateOptions(ef.flags.hint)
		result, err := ef.provisionManager.State(ctx, stateOptions)
		if err != nil {
			return nil, fmt.Errorf("getting deployment: %w", err)
		}

		if err := provisioning.UpdateEnvironment(ctx, result.State.Outputs, ef.env, ef.envManager); err != nil {
			return nil, err
		}

		state.MergeInto(*result.State)
	}

	if ef.formatter.Kind() == output.JsonFormat {
		err = ef.formatter.Format(provisioning.NewEnvRefreshResultFromState(&state), ef.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("writing deployment result in JSON format: %w", err)
		}
	}

	servicesStable, err := ef.importManager.ServiceStable(ctx, ef.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range servicesStable {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project:        ef.projectConfig,
			Service:        svc,
			ServiceContext: project.NewServiceContext(),
			Args: map[string]any{
				"bicepOutput": state.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, err
		}
	}

	localEnvPath := ef.envManager.EnvPath(ef.env)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Environment refresh completed",
			FollowUp: fmt.Sprintf("View environment variables at %s", output.WithHyperlink(localEnvPath, localEnvPath)),
		},
	}, nil
}

func newEnvGetValuesFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envGetValuesFlags {
	flags := &envGetValuesFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvGetValuesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-values",
		Short: "Get all environment values.",
	}
}

type envGetValuesFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (eg *envGetValuesFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	eg.EnvFlag.Bind(local, global)
	eg.global = global
}

type envGetValuesAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	envManager environment.Manager
	formatter  output.Formatter
	writer     io.Writer
	flags      *envGetValuesFlags
}

func newEnvGetValuesAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	flags *envGetValuesFlags,
) actions.Action {
	return &envGetValuesAction{
		azdCtx:     azdCtx,
		console:    console,
		envManager: envManager,
		formatter:  formatter,
		writer:     writer,
		flags:      flags,
	}
}

func (eg *envGetValuesAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	name, err := eg.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	// Note: if there is not an environment yet, GetDefaultEnvironmentName() returns empty string (not error)
	// and later, when envManager.Get() is called with the empty string, azd returns an error.
	// But if there is already an environment (default to be selected), azd must honor the --environment flag
	// over the default environment.
	if eg.flags.EnvironmentName != "" {
		name = eg.flags.EnvironmentName
	}
	env, err := eg.envManager.Get(ctx, name)
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`"environment does not exist. You can create it with "azd env new"`,
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	return nil, eg.formatter.Format(env.Dotenv(), eg.writer, nil)
}

func newEnvGetValueFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envGetValueFlags {
	flags := &envGetValueFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvGetValueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-value <keyName>",
		Short: "Get specific environment value.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type envGetValueFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (eg *envGetValueFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	eg.EnvFlag.Bind(local, global)
	eg.global = global
}

type envGetValueAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	envManager environment.Manager
	writer     io.Writer
	flags      *envGetValueFlags
	args       []string
}

func newEnvGetValueAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	console input.Console,
	writer io.Writer,
	flags *envGetValueFlags,
	args []string,

) actions.Action {
	return &envGetValueAction{
		azdCtx:     azdCtx,
		console:    console,
		envManager: envManager,
		writer:     writer,
		flags:      flags,
		args:       args,
	}
}

func (eg *envGetValueAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(eg.args) < 1 {
		return nil, fmt.Errorf("no key name provided")
	}

	keyName := eg.args[0]

	name, err := eg.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	// Note: if there is not an environment yet, GetDefaultEnvironmentName() returns empty string (not error)
	// and later, when envManager.Get() is called with the empty string, azd returns an error.
	// But if there is already an environment (default to be selected), azd must honor the --environment flag
	// over the default environment.
	if eg.flags.EnvironmentName != "" {
		name = eg.flags.EnvironmentName
	}
	env, err := eg.envManager.Get(ctx, name)
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`environment '%s' does not exist. You can create it with "azd env new %s"`,
			name,
			name,
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	values := env.Dotenv()
	keyValue, exists := values[keyName]
	if !exists {
		return nil, fmt.Errorf("key '%s' not found in the environment values", keyName)
	}

	// Directly write the key value to the writer
	if _, err := fmt.Fprintln(eg.writer, keyValue); err != nil {
		return nil, fmt.Errorf("writing key value: %w", err)
	}

	return nil, nil
}

func getCmdEnvHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage your application environments. With this command group, you can create a new environment or get, set,"+
			" and list your application environments.",
		[]string{
			formatHelpNote("An Application can have multiple environments (ex: dev, test, prod)."),
			formatHelpNote("Each environment may have a different configuration (that is, connectivity information)" +
				" for accessing Azure resources."),
			formatHelpNote(fmt.Sprintf("You can find all environment configuration under the %s folder.",
				output.WithLinkFormat(".azure/<environment-name>"))),
			formatHelpNote(fmt.Sprintf("The environment name is stored as the %s environment variable in the %s file.",
				output.WithHighLightFormat("AZURE_ENV_NAME"),
				output.WithLinkFormat(".azure/<environment-name>/.env"))),
		})
}
