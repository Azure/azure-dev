// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ACRReuseFeatureID identifies the shared Foundry preview.
const ACRReuseFeatureID = "foundry.acr_reuse"

// ACRReuseEnvVar is the process-only opt-in for external ACR reuse.
const ACRReuseEnvVar = "AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE"

// ACRPendingConfigKey stores unfinished external registry access.
const ACRPendingConfigKey = "extensions.ai-projects.acrPending"

// ACRReuseEnabled reads the process, including host injection.
func ACRReuseEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(ACRReuseEnvVar))
	return err == nil && enabled
}

// RequireACRReuse rejects reuse without changing deployment state.
func RequireACRReuse() error {
	if ACRReuseEnabled() {
		return nil
	}
	return &azdext.LocalError{
		Code:     "feature_not_enabled",
		Category: azdext.LocalErrorCategoryCompatibility,
		Message: fmt.Sprintf("preview feature %q is disabled; set %s=true before running azd",
			ACRReuseFeatureID, ACRReuseEnvVar),
		Suggestion: "Set " + ACRReuseEnvVar + "=true in your shell or CI for setup, provision, preview, " +
			"deploy, down, recovery, and eject. Existing resources and saved state are unchanged.",
	}
}

// RequireACRReuseForInput distinguishes reuse from create.
func RequireACRReuseForInput(in Input) error {
	if ACRReuseEnabled() {
		return nil
	}
	binding := in.ACRBinding
	if binding == nil {
		var err error
		binding, err = ParseACRBinding(in.Env[ACRBindingEnvKey])
		if err != nil {
			return err
		}
	}
	if err := ValidateACRBinding(binding,
		in.Env["AZD_FOUNDRY_ACR_MODE"], in.Env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]); err != nil {
		return err
	}
	if in.ACRPending || binding != nil && binding.Mode == "reuse-connect" {
		return RequireACRReuse()
	}
	// Existing-project connection workflows predate this preview.
	if strings.EqualFold(in.Env["USE_EXISTING_AI_PROJECT"], "true") {
		return nil
	}
	if binding == nil && in.Env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] == "" &&
		in.Env["AZD_FOUNDRY_ACR_MODE"] != "reuse-connect" {
		return nil
	}
	if len(in.RawAzureYAML) > 0 {
		if in.ServiceName != "" {
			endpoint, err := ProjectEndpoint(in.RawAzureYAML, in.ServiceName, in.ProjectRoot, in.Env)
			if err != nil {
				return err
			}
			if endpoint != "" {
				return nil
			}
		} else {
			_, projects, err := acrProjectServices(in)
			if err != nil {
				return err
			}
			for _, project := range projects {
				if strings.TrimSpace(project.Endpoint) != "" {
					return nil
				}
			}
		}
	}
	mode, id, err := ResolveGreenfieldACRSelection(in)
	if err != nil {
		return err
	}
	if mode != "reuse-connect" {
		return nil
	}
	if err := ValidateACRSelector(id, in.Env["AZURE_SUBSCRIPTION_ID"], ""); err != nil {
		return err
	}
	return RequireACRReuse()
}

// RequireACRReuseForEnvironment checks saved reuse intent.
// Reads use the host; the opt-in itself never loads config or dotenv.
func RequireACRReuseForEnvironment(
	ctx context.Context, client *azdext.AzdClient, envName string, in Input,
) error {
	if ACRReuseEnabled() {
		return nil
	}
	if client != nil && envName != "" {
		if in.ACRBinding == nil {
			response, err := client.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
				EnvName: envName, Path: ACRBindingConfigKey,
			})
			if err != nil {
				return fmt.Errorf("read deployed registry binding before checking reuse: %w", err)
			}
			if response.GetFound() {
				in.ACRBinding, err = ParseACRBinding(response.GetValue())
				if err != nil {
					return err
				}
			}
		}
		response, err := client.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
			EnvName: envName, Path: ACRPendingConfigKey,
		})
		if err != nil {
			return fmt.Errorf("read unfinished registry access before checking reuse: %w", err)
		}
		in.ACRPending = in.ACRPending || response.GetFound()
	}
	return RequireACRReuseForInput(in)
}

// RequireACRReuseForProject guards hooks before any handler writes.
func RequireACRReuseForProject(
	ctx context.Context, client *azdext.AzdClient, project *azdext.ProjectConfig,
) error {
	if ACRReuseEnabled() {
		return nil
	}
	current, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("read current environment before checking registry reuse: %w", err)
	}
	envName := current.GetEnvironment().GetName()
	if envName == "" {
		return nil
	}
	response, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return fmt.Errorf("read environment before checking registry reuse: %w", err)
	}
	values := map[string]string{"AZURE_ENV_NAME": envName}
	for _, entry := range response.GetKeyValues() {
		values[entry.GetKey()] = entry.GetValue()
	}
	in := Input{Env: values, ProjectRoot: project.GetPath()}
	if in.ProjectRoot != "" {
		for _, name := range []string{"azure.yaml", "azure.yml"} {
			// #nosec G304 -- the host supplies the project root.
			raw, err := os.ReadFile(filepath.Join(in.ProjectRoot, name))
			if err == nil {
				in.RawAzureYAML = raw
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("read project before checking registry reuse: %w", err)
			}
		}
	}
	if len(in.RawAzureYAML) == 0 {
		for _, service := range project.GetServices() {
			if service.GetHost() != "azure.ai.project" && service.GetHost() != "azure.ai.agents" {
				continue
			}
			props := service.GetAdditionalProperties()
			if props == nil {
				props = service.GetConfig()
			}
			if props.GetFields()["endpoint"].GetStringValue() != "" {
				values["USE_EXISTING_AI_PROJECT"] = "true"
			}
		}
	}
	return RequireACRReuseForEnvironment(ctx, client, envName, in)
}
