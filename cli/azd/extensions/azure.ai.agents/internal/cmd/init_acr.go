// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

const initACRSubscriptionMessage = "Select an Azure subscription to provision your agent and Foundry project resources."

func initACRResourceID(flags *initFlags) string {
	if flags.acrResourceIDSet {
		return flags.acrResourceID
	}
	return flags.effectiveACRResourceID
}

func initACRConflict(input string) error {
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"--acr-resource-id cannot be combined with "+input,
		"use --acr-resource-id for hosted-container source builds in a new Foundry project",
	)
}

func validateInitACRInput(cmd *cobra.Command, flags *initFlags) error {
	if !flags.acrResourceIDSet && flags.effectiveACRResourceID == "" {
		return nil
	}
	registryID := flags.acrResourceID
	if !flags.acrResourceIDSet {
		registryID = flags.effectiveACRResourceID
	}
	if err := synthesis.ValidateACRSelector(registryID, "", ""); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--acr-resource-id must be a non-empty ACR ARM resource ID",
			"use /subscriptions/<id>/resourceGroups/<group>/providers/Microsoft.ContainerRegistry/registries/<name>",
		)
	}
	for _, name := range []string{
		"project-id", "acr-connection", "image", "registry-connection",
		"runtime", "entry-point", "dep-resolution",
	} {
		if cmd.Flags().Changed(name) {
			return initACRConflict("--" + name)
		}
	}
	if strings.EqualFold(flags.deployMode, "code") {
		return initACRConflict("--deploy-mode code")
	}
	if flags.kind != "" && !strings.EqualFold(flags.kind, "hosted") {
		return initACRConflict("--kind " + flags.kind)
	}
	if strings.EqualFold(flags.infra, "terraform") {
		return initACRConflict("--infra terraform")
	}
	return nil
}

func validateInitACRManifest(
	ctx context.Context, client *azdext.AzdClient, content []byte, root string, flags *initFlags, targetDir string,
) error {
	if !flags.acrResourceIDSet {
		return nil
	}
	_, values, err := readInitACREnvironment(ctx, client, flags, targetDir)
	if err != nil {
		return err
	}
	return validateInitACRManifestInEnvironment(content, root, flags.acrResourceID, values)
}

func validateInitACRManifestInEnvironment(
	content []byte, root, registryID string, values map[string]string,
) error {
	if registryID == "" {
		return nil
	}
	if err := validateInitACRInfrastructure(content, root); err != nil {
		return err
	}
	input := synthesis.Input{
		RawAzureYAML: content,
		ProjectRoot:  root,
		Env:          values,
	}
	if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, registryID); err != nil {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments, err.Error(),
			"use --acr-resource-id with source builds in a new Foundry project",
		)
	}
	needsACR, err := synthesis.RequiresACR(input)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("cannot apply --acr-resource-id: %s", err),
			"fix the source-build agent configuration and retry",
		)
	}
	if !needsACR {
		return initACRConflict("an agent configuration without hosted-container source builds")
	}
	return nil
}

func validateInitACRInfrastructure(content []byte, root string) error {
	config, err := readDeclaredInfraConfig(content)
	if err != nil {
		return err
	}
	if config.Provider == project.TerraformProviderName && len(config.Layers) == 0 {
		return initACRConflict("Terraform infrastructure")
	}
	foundryLayer, terraformLayer := false, false
	for _, layer := range config.Layers {
		provider := valueOrDefault(layer.Provider, config.Provider)
		if provider == project.FoundryProviderName {
			foundryLayer = true
		}
		if provider != project.TerraformProviderName {
			continue
		}
		terraformLayer = true
		infraDir, err := paths.JoinAllowRoot(root, layer.Path)
		if err != nil {
			return exterrors.Validation(exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("invalid infrastructure layer path: %s", err),
				"keep infrastructure layer paths within the project directory")
		}
		module := valueOrDefault(layer.Module, defaultInfraModule)
		if _, err := paths.JoinAllowRoot(root, filepath.Join(layer.Path, module+".tfvars.json")); err != nil {
			return exterrors.Validation(exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("invalid infrastructure layer module: %s", err),
				"keep infrastructure module paths within the project directory")
		}
		foundry, err := isFoundryTerraformInfra(
			infraDir, module,
		)
		if err != nil {
			return err
		}
		if foundry {
			return initACRConflict("Terraform Foundry infrastructure")
		}
	}
	if terraformLayer && !foundryLayer {
		return initACRConflict("Terraform infrastructure without a Bicep Foundry infrastructure layer")
	}
	return nil
}

