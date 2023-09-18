package templates

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
	projects, err := s.getWritableProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting writable projects: %w", err)
	}

	templatesChan := make(chan *Template)
	defer close(templatesChan)
	templates := []*Template{}
	go func() {
		for template := range templatesChan {
			templates = append(templates, template)
		}
	}()

	errorsChan := make(chan error)
	defer close(errorsChan)
	errors := []error{}
	go func() {
		for err := range errorsChan {
			errors = append(errors, err)
		}
	}()

	var wg sync.WaitGroup

	for _, project := range projects {
		wg.Add(1)

		go func(project *devcentersdk.Project) {
			defer wg.Done()

			envDefinitions, err := s.devCenterClient.
				DevCenterByEndpoint(project.DevCenter.ServiceUri).
				ProjectByName(project.Name).
				EnvironmentDefinitions().
				Get(ctx)

			if err != nil {
				errorsChan <- err
				return
			}

			for _, envDefinition := range envDefinitions.Value {
				// We only want to consider environment definitions that have
				// a repo url parameter as valid templates for azd
				containsRepoUrl := slices.ContainsFunc(envDefinition.Parameters, func(p devcentersdk.Parameter) bool {
					return strings.ToLower(p.Name) == "repourl"
				})

				if containsRepoUrl {
					definitionParts := []string{
						project.DevCenter.Name,
						project.Name,
						envDefinition.CatalogName,
						envDefinition.Name,
					}
					definitionPath := strings.Join(definitionParts, "/")

					templatesChan <- &Template{
						Name:           envDefinition.Name,
						Source:         envDefinition.CatalogName,
						Description:    envDefinition.Description,
						RepositoryPath: definitionPath,
					}
				}
			}
		}(project)
	}

	wg.Wait()

	if len(errors) > 0 {
		return nil, errors[0]
	}

	return templates, nil
}

func (s *DevCenterSource) getWritableProjects(ctx context.Context) ([]*devcentersdk.Project, error) {
	devCenterList, err := s.devCenterClient.DevCenters().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting dev centers: %w", err)
	}

	errorsChan := make(chan error)
	defer close(errorsChan)
	errors := []error{}
	go func() {
		for err := range errorsChan {
			errors = append(errors, err)
		}
	}()

	projectsChan := make(chan *devcentersdk.Project)
	writeableProjects := []*devcentersdk.Project{}
	defer close(projectsChan)
	go func() {
		for project := range projectsChan {
			writeableProjects = append(writeableProjects, project)
		}
	}()

	var wg sync.WaitGroup

	for _, devCenter := range devCenterList.Value {
		wg.Add(1)

		go func(dc *devcentersdk.DevCenter) {
			defer wg.Done()

			projects, err := s.devCenterClient.
				DevCenterByEndpoint(dc.ServiceUri).
				Projects().
				Get(ctx)

			if err != nil {
				errorsChan <- err
				return
			}

			for _, project := range projects.Value {
				wg.Add(1)

				go func(p *devcentersdk.Project) {
					defer wg.Done()

					hasWriteAccess := s.devCenterClient.
						DevCenterByEndpoint(p.DevCenter.ServiceUri).
						ProjectByName(p.Name).
						Permissions().
						HasWriteAccess(ctx)

					if hasWriteAccess {
						projectsChan <- p
					}
				}(project)
			}
		}(devCenter)
	}

	wg.Wait()

	if len(errorsChan) > 0 {
		return nil, errors[0]
	}

	return writeableProjects, nil
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
