package project

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/blang/semver/v4"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

const (
	//nolint:lll
	projectSchemaAnnotation = "# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json"

	cInfraDirectory = "infra"
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

	for key, svc := range projectConfig.Services {
		svc.Name = key
		svc.Project = &projectConfig
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

		var err error
		svc.Language, err = parseServiceLanguage(svc.Language)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.Host, err = parseServiceHost(svc.Host)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.Infra.Provider, err = provisioning.ParseProvider(svc.Infra.Provider)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}
	}

	if projectConfig.Infra.Path == "" {
		projectConfig.Infra.Path = cInfraDirectory
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

		tracing.SetUsageAttributes(fields.ProjectServiceLanguagesKey.StringSlice(languages))
		tracing.SetUsageAttributes(fields.ProjectServiceHostsKey.StringSlice(hosts))
	}

	projectConfig.Path = filepath.Dir(projectFilePath)
	return projectConfig, nil
}

// Saves the current instance back to the azure.yaml file
func Save(ctx context.Context, projectConfig *ProjectConfig, projectFilePath string) error {
	projectBytes, err := yaml.Marshal(projectConfig)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	projectFileContents := bytes.NewBufferString(projectSchemaAnnotation + "\n\n")
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return fmt.Errorf("preparing new project file contents: %w", err)
	}

	err = os.WriteFile(projectFilePath, projectFileContents.Bytes(), osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	projectConfig.Path = projectFilePath

	return nil
}
