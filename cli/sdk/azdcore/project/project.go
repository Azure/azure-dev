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

	"github.com/azure/azure-dev/cli/sdk/azdcore/common/permissions"
	"github.com/azure/azure-dev/cli/sdk/azdcore/config"
	"github.com/azure/azure-dev/cli/sdk/azdcore/contracts"
	"github.com/azure/azure-dev/cli/sdk/azdcore/internal"
	"github.com/blang/semver/v4"
	"gopkg.in/yaml.v3"
)

const (
	//nolint:lll
	projectSchemaAnnotation = "# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json"
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

	projectConfig.EventDispatcher = NewEventDispatcher[ProjectLifecycleEventArgs]()

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
	projectConfig.Infra.Provider, err = contracts.ParseProvisioningProvider(projectConfig.Infra.Provider)
	if err != nil {
		return nil, fmt.Errorf("parsing project %s: %w", projectConfig.Name, err)
	}

	if projectConfig.Infra.Path == "" {
		projectConfig.Infra.Path = "infra"
	}

	if strings.Contains(projectConfig.Infra.Path, "\\") && !strings.Contains(projectConfig.Infra.Path, "/") {
		projectConfig.Infra.Path = strings.ReplaceAll(projectConfig.Infra.Path, "\\", "/")
	}

	projectConfig.Infra.Path = filepath.FromSlash(projectConfig.Infra.Path)

	for key, svc := range projectConfig.Services {
		svc.Name = key
		svc.Project = &projectConfig
		svc.EventDispatcher = NewEventDispatcher[ServiceLifecycleEventArgs]()

		var err error
		svc.Language, err = parseServiceLanguage(svc.Language)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.Host, err = parseServiceHost(svc.Host)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.Infra.Provider, err = contracts.ParseProvisioningProvider(svc.Infra.Provider)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		if strings.Contains(svc.Infra.Path, "\\") && !strings.Contains(svc.Infra.Path, "/") {
			svc.Infra.Path = strings.ReplaceAll(svc.Infra.Path, "\\", "/")
		}

		svc.Infra.Path = filepath.FromSlash(svc.Infra.Path)

		// TODO: Move parsing/validation requirements for service targets into their respective components.
		// When working within container based applications users may be using external/pre-built images instead of source
		// In this case it is valid to have not specified a language but would be required to specify a source image
		if svc.Host == ContainerAppTarget && svc.Language == ServiceLanguageNone && svc.Image.Empty() {
			return nil, fmt.Errorf("parsing service %s: must specify language or image", svc.Name)
		}

		if strings.ContainsRune(svc.RelativePath, '\\') && !strings.ContainsRune(svc.RelativePath, '/') {
			svc.RelativePath = strings.ReplaceAll(svc.RelativePath, "\\", "/")
		}

		svc.RelativePath = filepath.FromSlash(svc.RelativePath)

		if strings.ContainsRune(svc.OutputPath, '\\') && !strings.ContainsRune(svc.OutputPath, '/') {
			svc.OutputPath = strings.ReplaceAll(svc.OutputPath, "\\", "/")
		}

		svc.OutputPath = filepath.FromSlash(svc.OutputPath)
	}

	return &projectConfig, nil
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

	if projectConfig.Services != nil {
		hosts := make([]string, len(projectConfig.Services))
		languages := make([]string, len(projectConfig.Services))
		i := 0
		for _, svcConfig := range projectConfig.Services {
			hosts[i] = string(svcConfig.Host)
			languages[i] = string(svcConfig.Language)
			i++
		}

		slices.Sort(hosts)
		slices.Sort(languages)
	}

	projectConfig.Path = filepath.Dir(projectFilePath)
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

	projectBytes, err := yaml.Marshal(copy)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	projectFileContents := bytes.NewBufferString(projectSchemaAnnotation + "\n\n")
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return fmt.Errorf("preparing new project file contents: %w", err)
	}

	err = os.WriteFile(projectFilePath, projectFileContents.Bytes(), permissions.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	projectConfig.Path = projectFilePath

	return nil
}
