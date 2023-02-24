package templates

import (
	"context"
	"errors"
	"sort"

	"github.com/AlecAivazis/survey/v2"
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

var noSqlDatabaseDisplay = map[string]string{
	"Azure Cosmos DB with MongoDB API": "mongodb",
	"Other":                            "other",
}

var sqlDatabaseDisplay = map[string]string{
	"Azure Database for PostgreSQL": "azuredb-postgreSQL",
	"Azure Database for MySQL":      "azuresql",
}

// TODO: This should be derivable from a project config
type Characteristics struct {
	Type ApplicationType

	LanguageTags       []string
	DatabaseTags       []string
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
			Message: "Which best describes your app?",
			Options: options,
		})

		if err != nil {
			return err
		}

		if appTypeIndex != len(options)-1 {
			c.Type = appTypesDisplay[options[appTypeIndex]]
		}
	}

	if len(c.LanguageTags) == 0 {
		langOptions := maps.Keys(languageDisplay)
		sort.Strings(langOptions)

		answers := []string{}
		prompt := &survey.MultiSelect{
			Message: "Which of the following languages does your app use?",
			Options: langOptions,
		}

		err := survey.AskOne(prompt, &answers)
		if err != nil {
			return err
		}

		for _, ans := range answers {
			c.LanguageTags = append(c.LanguageTags, languageDisplay[ans])
		}
	}

	if len(c.DatabaseTags) == 0 {
		promptDb(ctx, console, c)
	}

	return nil
}

func promptDb(ctx context.Context, console input.Console, c *Characteristics) error {
	if needDb, err := console.Confirm(ctx, input.ConsoleOptions{
		Message: "Do you need a database for your app?",
	}); err != nil {
		return err
	} else if !needDb {
		return nil
	}

	databases := []string{
		"NoSQL Database",
		"Relational SQL Database",
	}
	answerIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      "Which type of database would you like to use?",
		Options:      databases,
		DefaultValue: "NoSQL Database",
	})
	if err != nil {
		return err
	}

	var dbOptions []string
	if answerIndex == 0 {
		dbOptions = maps.Keys(noSqlDatabaseDisplay)
	} else {
		dbOptions = maps.Keys(sqlDatabaseDisplay)
	}
	sort.Strings(dbOptions)

	answerIndex, err = console.Select(ctx, input.ConsoleOptions{
		Message: "Which database would you like to use?",
		Options: dbOptions,
	})
	if err != nil {
		return err
	}

	c.DatabaseTags = append(c.DatabaseTags, dbOptions[answerIndex])
	return nil
}