func readInitACREnvironment(
	ctx context.Context, client *azdext.AzdClient, flags *initFlags, targetDir string,
) (string, map[string]string, error) {
	if targetDir == "" {
		targetDir = "."
	}
	if flags.templatePointer != "" || flags.acrTemplateTarget {
		flags.resolvedEnvironmentName = deriveEnvName(flags, targetDir)
		root, projectErr := azdext.GetProjectDir()
		target, err := filepath.Abs(targetDir)
		if err != nil {
			return "", nil, err
		}
		if projectErr != nil && !errors.Is(projectErr, azdext.ErrProjectNotFound) {
			return "", nil, projectErr
		}
		relative, relativeErr := filepath.Rel(root, target)
		if projectErr != nil || relativeErr != nil || relative != "." {
			return readInitACRTargetEnvironment(flags, targetDir)
		}
	}
	if _, err := azdext.GetProjectDir(); errors.Is(err, azdext.ErrProjectNotFound) {
		envName := deriveEnvName(flags, targetDir)
		return "", map[string]string{"AZURE_ENV_NAME": envName}, nil
	} else if err != nil {
		return "", nil, fmt.Errorf("resolve project before selecting a registry: %w", err)
	}
	environments, err := client.Environment().List(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", nil, fmt.Errorf("read environments before selecting a registry: %w", err)
	}
	requestedName := valueOrDefault(flags.env, flags.resolvedEnvironmentName)
	envName := ""
	for _, env := range environments.GetEnvironments() {
		if requestedName != "" && env.GetName() == requestedName ||
			requestedName == "" && env.GetDefault() {
			envName = env.GetName()
			break
		}
	}
	values := map[string]string{}
	if envName != "" {
		values, err = getAgentEnvironmentValues(ctx, client, envName)
		if err != nil {
			return "", nil, fmt.Errorf("read registry selection: %w", err)
		}
	}
	values["AZURE_ENV_NAME"] = valueOrDefault(envName, deriveEnvName(flags, targetDir))
	return envName, values, nil
}

func readInitACRTargetEnvironment(flags *initFlags, targetDir string) (string, map[string]string, error) {
	envName := deriveEnvName(flags, targetDir)
	flags.resolvedEnvironmentName = envName
	root, err := filepath.Abs(targetDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve template target: %w", err)
	}
	target := azdcontext.NewAzdContextWithDirectory(root)
	envPath, err := target.EnvironmentFilePath(envName, ".env")
	if err != nil {
		return "", nil, err
	}
	values, err := godotenv.Read(envPath)
	if errors.Is(err, os.ErrNotExist) {
		values = map[string]string{}
	} else if err != nil {
		return "", nil, fmt.Errorf("read target registry selection: %w", err)
	}
	configPath, err := target.EnvironmentFilePath(envName, "config.json")
	if err != nil {
		return "", nil, err
	}
	cfg, err := config.NewFileConfigManager(config.NewManager()).Load(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("read target registry binding: %w", err)
	}
	if err == nil {
		if binding, found := cfg.Get(synthesis.ACRBindingConfigKey); found {
			encoded, err := json.Marshal(binding)
			if err != nil {
				return "", nil, fmt.Errorf("encode target registry binding: %w", err)
			}
			values[synthesis.ACRBindingEnvKey] = string(encoded)
		}
	}
	values["AZURE_ENV_NAME"] = envName
	// Target state is local, not an environment in the caller's host.
	return "", values, nil
}

