// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/blang/semver/v4"
	"github.com/braydonk/yaml"
)

func New(ctx context.Context, projectFilePath string, projectName string) (*ProjectConfig, error) {
	newProject := &ProjectConfig{
		Name: projectName,
	}

	err := Save(ctx, newProject, projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("marshaling project file to yaml: %w", err)
	}

	return Load(ctx, projectFilePath)
}

// Parse will parse a project from a yaml string and return the project configuration
func Parse(ctx context.Context, yamlContent string) (*ProjectConfig, error) {
	var projectConfig ProjectConfig

	if strings.TrimSpace(yamlContent) == "" {
		return nil, fmt.Errorf("unable to parse azure.yaml file. File is empty.")
	}

	if err := yaml.Unmarshal([]byte(yamlContent), &projectConfig); err != nil {
		return nil, fmt.Errorf(
			"unable to parse azure.yaml file. Check the format of the file, "+
				"and also verify you have the latest version of the CLI: %w",
			err,
		)
	}
	var topLevelFields map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &topLevelFields); err != nil {
		return nil, fmt.Errorf("unable to inspect azure.yaml fields: %w", err)
	}
	_, projectConfig.infraPresent = topLevelFields["infra"]

	if err := projectConfig.Validate(); err != nil {
		return nil, err
	}

	projectConfig.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

	if projectConfig.RequiredVersions != nil && projectConfig.RequiredVersions.Azd != nil {
		supportedRange, err := semver.ParseRange(*projectConfig.RequiredVersions.Azd)
		if err != nil {
			return nil, fmt.Errorf("%s is not a valid semver range (for requiredVersions.azd): %w",
				*projectConfig.RequiredVersions.Azd, err)
		}

		if !internal.IsDevVersion() && !supportedRange(internal.VersionInfo().Version) {
			return nil, fmt.Errorf("this project requires a version of azd within the range '%s', but you have '%s'. "+
				"Visit https://aka.ms/azure-dev/install to install a supported version.",
				*projectConfig.RequiredVersions.Azd,
				internal.VersionInfo().Version.String())
		}
	}

	var err error
	projectConfig.Infra.Provider, err = provisioning.ParseProvider(projectConfig.Infra.Provider)
	if err != nil {
		return nil, fmt.Errorf("parsing project %s: %w", projectConfig.Name, err)
	}

	for i := range projectConfig.Infra.Layers {
		projectConfig.Infra.Layers[i].Provider, err = provisioning.ParseProvider(
			projectConfig.Infra.Layers[i].Provider,
		)
		if err != nil {
			return nil, fmt.Errorf("parsing layer %q provider: %w",
				projectConfig.Infra.Layers[i].Name, err)
		}

		if projectConfig.Infra.Layers[i].Provider == provisioning.NotSpecified {
			projectConfig.Infra.Layers[i].Provider = projectConfig.Infra.Provider
		}
	}

	for _, layer := range projectConfig.Layers {
		for i := range layer.Infra {
			layer.Infra[i].Provider, err = provisioning.ParseProvider(layer.Infra[i].Provider)
			if err != nil {
				return nil, fmt.Errorf("parsing layer %q infrastructure %q provider: %w",
					layer.Name, layer.Infra[i].Name, err)
			}
			layer.Infra[i].Layer = layer.Name
			layer.Infra[i].Path = filepath.FromSlash(strings.ReplaceAll(layer.Infra[i].Path, "\\", "/"))
		}

		for name, service := range layer.Services {
			if err := parseServiceConfig(&projectConfig, name, service); err != nil {
				return nil, err
			}
		}
	}

	if strings.Contains(projectConfig.Infra.Path, "\\") && !strings.Contains(projectConfig.Infra.Path, "/") {
		projectConfig.Infra.Path = strings.ReplaceAll(projectConfig.Infra.Path, "\\", "/")
	}

	projectConfig.Infra.Path = filepath.FromSlash(projectConfig.Infra.Path)

	for key, service := range projectConfig.Services {
		if err := parseServiceConfig(&projectConfig, key, service); err != nil {
			return nil, err
		}
	}

	for key, svc := range projectConfig.Resources {
		svc.Name = key
		svc.Project = &projectConfig
	}

	return &projectConfig, nil
}

