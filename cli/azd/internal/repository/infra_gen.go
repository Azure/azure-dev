package repository

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/otiai10/copy"
	"golang.org/x/exp/maps"
)

// A regex that matches against "likely" well-formed database names
var wellFormedDbNameRegex = regexp.MustCompile(`^[a-zA-Z-_0-9]$`)

type DatabasePostgres struct {
	DatabaseUser string
	DatabaseName string
}

type DatabaseCosmos struct {
	DatabaseName string
}

type Parameter struct {
	Name   string
	Value  string
	Type   string
	Secret bool
}

type InfraSpec struct {
	Parameters []Parameter
	Services   []ServiceSpec

	// Databases to create
	DbPostgres *DatabasePostgres
	DbCosmos   *DatabaseCosmos
}

type Frontend struct {
	Backends []ServiceSpec
}

type Backend struct {
	Frontends []ServiceSpec
}

type ServiceSpec struct {
	Name string
	Port int

	// Front-end properties.
	Frontend *Frontend

	// Back-end properties
	Backend *Backend

	// Connection to a database. Only one should be set.
	DbPostgres *DatabasePostgres
	DbCosmos   *DatabaseCosmos
}

func supportedLanguages() []appdetect.ProjectType {
	return []appdetect.ProjectType{
		appdetect.DotNet,
		appdetect.Java,
		appdetect.JavaScript,
		appdetect.TypeScript,
		appdetect.Python,
	}
}

func mapLanguage(l appdetect.ProjectType) project.ServiceLanguageKind {
	switch l {
	case appdetect.Python:
		return project.ServiceLanguagePython
	case appdetect.DotNet:
		return project.ServiceLanguageDotNet
	case appdetect.JavaScript:
		return project.ServiceLanguageJavaScript
	case appdetect.TypeScript:
		return project.ServiceLanguageTypeScript
	case appdetect.Java:
		return project.ServiceLanguageJava
	default:
		return ""
	}
}

func supportedFrameworks() []appdetect.Framework {
	return []appdetect.Framework{
		appdetect.Angular,
		appdetect.JQuery,
		appdetect.VueJs,
		appdetect.React,
	}
}

type DatabaseKind string

const (
	DbPostgre     DatabaseKind = "postgres"
	DbCosmosMongo DatabaseKind = "cosmos-mongo"
)

func mapDatabase(d appdetect.Framework) DatabaseKind {
	switch d {
	case appdetect.DbMongo:
		return DbCosmosMongo
	case appdetect.DbPostgres:
		return DbPostgre
	default:
		return ""
	}
}

func supportedDatabases() []DatabaseKind {
	return []DatabaseKind{
		DbPostgre,
		DbCosmosMongo,
	}
}

func (f DatabaseKind) Display() string {
	switch f {
	case DbPostgre:
		return "PostgreSQL"
	case DbCosmosMongo:
		return "MongoDB"
	}

	return ""
}

func projectDisplayName(p appdetect.Project) string {
	name := p.Language.Display()
	for _, framework := range p.Frameworks {
		if framework.IsWebUIFramework() {
			name = framework.Display()
		}
	}

	return name
}

