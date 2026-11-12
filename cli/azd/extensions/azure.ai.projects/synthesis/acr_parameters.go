// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"go.yaml.in/yaml/v3"
)

func requireACRReuseForOnDiskInput(
	ctx context.Context, in Input, compiler BicepParamCompiler,
) error {
	if ACRReuseEnabled() || in.ProjectRoot == "" || len(in.RawAzureYAML) == 0 {
		return nil
	}
	target, err := foundryParameterTarget(in.RawAzureYAML)
	if err != nil || target == nil {
		return err
	}
	sourcePath, err := foundryParameterPath(in.ProjectRoot, *target, ".bicepparam")
	if err != nil {
		return err
	}
	bicepParam := sourcePath != ""
	if !bicepParam {
		sourcePath, err = foundryParameterPath(in.ProjectRoot, *target, ".bicep")
		if err != nil || sourcePath == "" {
			return err
		}
		sourcePath, err = foundryParameterPath(in.ProjectRoot, *target, ".parameters.json")
		if err != nil || sourcePath == "" {
			return err
		}
	}
	existing, err := acrExistingProject(in)
	if err != nil || existing {
		return err
	}
	var template, parameters map[string]any
	if bicepParam {
		template, parameters, err = CompileBicepParameters(ctx, compiler, sourcePath, in.Env)
	} else {
		parameters, err = LoadARMParametersFile(sourcePath, in.Env)
	}
	if err != nil {
		return err
	}
	for name, raw := range parameters {
		if !strings.EqualFold(name, "existingAcrResourceId") {
			continue
		}
		entry, valid := raw.(map[string]any)
		if !valid || entry["value"] == nil {
			return invalidACRParameter(sourcePath)
		}
	}
	value := ARMParameterValue(template, parameters, "existingAcrResourceId")
	if value == nil {
		return nil
	}
	id, stringValue := value.(string)
	if !stringValue {
		return invalidACRParameter(sourcePath)
	}
	if id == "" {
		return nil
	}
	group, err := GreenfieldACRResourceGroup(in)
	if err != nil {
		return err
	}
	if value, ok := ARMParameterValue(template, parameters, "resourceGroupName").(string); ok && value != "" {
		group = value
	}
	if err := ValidateACRSelector(id, in.Env["AZURE_SUBSCRIPTION_ID"], group); err != nil {
		return err
	}
	return RequireACRReuse()
}

func invalidACRParameter(sourcePath string) error {
	return &azdext.LocalError{
		Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryValidation,
		Message: fmt.Sprintf("existingAcrResourceId in %s must contain a string value", sourcePath),
		Suggestion: "use parameters.existingAcrResourceId.value in ARM parameters JSON, " +
			"or a string-valued parameter in Bicep",
	}
}

type foundryParameterInfra struct {
	Provider string `yaml:"provider"`
	Path     string `yaml:"path"`
	Module   string `yaml:"module"`
}

func foundryParameterTarget(raw []byte) (*foundryParameterInfra, error) {
	var document struct {
		Infra struct {
			foundryParameterInfra `yaml:",inline"`
			Layers                []foundryParameterInfra `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, &azdext.LocalError{
			Code: "invalid_azure_yaml", Category: azdext.LocalErrorCategoryValidation,
			Message:    fmt.Sprintf("parse azure.yaml infrastructure configuration: %s", err),
			Suggestion: "verify azure.yaml is valid YAML",
		}
	}
	root := document.Infra.foundryParameterInfra
	root.Provider = strings.TrimSpace(root.Provider)
	var target *foundryParameterInfra
	if len(document.Infra.Layers) == 0 {
		if root.Provider != "microsoft.foundry" {
			return nil, nil
		}
		target = &root
	} else {
		if root.Provider == "microsoft.foundry" {
			return nil, invalidFoundryParameterTarget("a root Foundry provider cannot also declare infrastructure layers")
		}
		for _, layer := range document.Infra.Layers {
			provider := strings.TrimSpace(layer.Provider)
			if provider == "" {
				provider = root.Provider
			}
			if provider != "microsoft.foundry" {
				continue
			}
			if target != nil {
				return nil, invalidFoundryParameterTarget("multiple infrastructure layers use microsoft.foundry")
			}
			selected := layer
			selected.Provider = provider
			target = &selected
		}
	}
	if target == nil {
		return nil, nil
	}
	target.Path = strings.TrimSpace(target.Path)
	if target.Path == "" {
		target.Path = "infra"
	}
	target.Path = strings.ReplaceAll(target.Path, "\\", "/")
	if filepath.IsAbs(target.Path) || strings.HasPrefix(target.Path, "/") ||
		strings.Contains(target.Path, ":") || filepath.Clean(filepath.FromSlash(target.Path)) == "." ||
		strings.Contains("/"+target.Path+"/", "/../") {
		return nil, invalidFoundryParameterTarget("the Foundry infrastructure path must stay inside the project")
	}
	target.Module = strings.TrimSpace(target.Module)
	if target.Module == "" {
		target.Module = "main"
	}
	if filepath.Ext(target.Module) != "" || target.Module == "." || target.Module == ".." ||
		strings.ContainsAny(target.Module, `/\`) {
		return nil, invalidFoundryParameterTarget("the Foundry module must be a file name without an extension")
	}
	return target, nil
}

func invalidFoundryParameterTarget(message string) error {
	return &azdext.LocalError{
		Code: "invalid_service_config", Category: azdext.LocalErrorCategoryValidation,
		Message: message, Suggestion: "verify the microsoft.foundry infrastructure path, module, and layer configuration",
	}
}

func foundryParameterPath(root string, target foundryParameterInfra, extension string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.FromSlash(target.Path), target.Module+extension)
	if !osutil.IsPathContained(root, path) {
		return "", invalidFoundryParameterTarget("the Foundry parameter source must stay inside the project")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", invalidFoundryParameterTarget(fmt.Sprintf("inspect Foundry parameter source: %s", err))
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !osutil.IsPathContained(rootReal, pathReal) {
		return "", invalidFoundryParameterTarget("the Foundry parameter source escapes the project through a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return "", invalidFoundryParameterTarget(fmt.Sprintf("Foundry parameter source %q must be a regular file", path))
	}
	return path, nil
}