func parseServiceConfig(projectConfig *ProjectConfig, name string, service *ServiceConfig) error {
	service.Name = name
	service.Project = projectConfig
	service.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

	var err error
	service.Language, err = parseServiceLanguage(service.Language)
	if err != nil {
		return fmt.Errorf("parsing service %s: %w", service.Name, err)
	}
	service.Host, err = parseServiceHost(service.Host)
	if err != nil {
		return fmt.Errorf("parsing service %s: %w", service.Name, err)
	}
	service.Infra.Provider, err = provisioning.ParseProvider(service.Infra.Provider)
	if err != nil {
		return fmt.Errorf("parsing service %s: %w", service.Name, err)
	}

	service.Infra.Path = filepath.FromSlash(strings.ReplaceAll(service.Infra.Path, "\\", "/"))
	service.RelativePath = filepath.FromSlash(strings.ReplaceAll(service.RelativePath, "\\", "/"))
	service.OutputPath = filepath.FromSlash(strings.ReplaceAll(service.OutputPath, "\\", "/"))

	if service.Host == ContainerAppTarget && service.Language == ServiceLanguageNone && service.Image.Empty() {
		return fmt.Errorf("parsing service %s: must specify language or image", service.Name)
	}

	return nil
}

// Load hydrates the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func Load(ctx context.Context, projectFilePath string) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectFilePath)
	bytes, err := os.ReadFile(projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := Parse(ctx, yaml)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	projectConfig.Path = filepath.Dir(projectFilePath)

	provisioningOptions := provisioning.Options{}
	mergo.Merge(&provisioningOptions, projectConfig.Infra)
	mergo.Merge(&provisioningOptions, DefaultProvisioningOptions)

	// complement the project config with hooks defined in the infra path using the syntax `<moduleName>.hooks.yaml`
	// for example `main.hooks.yaml`
	hooksDefinedAtInfraPath, err := hooksFromInfraModule(
		filepath.Join(projectConfig.Path, provisioningOptions.Path),
		provisioningOptions.Module)
	if err != nil {
		return nil, fmt.Errorf("failed getting hooks from infra path, %w", err)
	}
	// Merge the hooks defined at the infra path with the hooks defined in the project configuration
	if len(hooksDefinedAtInfraPath) > 0 && projectConfig.Hooks == nil {
		projectConfig.Hooks = make(map[string][]*ext.HookConfig)
	}
	for hookName, externalHookList := range hooksDefinedAtInfraPath {
		if hookListFromAzureYaml, hookExists := projectConfig.Hooks[hookName]; hookExists {
			mergedHooks := make([]*ext.HookConfig, 0, len(hookListFromAzureYaml)+len(externalHookList))
			mergedHooks = append(mergedHooks, hookListFromAzureYaml...)
			mergedHooks = append(mergedHooks, externalHookList...)
			projectConfig.Hooks[hookName] = mergedHooks
			continue
		}
		projectConfig.Hooks[hookName] = externalHookList
	}

	// Validate hooks after merging infra-module definitions so that nil entries
	// from *.hooks.yaml files are caught before they can cause nil-pointer panics.
	if infraHookProblems := validateHooks(projectConfig.Hooks, "infra hooks"); len(infraHookProblems) > 0 {
		return nil, &ConfigValidationError{Issues: infraHookProblems}
	}

	if projectConfig.Metadata != nil && projectConfig.Metadata.Template != "" {
		template := strings.Split(projectConfig.Metadata.Template, "@")
		if len(template) == 1 { // no version specifier, just the template ID
			tracing.SetUsageAttributes(fields.StringHashed(fields.ProjectTemplateIdKey, template[0]))
		} else if len(template) == 2 { // templateID@version
			tracing.SetUsageAttributes(fields.StringHashed(fields.ProjectTemplateIdKey, template[0]))
			tracing.SetUsageAttributes(fields.StringHashed(fields.ProjectTemplateVersionKey, template[1]))
		} else { // unknown format, just send the whole thing
			tracing.SetUsageAttributes(fields.StringHashed(fields.ProjectTemplateIdKey, projectConfig.Metadata.Template))
		}
	}

	if projectConfig.Name != "" {
		tracing.SetUsageAttributes(fields.StringHashed(fields.ProjectNameKey, projectConfig.Name))
	}

	services := projectConfig.ServiceConfigs()
	if len(services) > 0 {
		hosts := make([]string, len(services))
		languages := make([]string, len(services))
		i := 0
		for _, svcConfig := range services {
			hosts[i] = string(svcConfig.Host)
			languages[i] = string(svcConfig.Language)
			i++
		}

		slices.Sort(hosts)
		slices.Sort(languages)

		tracing.SetUsageAttributes(fields.ProjectServiceLanguagesKey.StringSlice(languages))
		tracing.SetUsageAttributes(fields.ProjectServiceHostsKey.StringSlice(hosts))
	}

	return projectConfig, nil
}

