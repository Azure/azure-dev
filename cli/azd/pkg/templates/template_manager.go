package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
	"golang.org/x/exp/maps"
)

type TemplateManager struct {
}

// Get a set of templates where each key is the name of the template
func (tm *TemplateManager) ListTemplates() (map[string]Template, error) {
	result := make(map[string]Template)
	var templates []Template
	err := json.Unmarshal(resources.TemplatesJson, &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	for _, template := range templates {
		result[template.Name] = template
	}

	return result, nil
}

func (tm *TemplateManager) GetTemplate(templateName string) (Template, error) {
	templates, err := tm.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("unable to list templates: %w", err)
	}

	if matchingTemplate, ok := templates[templateName]; ok {
		return matchingTemplate, nil
	}

	return Template{}, fmt.Errorf("template with name '%s' was not found", templateName)
}

func NewTemplateManager() *TemplateManager {
	return &TemplateManager{}
}

func (tm *TemplateManager) MatchTemplate(app appdetect.Application) Template {
	hasOneWebApp := false
	var webLanguage string
	apiLanguages := []string{}
	for _, proj := range app.Projects {
		if proj.Frameworks != nil && len(proj.Frameworks) > 0 {
			for _, framework := range proj.Frameworks {
				if framework.IsWebUIFramework() {
					hasOneWebApp = true
					webLanguage = framework.Display()
				}
			}
		} else {
			apiLanguages = append(apiLanguages, proj.Language)
		}
	}

	template := Template{}

	if len(app.Projects) == 1 && hasOneWebApp {
		template.RepositoryPath = "web"
		template.Name = fmt.Sprintf("Web Application (%s)", webLanguage)
	} else if hasOneWebApp {
		template.RepositoryPath = "api-web"
		template.Name = fmt.Sprintf("Web Application (%s) with API (%s)", webLanguage, strings.Join(apiLanguages, ", "))
	} else {
		template.RepositoryPath = "api"
		template.Name = fmt.Sprintf("Web API (%s)", strings.Join(apiLanguages, ", "))
	}

	return template
}

// PromptTemplate ask the user to select a template.
// An empty Template with default values is returned if the user selects 'Empty Template' from the choices
func PromptTemplate(ctx context.Context, message string, console input.Console) (Template, error) {
	var result Template
	templateManager := NewTemplateManager()
	templatesSet, err := templateManager.ListTemplates()

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	templateNames := []string{"Empty Template"}
	names := maps.Keys(templatesSet)
	sort.Strings(names)
	templateNames = append(templateNames, names...)

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      templateNames,
		DefaultValue: templateNames[0],
	})

	// separate this prompt from the next log
	console.Message(ctx, "")

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	if selectedIndex == 0 {
		return result, nil
	}

	selectedTemplateName := templateNames[selectedIndex]
	log.Printf("Selected template: %s", fmt.Sprint(selectedTemplateName))

	return templatesSet[selectedTemplateName], nil
}

func getAppTypeDisplayName(appType string) string {
	switch appType {
	case "api":
		return "API"
	case "api-web":
		return "API with Single-Page Application"
	case "web":
		return "Web Application"
	}

	return ""
}

var appTypes = map[string]string{
	"API":                              "api",
	"API with Single-Page Application": "api-web",
	"Web Application":                  "web",
}

func PromptInfraTemplate(ctx context.Context, console input.Console) (Template, error) {
	options := maps.Keys(appTypes)
	sort.Strings(options)
	appTypeIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which best describes your existing application?",
		Options: options,
	})

	if err != nil {
		return Template{}, err
	}

	displayOption := options[appTypeIndex]
	appTypeOption := appTypes[displayOption]
	if appTypeOption == "api" {
		db, err := console.Confirm(ctx, input.ConsoleOptions{
			Message: "Do you need a database?",
		})

		if err != nil {
			return Template{}, err
		}

		if db {
			appTypeOption += "-db"
		}
	}

	entries, err := resources.AppTypes.ReadDir("app-types")
	if err != nil {
		return Template{}, fmt.Errorf("reading app types FS: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == appTypeOption {
			return Template{
				Name:           displayOption,
				RepositoryPath: appTypeOption,
			}, nil
		}
	}

	return Template{}, fmt.Errorf("unable to find a matching template.")
}