func readInitACRBinding(
	ctx context.Context, client *azdext.AzdClient, envName string, values map[string]string,
) error {
	if envName == "" {
		return nil
	}
	binding, err := client.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
		EnvName: envName, Path: synthesis.ACRBindingConfigKey,
	})
	if err != nil {
		return fmt.Errorf("read deployed registry binding: %w", err)
	}
	if binding.GetFound() {
		values[synthesis.ACRBindingEnvKey] = string(binding.GetValue())
	}
	return nil
}

func validateInitACRSubscription(registryID, subscriptionID string) error {
	if registryID == "" {
		return nil
	}
	if err := synthesis.ValidateACRSelector(registryID, subscriptionID, ""); err != nil {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments, err.Error(),
			"use a registry in the Foundry project's subscription",
		)
	}
	return nil
}

func validateInitACRSelection(input synthesis.Input, registryID string) error {
	if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, registryID); err != nil {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments, err.Error(),
			azdext.ErrorSuggestion(err),
		)
	}
	return nil
}

func planInitACRSubscription(
	ctx context.Context, client *azdext.AzdClient, flags *initFlags, targetDir string,
) (*azdext.AzureScope, error) {
	registryID := initACRResourceID(flags)
	if registryID == "" {
		return nil, nil
	}
	envName, values, err := readInitACREnvironment(ctx, client, flags, targetDir)
	if err != nil {
		return nil, err
	}
	scope := &azdext.AzureScope{
		SubscriptionId: values["AZURE_SUBSCRIPTION_ID"],
		TenantId:       values["AZURE_TENANT_ID"],
	}
	if err := validateInitACRSubscription(registryID, scope.SubscriptionId); err != nil {
		return nil, err
	}
	input := synthesis.Input{Env: values}
	if flags.templatePointer == "" && !flags.acrTemplateTarget {
		root, err := azdext.GetProjectDir()
		if err != nil && !errors.Is(err, azdext.ErrProjectNotFound) {
			return nil, fmt.Errorf("resolve project before selecting a registry: %w", err)
		}
		if err == nil {
			input.ProjectRoot = root
			input.RawAzureYAML, err = readProjectAzureYAML(root)
			if err != nil {
				return nil, fmt.Errorf("read project registry configuration: %w", err)
			}
		}
		if err := readInitACRBinding(ctx, client, envName, values); err != nil {
			return nil, err
		}
		if err := validateInitACRSelection(input, registryID); err != nil {
			return nil, err
		}
	}
	if scope.SubscriptionId == "" {
		if flags.noPrompt {
			return nil, nil
		}
		if err := resolveSubscription(
			ctx, client, &azdext.AzureContext{Scope: scope}, initACRSubscriptionMessage,
		); err != nil {
			return nil, err
		}
		if err := validateInitACRSubscription(registryID, scope.SubscriptionId); err != nil {
			return nil, err
		}
		if flags.templatePointer == "" && !flags.acrTemplateTarget {
			values["AZURE_SUBSCRIPTION_ID"] = scope.SubscriptionId
			if err := validateInitACRSelection(input, registryID); err != nil {
				return nil, err
			}
		}
	}
	return scope, nil
}

func validateInitACRProject(ctx context.Context, client *azdext.AzdClient, flags *initFlags) error {
	return validateInitACRProjectContent(ctx, client, flags, nil, "", ".")
}

