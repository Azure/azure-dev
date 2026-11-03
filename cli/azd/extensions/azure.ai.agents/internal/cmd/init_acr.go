// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func readInitACREnvironment(
	ctx context.Context, client *azdext.AzdClient, flags *initFlags,
) (string, map[string]string, error) {
	if _, err := azdext.GetProjectDir(); errors.Is(err, azdext.ErrProjectNotFound) {
		return "", map[string]string{}, nil
	} else if err != nil {
		return "", nil, fmt.Errorf("resolve project before selecting a registry: %w", err)
	}
	environments, err := client.Environment().List(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", nil, fmt.Errorf("read environments before selecting a registry: %w", err)
	}
	envName := ""
	for _, env := range environments.GetEnvironments() {
		if flags.env != "" && env.GetName() == flags.env ||
			flags.env == "" && env.GetDefault() {
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
	return envName, values, nil
}

func validateInitACRProject(ctx context.Context, client *azdext.AzdClient, flags *initFlags) error {
	if flags.acrResourceIDSet {
		if err := synthesis.ValidateACRSelector(flags.acrResourceID, "", ""); err != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				"--acr-resource-id: "+err.Error(), "provide an external registry ARM resource ID")
		}
	}
	if !flags.acrResourceIDSet && (flags.projectResourceId != "" || flags.templatePointer != "") {
		return nil
	}
	root, err := azdext.GetProjectDir()
	if errors.Is(err, azdext.ErrProjectNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve project before selecting a registry: %w", err)
	}
	content, err := readProjectAzureYAML(root)
	if err != nil {
		return fmt.Errorf("read project registry configuration: %w", err)
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
	envName, values, err := readInitACREnvironment(ctx, client, flags)
	if err != nil {
		return err
	}
	selectedID := flags.acrResourceID
	values["AZURE_ENV_NAME"] = envName
	input := synthesis.Input{RawAzureYAML: content, ProjectRoot: root, Env: values}
	if !flags.acrResourceIDSet {
		selectedID = values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		if strings.EqualFold(values["USE_EXISTING_AI_PROJECT"], "true") {
			return nil
		}
		if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, selectedID); errors.Is(
			err, synthesis.ErrEndpointBrownfield,
		) {
			return nil
		}
	}
	if envName != "" {
		binding, err := client.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
			EnvName: envName, Path: synthesis.ACRBindingConfigKey,
		})
		if err != nil {
			return fmt.Errorf("read deployed registry binding: %w", err)
		}
		if binding.GetFound() {
			values[synthesis.ACRBindingEnvKey] = string(binding.GetValue())
		}
	}
	if !flags.acrResourceIDSet {
		if selectedID == "" && values["AZD_FOUNDRY_ACR_MODE"] != "reuse-connect" &&
			values[synthesis.ACRBindingEnvKey] == "" {
			return nil
		}
		mode, id, err := synthesis.ResolveGreenfieldACRSelection(input)
		if err != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments, err.Error(),
				"restore the deployed registry selection or use a new azd environment")
		}
		if mode != "reuse-connect" {
			return nil
		}
		selectedID = id
	}
	if flags.templatePointer != "" {
		// A template can replace the manifest and teardown scope.
		input.RawAzureYAML = nil
		input.Env = map[string]string{"AZURE_SUBSCRIPTION_ID": values["AZURE_SUBSCRIPTION_ID"]}
	}
	if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, selectedID); err != nil {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments, err.Error(),
			"use a registry in the Foundry project's subscription, outside its resource group; "+
				"deployed registry migration is not supported",
		)
	}
	return nil
}
