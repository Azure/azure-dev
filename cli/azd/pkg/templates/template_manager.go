package templates

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
	"golang.org/x/exp/slices"
)

type TemplateManager struct {
	configManager config.UserConfigManager
	sources       []Source
}

func NewTemplateManager(configManager config.UserConfigManager) (*TemplateManager, error) {
	return &TemplateManager{
		configManager: configManager,
	}, nil
}

type ListOptions struct {
	Source string
}

type sourceFilterPredicate func(config *SourceConfig) bool

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates(ctx context.Context, options *ListOptions) ([]*Template, error) {
	allTemplates := []*Template{}

	var filterPredicate sourceFilterPredicate
	if options != nil && options.Source != "" {
		filterPredicate = func(config *SourceConfig) bool {
			return strings.ToLower(config.Key) == strings.ToLower(options.Source)
		}
	}

	sources, err := tm.getSources(ctx, filterPredicate)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	for _, source := range sources {
		templates, err := source.ListTemplates(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list templates: %w", err)
		}

		slices.SortFunc(templates, func(a *Template, b *Template) bool {
			return a.RepositoryPath < b.RepositoryPath
		})

		allTemplates = append(allTemplates, templates...)
	}

	return allTemplates, nil
}

func (tm *TemplateManager) GetTemplate(ctx context.Context, name string) (*Template, error) {
	errors := []error{}

	sources, err := tm.getSources(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	for _, source := range sources {
		template, err := source.GetTemplate(ctx, name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		return template, nil
	}

	return nil, fmt.Errorf("unable to find template '%s': %w", name, errors[0])
}

func (tm *TemplateManager) getSources(ctx context.Context, filter sourceFilterPredicate) ([]Source, error) {
	if tm.sources != nil {
		return tm.sources, nil
	}

	configs, err := tm.getSourceConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed parsing template sources: %w", err)
	}

	sources, err := tm.createSourcesFromConfig(ctx, configs, filter)
	if err != nil {
		return nil, fmt.Errorf("failed initializing template sources: %w", err)
	}

	tm.sources = sources

	return tm.sources, nil
}

func (tm *TemplateManager) getSourceConfigs() (map[string]*SourceConfig, error) {
	config, err := tm.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("unable to load user configuration: %w", err)
	}

	sourceConfigs := map[string]*SourceConfig{}
	rawSources, ok := config.Get("templates.sources")
	if ok {
		sourceMap := rawSources.(map[string]interface{})
		for key, rawSource := range sourceMap {
			propMap, ok := rawSource.(map[string]interface{})
			if !ok {
				continue
			}

			sourceType, ok := propMap["type"].(string)
			if !ok {
				return nil, fmt.Errorf("unable to parse source type for '%s'", key)
			}

			name, ok := propMap["name"].(string)
			if !ok {
				name = key
			}

			location, ok := propMap["location"].(string)
			if !ok {
				return nil, fmt.Errorf("unable to parse source location for '%s'", key)
			}

			sourceConfigs[key] = &SourceConfig{
				Key:      key,
				Type:     SourceKind(sourceType),
				Name:     name,
				Location: location,
			}
		}
	}

	rawAwesome, ok := config.Get("templates.awesome")
	if ok {
		boolValue, err := strconv.ParseBool(rawAwesome.(string))
		if err == nil && boolValue {
			sourceConfigs["awesome-azd"] = &SourceConfig{
				Key:      "awesome-azd",
				Name:     "Awesome AZD",
				Type:     SourceUrl,
				Location: "https://raw.githubusercontent.com/wbreza/azure-dev/template-source/cli/azd/resources/awesome-templates.json",
			}
		}
	} else {
		sourceConfigs["default"] = &SourceConfig{
			Key:  "default",
			Name: "Default",
			Type: SourceResource,
		}
	}

	return sourceConfigs, nil
}

func (tm *TemplateManager) createSourcesFromConfig(
	ctx context.Context,
	configs map[string]*SourceConfig,
	filter sourceFilterPredicate,
) ([]Source, error) {
	sources := []Source{}

	for name, config := range configs {
		if filter != nil && !filter(config) {
			continue
		}

		var source Source
		var err error

		switch config.Type {
		case SourceFile:
			source, err = NewFileTemplateSource(config.Name, config.Location)
		case SourceUrl:
			source, err = NewUrlTemplateSource(ctx, config.Name, config.Location)
		case SourceResource:
			source, err = NewJsonTemplateSource(config.Name, string(resources.TemplatesJson))
		}

		if err != nil {
			return nil, fmt.Errorf("unable to create template source '%s': %w", name, err)
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(ctx context.Context, message string, console input.Console) (*Template, error) {
	templateManager, err := NewTemplateManager(config.NewUserConfigManager())
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	templates, err := templateManager.ListTemplates(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	choices := make([]string, 0, len(templates)+1)

	// prepend the minimal template option to guarantee first selection
	choices = append(choices, "Minimal")
	for _, template := range templates {
		choices = append(choices, fmt.Sprintf("%s (%s)", template.Name, template.RepositoryPath))
	}

	selected, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      choices,
		DefaultValue: choices[0],
	})

	// separate this prompt from the next log
	console.Message(ctx, "")

	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	if selected == 0 {
		return nil, nil
	}

	template := templates[selected-1]
	log.Printf("Selected template: %s", fmt.Sprint(template.RepositoryPath))

	return template, nil
}