func validateInitACRProjectContent(
	ctx context.Context, client *azdext.AzdClient, flags *initFlags, content []byte, root, targetDir string,
) error {
	flags.effectiveACRResourceID = ""
	staged := content != nil
	if flags.acrResourceIDSet {
		if err := synthesis.ValidateACRSelector(flags.acrResourceID, "", ""); err != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				"--acr-resource-id: "+err.Error(), "provide an external registry ARM resource ID")
		}
		if !staged && flags.templatePointer != "" {
			// The target and its environment are resolved after staging.
			return nil
		}
	}
	if !flags.acrResourceIDSet && (flags.projectResourceId != "" || !staged && flags.templatePointer != "") {
		return nil
	}
	if !staged {
		var err error
		root, err = azdext.GetProjectDir()
		if errors.Is(err, azdext.ErrProjectNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve project before selecting a registry: %w", err)
		}
		content, err = readProjectAzureYAML(root)
		if err != nil {
			return fmt.Errorf("read project registry configuration: %w", err)
		}
	}
	if !flags.acrResourceIDSet && flags.infra != "" {
		provider, err := parseInfraProvider(flags.infra)
		if err != nil {
			return err
		}
		gate, err := resolveInfraGate(provider)
		if err != nil {
			return err
		}
		if gate.standaloneEject {
			// Eject validates its final scope during synthesis.
			return nil
		}
	}
	envName, values, err := readInitACREnvironment(ctx, client, flags, targetDir)
	if err != nil {
		return err
	}
	if err := readInitACRBinding(ctx, client, envName, values); err != nil {
		return err
	}
	if !staged && flags.templatePointer == "" && envName == "" &&
		flags.env == "" && flags.resolvedEnvironmentName == "" {
		if flags.agentName != "" && flags.noPrompt {
			values["AZURE_ENV_NAME"] = sanitizeAgentName(flags.agentName + "-dev")
		} else {
			// Interactive init resolves its source setup later.
			delete(values, "AZURE_ENV_NAME")
		}
	}
	selectedID := flags.acrResourceID
	input := synthesis.Input{RawAzureYAML: content, ProjectRoot: root, Env: values}
	if !flags.acrResourceIDSet {
		selectedID = values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		if strings.EqualFold(values["USE_EXISTING_AI_PROJECT"], "true") {
			return nil
		}
		var selectionErr error
		if selectedID == "" && values[synthesis.ACRBindingEnvKey] != "" {
			_, selectedID, selectionErr = synthesis.ResolveGreenfieldACRSelection(input)
		}
		if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, selectedID); errors.Is(
			err, synthesis.ErrEndpointBrownfield,
		) {
			return nil
		}
		if selectionErr != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments, selectionErr.Error(),
				"restore the deployed registry selection or use a new azd environment")
		}
	}
	if flags.acrResourceIDSet {
		if err := synthesis.ValidateDeployedACRReuse(input, selectedID); err != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments, err.Error(),
				"restore the deployed registry selection or use a new azd environment")
		}
	}
	if !flags.acrResourceIDSet {
		if selectedID == "" && values["AZD_FOUNDRY_ACR_MODE"] != "reuse-connect" &&
			values[synthesis.ACRBindingEnvKey] == "" {
			return nil
		}
	}
	if !flags.acrResourceIDSet || values[synthesis.ACRBindingEnvKey] != "" {
		mode, id, err := synthesis.ResolveGreenfieldACRSelection(input)
		if err != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments, err.Error(),
				"restore the deployed registry selection or use a new azd environment")
		}
		if !flags.acrResourceIDSet {
			if mode != "reuse-connect" {
				return nil
			}
			selectedID = id
		}
	}
	if !staged && flags.templatePointer != "" {
		// A template can replace the manifest and teardown scope.
		input.RawAzureYAML = nil
		input.Env = map[string]string{
			"AZURE_SUBSCRIPTION_ID":    values["AZURE_SUBSCRIPTION_ID"],
			synthesis.ACRBindingEnvKey: values[synthesis.ACRBindingEnvKey],
		}
	}
	if err := validateInitACRSelection(input, selectedID); err != nil {
		return err
	}
	flags.effectiveACRResourceID = selectedID
	if strings.EqualFold(flags.deployMode, "code") {
		return initACRConflict("--deploy-mode code")
	}
	if flags.templatePointer == "" && !flags.acrTemplateTarget {
		if err := validateInitACRInfrastructure(content, root); err != nil {
			return err
		}
	}
	if canReuseExistingAgentConfiguration(flags, false, flags.src != "") {
		info, err := inspectAzureYaml(content, root)
		if err != nil {
			return err
		}
		if info.hasAgentService {
			if err := validateInitACRManifestInEnvironment(content, root, selectedID, values); err != nil {
				return err
			}
		}
	}
	return nil
}