func LoadConfig(ctx context.Context, projectFilePath string) (config.Config, error) {
	log.Printf("Reading project from file '%s'\n", projectFilePath)
	bytes, err := os.ReadFile(projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yamlContent := string(bytes)

	rawConfig := map[string]any{}

	if err := yaml.Unmarshal([]byte(yamlContent), &rawConfig); err != nil {
		return nil, fmt.Errorf(
			"unable to parse azure.yaml file. Check the format of the file, "+
				"and also verify you have the latest version of the CLI: %w",
			err,
		)
	}

	return config.NewConfig(rawConfig), nil
}

func SaveConfig(ctx context.Context, config config.Config, projectFilePath string) error {
	projectBytes, err := yaml.Marshal(config.Raw())
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	projectConfig, err := Parse(ctx, string(projectBytes))
	if err != nil {
		return fmt.Errorf("parsing project yaml: %w", err)
	}

	return Save(ctx, projectConfig, projectFilePath)
}

// Saves the current instance back to the azure.yaml file
func Save(ctx context.Context, projectConfig *ProjectConfig, projectFilePath string) error {
	// We store paths at runtime with os native separators, but want to normalize paths to use forward slashes
	// before saving so `azure.yaml` is consistent across platforms. To avoid mutating the original projectConfig,
	// we make a copy.
	copy := *projectConfig

	copy.Infra.Path = filepath.ToSlash(copy.Infra.Path)
	copy.Services = make(map[string]*ServiceConfig, len(projectConfig.Services))

	for name, svc := range projectConfig.Services {
		svcCopy := *svc
		svcCopy.Project = &copy
		svcCopy.Infra.Path = filepath.ToSlash(svc.Infra.Path)
		svcCopy.RelativePath = filepath.ToSlash(svc.RelativePath)
		svcCopy.OutputPath = filepath.ToSlash(svc.OutputPath)

		copy.Services[name] = &svcCopy
	}
	if projectConfig.Layers != nil {
		copy.Layers = make(LayerConfigs, len(projectConfig.Layers))
		for i, layer := range projectConfig.Layers {
			layerCopy := *layer
			layerCopy.Infra = slices.Clone(layer.Infra)
			for j := range layerCopy.Infra {
				layerCopy.Infra[j].Path = filepath.ToSlash(layerCopy.Infra[j].Path)
			}
			layerCopy.Services = make(map[string]*ServiceConfig, len(layer.Services))
			for name, service := range layer.Services {
				serviceCopy := *service
				serviceCopy.Project = &copy
				serviceCopy.Infra.Path = filepath.ToSlash(service.Infra.Path)
				serviceCopy.RelativePath = filepath.ToSlash(service.RelativePath)
				serviceCopy.OutputPath = filepath.ToSlash(service.OutputPath)
				layerCopy.Services[name] = &serviceCopy
			}
			copy.Layers[i] = &layerCopy
		}
	}

	projectBytes, err := yaml.Marshal(copy)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	version := "v1.0"
	if projectConfig.Format() == ProjectFormatLayersV2 {
		version = "alpha"
	} else if projectConfig.MetaSchemaVersion != "" {
		version = projectConfig.MetaSchemaVersion
	}

	annotation := fmt.Sprintf(
		"# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/%s/azure.yaml.json",
		version)
	projectFileContents := bytes.NewBufferString(annotation + "\n\n")
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return fmt.Errorf("preparing new project file contents: %w", err)
	}

	// Atomic write so readers never observe a partially written azure.yaml.
	err = azdext.WriteFileAtomic(projectFilePath, projectFileContents.Bytes(), osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	projectConfig.Path = filepath.Dir(projectFilePath)

	return nil
}

// hooksFromInfraModule check if there is file named azd.hooks.yaml in the service path
// and return the hooks configuration.
func hooksFromInfraModule(infraPath, moduleName string) (HooksConfig, error) {
	hooksPath := filepath.Join(infraPath, moduleName+".hooks.yaml")
	if _, err := os.Stat(hooksPath); os.IsNotExist(err) {
		return nil, nil
	}
	hooksFile, err := os.ReadFile(hooksPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading hooks from '%s', %w", hooksPath, err)
	}

	// open hooksPath into a byte array and unmarshal it into a map[string]*ext.HookConfig
	hooks := make(HooksConfig)
	if err := yaml.Unmarshal(hooksFile, &hooks); err != nil {
		return nil, fmt.Errorf("failed unmarshalling hooks from '%s', %w", hooksPath, err)
	}

	return hooks, nil
}
