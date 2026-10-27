// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/infra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newInfraCommand(extCtx *azdext.ExtensionContext, open infraHostFactory) *cobra.Command {
	group := &cobra.Command{Use: "infra", Short: "Generate infrastructure and complete azd-managed provisioning"}
	var output, location, api, models, endpoint, prefix string
	var roles, noRoles, force bool
	generate := &cobra.Command{Use: "generate", Short: "Generate the canonical azd/Bicep project", Args: cobra.NoArgs}
	generate.Long = "Generate an azd/Bicep project without deploying Azure resources.\n" +
		"Prompt for omitted environment, region, optional new-resource prefix, and role assignment.\n" +
		"Explicit inputs take precedence. --no-prompt and --output json use context/defaults without the wizard.\n" +
		"Show a readable summary by default; use --output json for the complete structured result."
	generate.Example = `  azd ai cu infra generate
    Interactively generate an azd/Bicep project without deploying resources.

  azd ai cu infra generate --location eastus2 --models recommended --output-dir provision
    Generate a project with recommended models and no role assignments by default.

  azd ai cu infra generate --foundry-endpoint https://resource.services.ai.azure.com/ --models none
    Generate a project targeting an existing Foundry resource without model deployments.`
	generate.Flags().StringVarP(&output, "output-dir", "d", "provision", "Generated project directory")
	generate.Flags().StringVarP(&location, "location", "l", "", "CU-supported Azure region (default: eastus2)")
	generate.Flags().StringVar(&api, "api-version", "", "Content Understanding API version (default: GA)")
	generate.Flags().StringVar(&models, "models", "prompt",
		"prompt, recommended, none, or comma-separated model@version selectors")
	generate.Flags().StringVar(&endpoint, "foundry-endpoint", "", "Existing Foundry account name, ARM ID, or endpoint")
	generate.Flags().StringVar(&prefix, "foundry-prefix", "", "Prefix for a new Foundry account")
	generate.Flags().BoolVar(&roles, "assign-roles", false,
		"Assign Cognitive Services User to a new Foundry account (requires role-assignment permissions)")
	generate.Flags().BoolVar(&noRoles, "no-assign-roles", false, "Do not add data-plane role assignments (the default)")
	generate.Flags().BoolVar(&force, "force", false, "Replace known generated files and force CU configuration setup")
	generate.RunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("assign-roles") && cmd.Flags().Changed("no-assign-roles") {
			return exterrors.FromError(commandError("conflicting_role_assignment",
				"Role-assignment options cannot be combined.", "Use --assign-roles or --no-assign-roles."))
		}
		if cmd.Flags().Changed("foundry-endpoint") && cmd.Flags().Changed("assign-roles") {
			return exterrors.FromError(commandError("conflicting_role_assignment",
				"--assign-roles cannot be combined with --foundry-endpoint.",
				"For an existing resource, omit --assign-roles and have an administrator "+
					"grant Cognitive Services User on the resource."))
		}
		return exterrors.FromError(runInfraGenerate(
			cmd, extCtx, open, output, location, api, models, endpoint, prefix, roles && !noRoles, force))
	}
	var groupName, account, selection, out string
	modelCommand := &cobra.Command{
		Use: "_models", Hidden: true, Short: "Internal generated-project model setup", Args: cobra.NoArgs,
	}
	modelCommand.Flags().StringVar(&groupName, "resource-group", "", "Foundry resource group")
	modelCommand.Flags().StringVar(&account, "account", "", "Foundry account name")
	modelCommand.Flags().StringVar(&selection, "selection", "", "prompt, recommended, none, or model@version selectors")
	modelCommand.Flags().StringVar(&out, "out", "", "Canonical Bicep model output file")
	modelCommand.RunE = func(cmd *cobra.Command, _ []string) error {
		return exterrors.FromError(runInfraModels(cmd, extCtx, open, groupName, account, selection, out))
	}
	post := &cobra.Command{Use: "_postprovision-v1", Hidden: true,
		Short: "Internal versioned postprovision contract", Args: cobra.NoArgs}
	post.RunE = func(cmd *cobra.Command, _ []string) error {
		return exterrors.FromError(runInfraPost(cmd, extCtx, open))
	}
	azdext.RegisterFlagOptions(generate, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"default", "json", "none"},
		Usage: "Output format: default (human-readable), json, or none",
	})
	group.AddCommand(generate, jsonCommand(modelCommand), jsonCommand(post))
	return group
}

