package devcenter

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"go.uber.org/multierr"
	"golang.org/x/exp/slices"
)

const (
	SourceKindDevCenter templates.SourceKind = "devcenter"
)

var SourceDevCenter = &templates.SourceConfig{
	Key:  "devcenter",
	Name: "Dev Center",
	Type: SourceKindDevCenter,
}

type TemplateSource struct {
	config          *Config
	manager         Manager
	devCenterClient devcentersdk.DevCenterClient
}

func NewTemplateSource(config *Config, manager Manager, devCenterClient devcentersdk.DevCenterClient) templates.Source {
	return &TemplateSource{
		config:          config,
		manager:         manager,
		devCenterClient: devCenterClient,
	}
}

func (s *TemplateSource) Name() string {
	return "DevCenter"
}

func (s *TemplateSource) ListTemplates(ctx context.Context) ([]*templates.Template, error) {
	var devCenterFilter DevCenterFilterPredicate
	var projectFilter ProjectFilterPredicate
	var catalogFilter EnvironmentDefinitionFilterPredicate

	if s.config.Name != "" {
		devCenterFilter = func(dc *devcentersdk.DevCenter) bool {
			return strings.EqualFold(dc.Name, s.config.Name)
		}
	}

	if s.config.Catalog != "" {
		catalogFilter = func(ed *devcentersdk.EnvironmentDefinition) bool {
			return strings.EqualFold(ed.CatalogName, s.config.Catalog)
		}
	}

	if s.config.Project != "" {
		projectFilter = func(p *devcentersdk.Project) bool {
			return strings.EqualFold(p.Name, s.config.Project)
		}
	}

	projects, err := s.manager.WritableProjectsWithFilter(ctx, devCenterFilter, projectFilter)
	if err != nil {
		return nil, fmt.Errorf("failed getting writable projects: %w", err)
	}

	templatesChan := make(chan *templates.Template)
	errorsChan := make(chan error)

	// Perform the lookup and checking for projects in parallel to speed up the process
	var wg sync.WaitGroup

	for _, project := range projects {
		wg.Add(1)

		go func(project *devcentersdk.Project) {
			defer wg.Done()

			// If a project is specified in the config then only consider templates for the specified project
			if s.config.Project != "" && !strings.EqualFold(s.config.Project, project.Name) {
				return
			}

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
				// Filter out environment definitions that do not match the specified catalog
				if catalogFilter != nil && !catalogFilter(envDefinition) {
					continue
				}

				// We only want to consider environment definitions that have
				// a repo url parameter as valid templates for azd
				var repoUrls []string
				var repoUrlParamId string
				containsRepoUrl := slices.ContainsFunc(envDefinition.Parameters, func(p devcentersdk.Parameter) bool {
					if strings.EqualFold(p.Id, "repourl") {
						repoUrlParamId = p.Id

						// Repo url parameter can support multiple values
						// Values can either have a default or multiple allowed values but not both
						if p.Allowed != nil && len(p.Allowed) > 0 {
							repoUrls = append(repoUrls, p.Allowed...)
						} else if p.Default != nil {
							defaultValue, ok := p.Default.(string)
							if ok && defaultValue != "" {
								repoUrls = append(repoUrls, defaultValue)
							}
						}

						return true
					}

					return false
				})

				if !containsRepoUrl {
					continue
				}

				definitionParts := []string{
					project.DevCenter.Name,
					envDefinition.CatalogName,
					envDefinition.Name,
				}
				definitionPath := strings.Join(definitionParts, "/")

				// List an available AZD template for each repo url that is referenced in the template
				for _, url := range repoUrls {
					templatesChan <- &templates.Template{
						Id:             url + definitionPath,
						Name:           envDefinition.Name,
						Source:         fmt.Sprintf("%s/%s", project.DevCenter.Name, envDefinition.CatalogName),
						Description:    envDefinition.Description,
						RepositoryPath: url,

						// Metadata will be used when creating any azd environments that are based on this template
						Metadata: templates.Metadata{
							Project: map[string]string{
								"platform.type":                                     string(PlatformKindDevCenter),
								fmt.Sprintf("%s.name", ConfigPath):                  project.DevCenter.Name,
								fmt.Sprintf("%s.catalog", ConfigPath):               envDefinition.CatalogName,
								fmt.Sprintf("%s.environmentDefinition", ConfigPath): envDefinition.Name,
							},
							Config: map[string]string{
								// Set the repoUrl param so it is not re-prompted by the provision provider
								fmt.Sprintf("provision.parameters.%s", repoUrlParamId): url,
							},
						},
					}
				}
			}
		}(project)
	}

	go func() {
		wg.Wait()
		close(templatesChan)
		close(errorsChan)
	}()

	var doneGroup sync.WaitGroup
	doneGroup.Add(2)

	var allErrors error
	distinctTemplates := []*templates.Template{}

	go func() {
		defer doneGroup.Done()

		for template := range templatesChan {
			contains := slices.ContainsFunc(distinctTemplates, func(t *templates.Template) bool {
				return t.Id == template.Id
			})

			if !contains {
				distinctTemplates = append(distinctTemplates, template)
			}
		}
	}()

	go func() {
		defer doneGroup.Done()

		for err := range errorsChan {
			allErrors = multierr.Append(allErrors, err)
		}
	}()

	// Wait for all the templates and errors to be processed from channels
	doneGroup.Wait()

	if allErrors != nil {
		return nil, allErrors
	}

	return distinctTemplates, nil
}

func (s *TemplateSource) GetTemplate(ctx context.Context, path string) (*templates.Template, error) {
	templateList, err := s.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list templates: %w", err)
	}

	// Attempt to match on the following:
	// Raw template id
	// Template path in the format: <devcenter>/<catalog>/<environment definition>
	// Template repository path
	for _, template := range templateList {
		if strings.EqualFold(template.Id, path) {
			return template, nil
		}

		templatePath := fmt.Sprintf("%s/%s", template.Source, template.Name)
		if strings.EqualFold(templatePath, path) {
			return template, nil
		}

		if strings.EqualFold(template.RepositoryPath, path) {
			return template, nil
		}
	}

	return nil, fmt.Errorf("template with path '%s' was not found, %w", path, templates.ErrTemplateNotFound)
}