func (i *Initializer) InitializeInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) error {
	wd := azdCtx.ProjectDirectory()
	title := "Scanning app code in " + output.WithHighLightFormat(wd)
	i.console.ShowSpinner(ctx, title, input.Step)
	projects, err := appdetect.Detect(wd)
	i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

	if err != nil {
		return err
	}

	detectedDbs := make(map[appdetect.Framework]struct{})
	for _, project := range projects {
		for _, framework := range project.Frameworks {
			if framework.IsDatabaseDriver() {
				if _, recorded := detectedDbs[framework]; recorded {
					continue
				}
			}

			detectedDbs[framework] = struct{}{}
		}
	}

	firstConfirmation := true

confirmDetection:
	for {
		if firstConfirmation {
			i.console.MessageUxItem(ctx, &ux.DoneMessage{Message: "Generating recommended Azure services"})
			i.console.Message(ctx, "\n"+output.WithBold("App detection summary:")+"\n")
		} else {
			i.console.MessageUxItem(ctx, &ux.DoneMessage{Message: "Revising app detection summary"})
			i.console.Message(ctx, "\n"+output.WithBold("Revised app detection summary:")+"\n")
		}
		firstConfirmation = false

		for _, project := range projects {
			status := ""
			if project.DetectionRule == "Updated" {
				status = " [Updated]"
			} else if project.DetectionRule == "Manual" {
				status = " [Added]"
			}

			i.console.Message(ctx, "  "+output.WithBold(projectDisplayName(project))+status)

			rel, err := filepath.Rel(wd, project.Path)
			if err != nil {
				return err
			}

			relWithDot := "./" + rel
			i.console.Message(ctx, "  "+"Detected in: "+output.WithHighLightFormat(relWithDot))
			i.console.Message(ctx, "  "+"Recommended: "+"Azure Container Apps")
			i.console.Message(ctx, "")
		}

		// handle detectedDbs
		for _, db := range maps.Keys(detectedDbs) {
			recommended := ""
			switch db {
			case appdetect.DbPostgres:
				recommended = "Azure Database for PostgreSQL flexible server"
			case appdetect.DbMongo:
				recommended = "CosmosDB API for MongoDB"
			}
			if recommended != "" {
				i.console.Message(ctx, "  "+output.WithBold(db.Display()))
				i.console.Message(ctx, "  "+"Recommended: "+recommended)
				i.console.Message(ctx, "")
			}
		}

		i.console.Message(ctx,
			"azd will generate the files necessary to host your app on Azure using the recommended services.")

		continueOption, err := i.console.Select(ctx, input.ConsoleOptions{
			Message: "Select an option",
			Options: []string{
				"Configure my app to run on Azure using the recommended services",
				"Add a language or database azd failed to detect",
				"Modify or remove a detected language or database",
			},
		})
		if err != nil {
			return err
		}

		switch continueOption {
		case 0:
			break confirmDetection
		case 1:
			languages := supportedLanguages()
			frameworks := supportedFrameworks()
			databases := supportedDatabases()
			selections := make([]string, 0, len(languages)+len(databases))
			entries := make([]any, 0, len(languages)+len(databases))

			for _, lang := range languages {
				selections = append(selections, fmt.Sprintf("%s\t%s", lang.Display(), "[Language]"))
				entries = append(entries, lang)
			}

			for _, framework := range frameworks {
				selections = append(selections, fmt.Sprintf("%s\t%s", framework.Display(), "[Language]"))
				entries = append(entries, framework)
			}

			for _, db := range databases {
				selections = append(selections, fmt.Sprintf("%s\t%s", db.Display(), "[Database]"))
				entries = append(entries, db)
			}

			tabbed := strings.Builder{}
			tabW := tabwriter.NewWriter(&tabbed, 0, 0, 3, ' ', 0)
			_, err = tabW.Write([]byte(strings.Join(selections, "\n")))
			if err != nil {
				return err
			}
			err = tabW.Flush()
			if err != nil {
				return err
			}

			selections = strings.Split(tabbed.String(), "\n")

			entIdx, err := i.console.Select(ctx, input.ConsoleOptions{
				Message: "Select a language or database to add",
				Options: selections,
			})
			if err != nil {
				return err
			}

			s := appdetect.Project{}
			switch entries[entIdx].(type) {
			case appdetect.ProjectType:
				s.Language = entries[entIdx].(appdetect.ProjectType)
			case appdetect.Framework:
				framework := entries[entIdx].(appdetect.Framework)
				s.Frameworks = []appdetect.Framework{framework}
				s.Language = appdetect.JavaScript
			}

			for {
				path, err := i.console.Prompt(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Enter file path of the directory that uses '%s'", projectDisplayName(s)),
					Options: selections,
					Suggest: func(input string) (completions []string) {
						matches, _ := filepath.Glob(input + "*")
						for _, match := range matches {
							if fs, err := os.Stat(match); err == nil && fs.IsDir() {
								completions = append(completions, match)
							}
						}

						return completions
					},
				})
				if err != nil {
					return err
				}

				fs, err := os.Stat(path)
				if errors.Is(err, os.ErrNotExist) || fs != nil && !fs.IsDir() {
					i.console.Message(ctx, fmt.Sprintf("'%s' is not a valid directory", path))
					continue
				}

				if err != nil {
					return err
				}

				path, err = filepath.Abs(path)
				if err != nil {
					return err
				}

				for idx, project := range projects {
					if project.Path == path {
						i.console.Message(
							ctx,
							fmt.Sprintf(
								"\nazd previously detected '%s' at %s.\n", projectDisplayName(project), project.Path))

						confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
							Message: fmt.Sprintf(
								"Do you want to change the detected service to '%s'", projectDisplayName(s)),
						})
						if err != nil {
							return err
						}
						if confirm {
							projects[idx].Language = s.Language
							projects[idx].Frameworks = s.Frameworks
							projects[idx].DetectionRule = "Updated"
						}

						continue confirmDetection
					}
				}

				s.Path = filepath.Clean(path)
				s.DetectionRule = "Manual"
				projects = append(projects, s)
				break
			}
		}
	}

	spec := InfraSpec{}
	for database := range detectedDbs {
	dbPrompt:
		for {
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: "Input a name for the app database",
			})
			if err != nil {
				return err
			}

			if dbName == "" {
				continue dbPrompt
			}

			if strings.ContainsAny(dbName, " ") {
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: "Database name contains whitespace. " +
						"This may not be allowed by the database server. Continue?",
				})
				if err != nil {
					return err
				}

				if confirm {
					break dbPrompt
				} else {
					continue dbPrompt
				}
			}

			if !wellFormedDbNameRegex.MatchString(dbName) {
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: "Database name contains special characters. " +
						"This may not be allowed by the database server. Continue?",
				})
				if err != nil {
					return err
				}

				if confirm {
					break dbPrompt
				} else {
					continue dbPrompt
				}
			}

			switch database {
			case appdetect.DbMongo:
				spec.DbCosmos = &DatabaseCosmos{
					DatabaseName: dbName,
				}

				break dbPrompt
			case appdetect.DbPostgres:
				spec.DbPostgres = &DatabasePostgres{
					DatabaseName: dbName,
				}

				spec.Parameters = append(spec.Parameters,
					Parameter{
						Name:   "sqlAdminPassword",
						Value:  "$(secretOrRandomPassword)",
						Type:   "string",
						Secret: true,
					},
					Parameter{
						Name:   "appUserPassword",
						Value:  "$(secretOrRandomPassword)",
						Type:   "string",
						Secret: true,
					})
				break dbPrompt
			}
		}
	}

	backends := []ServiceSpec{}
	frontends := []ServiceSpec{}
	for _, project := range projects {
		name := filepath.Base(project.Path)
		var port int
		for {
			val, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: "What port does '" + name + "' listen on? (0 means no exposed ports)",
			})
			if err != nil {
				return err
			}

			port, err = strconv.Atoi(val)
			if err == nil {
				break
			}
			i.console.Message(ctx, "Must be an integer. Try again or press Ctrl+C to cancel")
		}

		serviceSpec := ServiceSpec{
			Name: name,
			Port: port,
		}

		for _, framework := range project.Frameworks {
			if framework.IsDatabaseDriver() {
				switch framework {
				case appdetect.DbMongo:
					serviceSpec.DbCosmos = spec.DbCosmos
				case appdetect.DbPostgres:
					serviceSpec.DbPostgres = spec.DbPostgres
				}
			}

			if framework.IsWebUIFramework() {
				serviceSpec.Frontend = &Frontend{}
			}
		}

		if serviceSpec.Frontend == nil && serviceSpec.Port > 0 {
			backends = append(backends, serviceSpec)
			serviceSpec.Backend = &Backend{}
		} else {
			frontends = append(frontends, serviceSpec)
		}

		spec.Services = append(spec.Services, serviceSpec)
	}

	// Link services together
	for _, service := range spec.Services {
		if service.Frontend != nil {
			service.Frontend.Backends = backends
		}

		if service.Backend != nil {
			service.Backend.Frontends = frontends
		}

		spec.Parameters = append(spec.Parameters, Parameter{
			Name:  bicepName(service.Name) + "Exists",
			Value: fmt.Sprintf("${SERVICE_%s_RESOURCE_EXISTS=false}", strings.ToUpper(service.Name)),
			Type:  "bool",
		})
	}

	confirm, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Do you want to continue?",
		Options: []string{
			"Yes - Generate files to host my app on Azure using the recommended services",
			"No - Modify detected languages or databases",
		},
	})
	if err != nil {
		return err
	}

	if confirm == 1 {
		// modify
		panic("modify unimplemented")
	}

	generateProject := func() error {
		title := "Generating " + output.WithBold(azdcontext.ProjectFileName) +
			" in " + output.WithHighLightFormat(azdCtx.ProjectDirectory())
		i.console.ShowSpinner(ctx, title, input.Step)
		defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
		config, err := DetectionToConfig(wd, projects)
		if err != nil {
			return fmt.Errorf("converting config: %w", err)
		}
		err = project.Save(
			context.Background(),
			&config,
			filepath.Join(wd, azdcontext.ProjectFileName))
		if err != nil {
			return fmt.Errorf("generating azure.yaml: %w", err)
		}
		return nil
	}

	err = generateProject()
	if err != nil {
		return err
	}

	target := filepath.Join(azdCtx.ProjectDirectory(), "infra")
	title = "Generating Infrastructure as Code files in " + output.WithHighLightFormat(target)
	i.console.ShowSpinner(ctx, title, input.Step)
	defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

	staging, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}

	defer func() { _ = os.RemoveAll(staging) }()

	err = copyFS(resources.ScaffoldBase, "scaffold/base", staging)
	if err != nil {
		return fmt.Errorf("copying to staging: %w", err)
	}

	stagingApp := filepath.Join(staging, "app")
	if err := os.MkdirAll(stagingApp, osutil.PermissionDirectory); err != nil {
		return err
	}

	funcMap := template.FuncMap{
		"bicepName": bicepName,
		"upper":     strings.ToUpper,
	}

	root := "scaffold/templates"
	t, err := template.New("templates").
		Option("missingkey=error").
		Funcs(funcMap).
		ParseFS(resources.ScaffoldTemplates,
			path.Join(root, "*.bicept"),
			path.Join(root, "*.jsont"))
	if err != nil {
		return fmt.Errorf("parsing templates: %w", err)
	}

	if spec.DbCosmos != nil {
		err = execute(t, "db-cosmos.bicep", spec.DbCosmos, filepath.Join(stagingApp, "db-cosmos.bicep"))
		if err != nil {
			return err
		}
	}

	if spec.DbPostgres != nil {
		err = execute(t, "db-postgre.bicep", spec.DbPostgres, filepath.Join(stagingApp, "db-postgre.bicep"))
		if err != nil {
			return err
		}
	}

	for _, svc := range spec.Services {
		err = execute(t, "host-containerapp.bicep", svc, filepath.Join(stagingApp, svc.Name+".bicep"))
		if err != nil {
			return err
		}
	}

	err = execute(t, "main.bicep", spec, filepath.Join(staging, "main.bicep"))
	if err != nil {
		return err
	}

	err = execute(t, "main.parameters.json", spec, filepath.Join(staging, "main.parameters.json"))
	if err != nil {
		return err
	}

	if err := os.MkdirAll(target, osutil.PermissionDirectory); err != nil {
		return err
	}

	if err := copy.Copy(staging, target); err != nil {
		return fmt.Errorf("copying contents from temp staging directory: %w", err)
	}

	return nil
}

func execute(t *template.Template, name string, data any, writePath string) error {
	buf := bytes.NewBufferString("")
	err := t.ExecuteTemplate(buf, name, data)
	if err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	err = os.WriteFile(writePath, buf.Bytes(), osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("writing service file: %w", err)
	}
	return nil
}

func bicepName(name string) string {
	sb := strings.Builder{}
	separatorStart := -1
	for pos, char := range name {
		switch char {
		case '-', '_':
			separatorStart = pos
		default:
			if separatorStart != -1 {
				char = unicode.ToUpper(char)
			}
			separatorStart = -1

			if _, err := sb.WriteRune(char); err != nil {
				panic(err)
			}
		}
	}

	return sb.String()
}

func copyFS(embedFs embed.FS, root string, target string) error {
	return fs.WalkDir(embedFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(embedFs, name)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
