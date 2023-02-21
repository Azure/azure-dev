package templates

import (
	"context"
	"errors"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/maps"
)

type ApplicationType string

const (
	// A front-end SPA / static web app.
	WebApp ApplicationType = "web"
	// API only.
	ApiApp ApplicationType = "api"
	// Fullstack solution. Front-end SPA with back-end API.
	ApiWeb ApplicationType = "api-web"
)

var appTypesDisplay = map[string]ApplicationType{
	"API":                              ApiApp,
	"API with Single-Page Application": ApiWeb,
	"Web Application":                  WebApp,
}

type Characteristics struct {
	Type ApplicationType

	LanguageTags       []string
	InfrastructureTags []string

	// Capabilities specified in key OPERATOR value
	// runtime>1.10
	Capabilities []string
}

func (c *Characteristics) Description() string {
	return ""
}

// Attempts to match a template based on provided characteristics.
func Match(c Characteristics) []Template {
	if c.Type != "" {
		return []Template{{RepositoryPath: string(c.Type)}}
	}

	return nil
}

var ErrTemplateNotMatched = errors.New("no matching template")

// Attempts to match to a single template based on provided characteristics.
func MatchOne(ctx context.Context, console input.Console, c Characteristics) (Template, error) {
	matchedTemplates := Match(c)
	if len(matchedTemplates) == 0 {
		console.Message(
			ctx,
			"We couldn't find a matching template. Visit https://azure.github.io/awesome-azd/ for more options.")

		return Template{}, ErrTemplateNotMatched
	}

	if len(matchedTemplates) == 1 {
		return matchedTemplates[0], nil
	}

	templatesDisplay := map[string]*Template{}
	for _, template := range matchedTemplates {
		template := template
		templatesDisplay[template.Description] = &template
	}
	options := maps.Keys(templatesDisplay)
	sort.Strings(options)
	optIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message: "We found the following templates. Which one would you like to use?",
		Options: options,
	})
	if err != nil {
		return Template{}, err
	}

	return *templatesDisplay[options[optIndex]], nil
}

var languageDisplay = map[string]string{
	"C#":                     "csharp",
	"Python":                 "python",
	"Java":                   "java",
	"JavaScript (Front-end)": "web:ts",
	"TypeScript (Front-end)": "web:ts",
	"JavaScript (Back-end)":  "js",
	"TypeScript (Back-end)":  "ts",
}

func PromptToFillCharacteristics(ctx context.Context, console input.Console, c *Characteristics) error {
	if c.Type == "" {
		options := maps.Keys(appTypesDisplay)
		sort.Strings(options)
		options = append(options, "None of the above")
		appTypeIndex, err := console.Select(ctx, input.ConsoleOptions{
			Message: "Which best describes your application?",
			Options: options,
		})

		if err != nil {
			return err
		}

		if appTypeIndex != len(options)-1 {
			c.Type = appTypesDisplay[options[appTypeIndex]]
		}
	}

	langOptions := maps.Keys(languageDisplay)
	sort.Strings(langOptions)
	langOptions = append(langOptions, "Done")

	for {
		answerIndex, err := console.Select(ctx, input.ConsoleOptions{
			Message: "Which of the following languages does your application use? (Select 'Done' when done)",
			Options: langOptions,
		})
		if err != nil {
			return err
		}

		c.LanguageTags = append(c.LanguageTags, langOptions[answerIndex])

		if answerIndex == len(langOptions)-1 {
			break
		}
	}

	return nil
}
