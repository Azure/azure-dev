package templates

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"golang.org/x/exp/slices"
)

type DevCenterSource struct {
	devCenterClient devcentersdk.DevCenterClient
}

func NewDevCenterSource(devCenterClient devcentersdk.DevCenterClient) *DevCenterSource {
	return &DevCenterSource{
		devCenterClient: devCenterClient,
	}
}

func (s *DevCenterSource) Name() string {
	return "DevCenter"
}

func (s *DevCenterSource) ListTemplates(ctx context.Context) ([]*Template, error) {
	devCenterList, err := s.devCenterClient.DevCenters().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting dev centers: %w", err)
	}

	var templates []*Template

	for _, devCenter := range devCenterList.Value {
		projects, err := s.devCenterClient.
			DevCenterByEndpoint(devCenter.ServiceUri).
			Projects().
			Get(ctx)

		if err != nil {
			return nil, fmt.Errorf("failed getting projects for dev center %s: %w", devCenter.Name, err)
		}

		for _, project := range projects.Value {
			envDefinitions, err := s.devCenterClient.
				DevCenterByEndpoint(devCenter.ServiceUri).
				ProjectByName(project.Name).
				EnvironmentDefinitions().
				Get(ctx)

			if err != nil {
				return nil, fmt.Errorf("failed getting environment definitions for project %s: %w", project.Name, err)
			}

			for _, envDefinition := range envDefinitions.Value {
				// We only want to consider environment definitions that have
				// a repo url parameter as valid templates for azd
				containsRepoUrl := slices.ContainsFunc(envDefinition.Parameters, func(p devcentersdk.Parameter) bool {
					return strings.ToLower(p.Name) == "repourl"
				})

				if containsRepoUrl {
					definitionParts := []string{devCenter.Name, project.Name, envDefinition.CatalogName, envDefinition.Name}
					definitionPath := strings.Join(definitionParts, "/")

					templates = append(templates, &Template{
						Name:           envDefinition.Name,
						Source:         envDefinition.CatalogName,
						Description:    envDefinition.Description,
						RepositoryPath: definitionPath,
					})
				}
			}
		}
	}

	return templates, nil
}

func (s *DevCenterSource) GetTemplate(ctx context.Context, path string) (*Template, error) {
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list templates: %w", err)
	}

	for _, template := range templates {
		if template.RepositoryPath == path {
			return template, nil
		}
	}

	return nil, fmt.Errorf("template with path '%s' was not found, %w", path, ErrTemplateNotFound)
}