func runInfraGenerate(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open infraHostFactory,
	output, location, api, models, endpoint, prefix string, roles, force bool,
) error {
	for _, name := range []string{"environment", "location", "api-version", "models", "foundry-endpoint", "output-dir"} {
		if cmd.Flags().Changed(name) {
			value, err := cmd.Flags().GetString(name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(value) == "" {
				return commandError("empty_infra_argument", "--"+name+" cannot be empty.",
					"Omit the flag to use its default or supply a valid value.")
			}
		}
	}
	environment := extCtx.Environment
	if environment == "" {
		environment = "dev"
	}
	// Validate local arguments before prompting or contacting ARM.
	choices := infra.Choices{Environment: environment, Location: firstInfra(location, "eastus2"),
		APIVersion: firstInfra(api, cuclient.DefaultAPIVersion), ModelSelection: models, FoundryPrefix: prefix,
		AssignRoles: roles, ForceConfigurationSetup: force}
	var err error
	if choices, err = choices.Normalize(); err != nil {
		return err
	}
	if cmd.Flags().Changed("foundry-prefix") && cmd.Flags().Changed("foundry-endpoint") {
		return commandError("conflicting_foundry_resource", "Use an existing Foundry resource or a new prefix, not both.",
			"Remove --foundry-endpoint or --foundry-prefix.")
	}
	output, err = expandInfraPath(output)
	if err != nil {
		return err
	}
	ctx := azdext.WithAccessToken(cmd.Context())
	host, err := open(cmd, extCtx)
	if err != nil {
		return err
	}
	defer host.Close()
	subscription, err := host.Subscription(ctx, extCtx.NoPrompt)
	if err != nil {
		return err
	}
	if subscription.GetId() == "" || subscription.GetTenantId() == "" {
		return commandError("invalid_subscription", "The host returned an incomplete subscription.", "Run azd auth login.")
	}
	choices.SubscriptionID, choices.SubscriptionName, choices.TenantID =
		subscription.GetId(), subscription.GetName(), subscription.GetTenantId()
	if endpoint != "" {
		resolved, err := host.Resolve(ctx, subscription, endpoint)
		if err != nil {
			return err
		}
		scope, err := config.ResolveResourceScope(resolved.ID, subscription.GetId(), "", config.ResourceScope{})
		if err != nil {
			return err
		}
		choices.FoundryEndpoint, choices.FoundryResourceGroup = resolved.Endpoint, scope.ResourceGroup
		if location == "" && resolved.Region != "" {
			choices.Location = resolved.Region
		}
	}
	if !extCtx.NoPrompt && extCtx.OutputFormat != "json" {
		choices, err = host.PromptGeneration(ctx, choices, generationPrompts{
			environment: extCtx.Environment == "",
			location:    !cmd.Flags().Changed("location") && endpoint == "",
			prefix:      !cmd.Flags().Changed("foundry-prefix") && endpoint == "",
			roles: !cmd.Flags().Changed("assign-roles") &&
				!cmd.Flags().Changed("no-assign-roles") && endpoint == "",
		})
		if err != nil {
			return err
		}
	}
	result, err := infra.Materialize(ctx, output, choices, force)
	if err != nil {
		return err
	}
	if extCtx.OutputFormat == "json" || extCtx.OutputFormat == "none" {
		return writeServiceResult(cmd, extCtx.OutputFormat, result)
	}
	var summary strings.Builder
	action := "Generated"
	if result.ReusedExistingTemplate {
		action = "Updated"
	}
	fmt.Fprintf(&summary, "%s project: %s\nEnvironment: %s\nRegion: %s\nSubscription: %s (%s)\n",
		action, result.OutputDirectory, result.Environment, result.Location, result.SubscriptionName, result.SubscriptionID)
	if result.ReusedExistingTemplate {
		summary.WriteString("Reused existing templates")
		if !slices.Contains(result.Files, "infra/models.json") {
			summary.WriteString("; preserved infra/models.json")
		}
		summary.WriteString(".\n")
	}
	summary.WriteString("Files written:\n")
	for _, path := range result.Files {
		fmt.Fprintf(&summary, "  %s\n", path)
	}
	summary.WriteString("\nNo Azure resources were deployed. Next:\n")
	for index, step := range result.NextSteps {
		if index == 0 && runtime.GOOS == "windows" {
			step = "Set-Location -LiteralPath '" + strings.ReplaceAll(result.OutputDirectory, "'", "''") + "'"
		}
		fmt.Fprintf(&summary, "  %s\n", step)
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), summary.String()); err != nil {
		return fmt.Errorf("write infrastructure summary: %w", err)
	}
	return nil
}

