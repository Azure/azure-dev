package templates

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/theckman/yacspin"
	"golang.org/x/exp/slices"
)

var (
	ErrTemplateNotFound = fmt.Errorf("template not found")
)

type TemplateManager struct {
	sourceManager SourceManager
	sources       []Source
}

func NewTemplateManager(sourceManager SourceManager) (*TemplateManager, error) {
	return &TemplateManager{
		sourceManager: sourceManager,
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
			return strings.EqualFold(config.Key, options.Source)
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

		// Sort by source, then repository path and finally name
		slices.SortFunc(templates, func(a *Template, b *Template) bool {
			if a.Source != b.Source {
				return a.Source < b.Source
			}

			if a.RepositoryPath != b.RepositoryPath {
				return a.RepositoryPath < b.RepositoryPath
			}

			return a.Name < b.Name
		})

		allTemplates = append(allTemplates, templates...)
	}

	return allTemplates, nil
}

func (tm *TemplateManager) GetTemplate(ctx context.Context, path string) (*Template, error) {
	absTemplatePath, err := Absolute(path)
	if err != nil {
		return nil, err
	}

	allTemplates, err := tm.ListTemplates(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	matchingIndex := slices.IndexFunc(allTemplates, func(template *Template) bool {
		// Template ID match is used for more precise matching
		// for template sources like DevCenter
		if template.Id != "" && template.Id == path {
			return true
		}

		absPath, err := Absolute(template.RepositoryPath)
		if err != nil {
			log.Printf("failed to get absolute path for template '%s': %s", template.RepositoryPath, err.Error())
			return false
		}

		return absPath == absTemplatePath
	})

	if matchingIndex == -1 {
		return nil, fmt.Errorf("template with name '%s' was not found, %w", path, ErrTemplateNotFound)
	}

	return allTemplates[matchingIndex], nil
}

func (tm *TemplateManager) getSources(ctx context.Context, filter sourceFilterPredicate) ([]Source, error) {
	if tm.sources != nil {
		return tm.sources, nil
	}

	configs, err := tm.sourceManager.List(ctx)
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

func (tm *TemplateManager) createSourcesFromConfig(
	ctx context.Context,
	configs []*SourceConfig,
	filter sourceFilterPredicate,
) ([]Source, error) {
	sources := []Source{}

	for _, config := range configs {
		if filter != nil && !filter(config) {
			continue
		}

		source, err := tm.sourceManager.CreateSource(ctx, config)
		if err != nil {
			log.Printf("failed to create source: %s", err.Error())
			continue
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(
	ctx context.Context,
	message string,
	templateManager *TemplateManager,
	console input.Console,
) (*Template, error) {
	templates, err := templateManager.ListTemplates(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	// To support non-interactive unit tests scenarios we only add the line breaks and repo path in interactive TTY sessions.
	terminalMode := input.GetSpinnerTerminalMode(nil)
	nonInteractiveMode := yacspin.ForceNoTTYMode | yacspin.ForceDumbTerminalMode
	isInteractive := terminalMode != nonInteractiveMode

	choices := make([]string, 0, len(templates)+1)

	// prepend the minimal template option to guarantee first selection
	minimalChoice := "Minimal"
	if isInteractive {
		minimalChoice += "\n"
	}

	choices = append(choices, minimalChoice)
	for _, template := range templates {
		templateChoice := template.Name
		if isInteractive {
			repoPath := output.WithGrayFormat("(%s)", template.RepositoryPath)
			templateChoice += fmt.Sprintf("\n  %s\n", repoPath)
		}
		choices = append(choices, templateChoice)
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