func runInfraModels(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open infraHostFactory, group, account, selection, output string,
) error {
	if group == "" || account == "" || selection == "" || output == "" {
		return commandError("missing_model_arguments", "Supply --resource-group, --account, --selection, and --out.",
			"Use infra _models --help.")
	}
	selection, err := infra.NormalizeSelection(selection)
	if err != nil {
		return err
	}
	if selection == "prompt" && extCtx.NoPrompt {
		return commandError("model_selection_required", "Model selection cannot prompt in noninteractive mode.",
			"Use none, recommended, or explicit model@version selectors.")
	}
	if selection == "none" {
		result, err := infra.SetupModels(cmd.Context(), nil, selection, output, true, true)
		if err != nil {
			return err
		}
		return writeServiceResult(cmd, extCtx.OutputFormat, result)
	}
	ctx := azdext.WithAccessToken(cmd.Context())
	host, err := open(cmd, extCtx)
	if err != nil {
		return err
	}
	defer host.Close()
	snapshot, err := host.Snapshot(ctx, extCtx.Environment)
	if err != nil {
		return err
	}
	request, err := postRequest(snapshot)
	if err != nil {
		return err
	}
	if !strings.EqualFold(group, request.ResourceGroup) || !strings.EqualFold(account, request.Account) {
		return commandError("model_scope_mismatch",
			"Model hook arguments do not match the selected environment's Foundry account.",
			"Select the matching generated project environment.")
	}
	request.Selection = selection
	_, capabilities, err := host.Capabilities(ctx, snapshot, request)
	if err != nil {
		return err
	}
	result, err := infra.SetupModels(ctx, capabilities, selection, output, true, true)
	if err != nil {
		return err
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, result)
}

func runInfraPost(cmd *cobra.Command, extCtx *azdext.ExtensionContext, open infraHostFactory) error {
	ctx := azdext.WithAccessToken(cmd.Context())
	host, err := open(cmd, extCtx)
	if err != nil {
		return err
	}
	defer host.Close()
	snapshot, err := host.Snapshot(ctx, extCtx.Environment)
	if err != nil {
		return err
	}
	request, err := postRequest(snapshot)
	if err != nil {
		return err
	}
	if request.Selection == "prompt" && extCtx.NoPrompt && !request.SetupComplete {
		// Model setup is optional in postprovision; expose the actionable failure and continue basic configuration.
		_, err := fmt.Fprintln(cmd.ErrOrStderr(),
			"Warning: CU_MODEL_SELECTION=prompt cannot run without prompts; set none, recommended, or model@version.")
		if err != nil {
			return err
		}
	}
	postCapabilities, _, err := host.Capabilities(ctx, snapshot, request)
	if err != nil {
		return err
	}
	result, err := infra.Postprovision(ctx, request, postCapabilities)
	if err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Warning:", warning); err != nil {
			return err
		}
	}
	if !result.Succeeded {
		return &cuclient.Error{Kind: cuclient.ErrorService, Code: "postprovision_configuration_failed",
			Message:    "Azure provisioning completed, but required CU configuration is incomplete.",
			Suggestion: "Run azd ai cu doctor in the selected environment, repair configuration, and rerun azd up."}
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, result)
}

func postRequest(snapshot infraSnapshot) (infra.PostRequest, error) {
	request := infra.PostRequest{
		Endpoint:      snapshot.values["CU_ENDPOINT"],
		ResourceGroup: snapshot.values["AZURE_RESOURCE_GROUP"], Account: snapshot.values["FOUNDRY_RESOURCE_NAME"],
		Subscription:         snapshot.values["AZURE_SUBSCRIPTION_ID"],
		APIVersion:           firstInfra(snapshot.values["CU_API_VERSION"], cuclient.DefaultAPIVersion),
		Selection:            firstInfra(snapshot.values["CU_MODEL_SELECTION"], "prompt"),
		ForceConfiguration:   truthyInfra(snapshot.values["CU_CONFIG_SETUP_FORCE"]),
		DisableConfiguration: truthyInfra(os.Getenv("CU_DISABLE_AUTO_CONFIG")),
	}
	if raw, exists := snapshot.section["modelSetupComplete"]; exists {
		if err := json.Unmarshal(raw, &request.SetupComplete); err != nil {
			return request, commandError("invalid_environment_config", "cu.modelSetupComplete must be a boolean.",
				"Repair the selected environment configuration.")
		}
	}
	if request.Endpoint == "" || request.ResourceGroup == "" || request.Account == "" || request.Subscription == "" {
		return request, commandError("incomplete_provisioned_environment",
			"The selected environment lacks provisioned Foundry scope.",
			"Run azd provision or azd env refresh in the generated project.")
	}
	var err error
	request.Endpoint, err = cuclient.NormalizeEndpoint(request.Endpoint)
	if err != nil {
		return request, err
	}
	request.Endpoint = strings.TrimSuffix(request.Endpoint, "/contentunderstanding")
	request.APIVersion, err = cuclient.NormalizeAPIVersion(request.APIVersion)
	return request, err
}

func firstInfra(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truthyInfra(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func expandInfraPath(value string) (string, error) {
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if value == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(value, "~/")), nil
	}
	if strings.TrimSpace(value) == "" {
		return "", commandError("missing_output_directory", "An output directory is required.", "Use --output-dir.")
	}
	return value, nil
}
