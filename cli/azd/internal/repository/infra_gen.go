package repository

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/fatih/color"
	"github.com/otiai10/copy"
	"golang.org/x/exp/maps"
)

// A regex that matches against "likely" well-formed database names
var wellFormedDbNameRegex = regexp.MustCompile(`^[a-zA-Z\-_0-9]*$`)

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

func supportedDatabases() []appdetect.Framework {
	return []appdetect.Framework{
		appdetect.DbMongo,
		appdetect.DbPostgres,
	}
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

func dirSuggestions(input string) []string {
	completions := []string{}
	matches, _ := filepath.Glob(input + "*")
	for _, match := range matches {
		if fs, err := os.Stat(match); err == nil && fs.IsDir() {
			completions = append(completions, match)
		}
	}
	return completions
}

func tabWrite(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

func promptDir(
	ctx context.Context,
	console input.Console,
	message string) (string, error) {
	for {
		path, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: message,
			Suggest: dirSuggestions,
		})
		if err != nil {
			return "", err
		}

		fs, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) || fs != nil && !fs.IsDir() {
			console.Message(ctx, fmt.Sprintf("'%s' is not a valid directory", path))
			continue
		}

		if err != nil {
			return "", err
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return path, err
	}
}

type EntryKind string

const (
	EntryKindDetected EntryKind = "detection"
	EntryKindManual   EntryKind = "manual"
	EntryKindModified EntryKind = "modified"
)

func (i *Initializer) InitializeInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	initializeEnv func() error) error {
	wd := azdCtx.ProjectDirectory()
	i.console.Message(ctx, "")
	title := "Scanning app code in current directory"
	i.console.ShowSpinner(ctx, title, input.Step)
	projects, err := appdetect.Detect(wd)
	time.Sleep(1 * time.Second)

	i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

	if err != nil {
		return err
	}

	detectedDbs := make(map[appdetect.Framework]EntryKind)
	for _, project := range projects {
		for _, framework := range project.Frameworks {
			if framework.IsDatabaseDriver() {
				detectedDbs[framework] = EntryKindDetected
			}
		}
	}

	revision := false

confirmDetection:
	for {
		if revision {
			i.console.ShowSpinner(ctx, "Revising detected services", input.Step)
			time.Sleep(1 * time.Second)
			i.console.StopSpinner(ctx, "Revising detected services", input.StepDone)
			i.console.Message(ctx, "\n"+output.WithBold("Detected services (Revised):")+"\n")
		} else {
			i.console.Message(ctx, "\n"+output.WithBold("Detected services:")+"\n")
		}
		// assume changes will be made by default
		revision = true

		recommendedServices := []string{}
		for _, project := range projects {
			status := ""
			if project.DetectionRule == string(EntryKindModified) {
				status = " " + output.WithSuccessFormat("[Updated]")
			} else if project.DetectionRule == string(EntryKindManual) {
				status = " " + output.WithSuccessFormat("[Added]")
			}

			i.console.Message(ctx, "  "+output.WithBlueFormat(projectDisplayName(project))+status)

			rel, err := filepath.Rel(wd, project.Path)
			if err != nil {
				return err
			}
			relWithDot := "."
			if rel != "." {
				relWithDot = "./" + rel
			}
			i.console.Message(ctx, "  "+"Detected in: "+output.WithHighLightFormat(relWithDot))
			i.console.Message(ctx, "")

			if len(recommendedServices) == 0 {
				recommendedServices = append(recommendedServices, "Azure Container Apps")
			}
		}

		// handle detectedDbs
		for db, entry := range detectedDbs {
			switch db {
			case appdetect.DbPostgres:
				recommendedServices = append(recommendedServices, "Azure Database for PostgreSQL flexible server")
			case appdetect.DbMongo:
				recommendedServices = append(recommendedServices, "Azure CosmosDB API for MongoDB")
			}
			status := ""
			if entry == EntryKindModified {
				status = " " + output.WithSuccessFormat("[Updated]")
			} else if entry == EntryKindManual {
				status = " " + output.WithSuccessFormat("[Added]")
			}

			i.console.Message(ctx, "  "+output.WithBlueFormat(db.Display())+status)
			i.console.Message(ctx, "")
		}

		displayedServices := make([]string, 0, len(recommendedServices))
		for _, svc := range recommendedServices {
			displayedServices = append(displayedServices, color.MagentaString(svc))
		}

		if len(displayedServices) > 0 {
			i.console.Message(ctx,
				"azd will generate the files necessary to host your app on Azure using "+
					ux.ListAsText(displayedServices)+".\n")
		}

		continueOption, err := i.console.Select(ctx, input.ConsoleOptions{
			Message: "Select an option",
			Options: []string{
				"Confirm and continue initializing my app",
				"Add or remove a service",
			},
		})
		if err != nil {
			return err
		}

		switch continueOption {
		case 0:
			break confirmDetection
		case 1:
			modifyIdx, err := i.console.Select(ctx, input.ConsoleOptions{
				Message: "Add or remove a service",
				Options: []string{
					"Add a service",
					"Remove a service",
				},
			})
			if err != nil {
				return err
			}

			switch modifyIdx {
			case 0:
				languages := supportedLanguages()
				frameworks := supportedFrameworks()
				allDbs := supportedDatabases()
				databases := make([]appdetect.Framework, 0, len(allDbs))
				for _, db := range allDbs {
					if _, ok := detectedDbs[db]; !ok {
						databases = append(databases, db)
					}
				}
				selections := make([]string, 0, len(languages)+len(databases))
				entries := make([]any, 0, len(languages)+len(databases))

				for _, lang := range languages {
					selections = append(selections, fmt.Sprintf("%s\t%s", lang.Display(), "[Language]"))
					entries = append(entries, lang)
				}

				for _, framework := range frameworks {
					selections = append(selections, fmt.Sprintf("%s\t%s", framework.Display(), "[Framework]"))
					entries = append(entries, framework)
				}

				for _, db := range databases {
					selections = append(selections, fmt.Sprintf("%s\t%s", db.Display(), "[Database]"))
					entries = append(entries, db)
				}

				selections, err = tabWrite(selections, 3)
				if err != nil {
					return err
				}

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
					if framework.IsDatabaseDriver() {
						detectedDbs[framework] = EntryKindManual

						selection := make([]string, 0, len(projects))
						for _, prj := range projects {
							selection = append(selection,
								fmt.Sprintf("%s\t[%s]", projectDisplayName(prj), filepath.Base(prj.Path)))
						}

						selection, err = tabWrite(selection, 3)
						if err != nil {
							return err
						}

						idx, err := i.console.Select(ctx, input.ConsoleOptions{
							Message: "Select the service that uses this database",
							Options: selection,
						})
						if err != nil {
							return err
						}

						projects[idx].Frameworks = append(projects[idx].Frameworks, framework)
						continue confirmDetection
					} else if framework.Language() != "" {
						s.Frameworks = []appdetect.Framework{framework}
						s.Language = framework.Language()
					}
				default:
					log.Panic("unhandled entry type")
				}

				msg := fmt.Sprintf("Enter file path of the directory that uses '%s'", projectDisplayName(s))
				path, err := promptDir(ctx, i.console, msg)
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
							projects[idx].DetectionRule = string(EntryKindModified)
						} else {
							revision = false
						}

						continue confirmDetection
					}
				}

				s.Path = filepath.Clean(path)
				s.DetectionRule = string(EntryKindManual)
				projects = append(projects, s)
				continue confirmDetection
			case 1:
				modifyOptions := make([]string, 0, len(projects)+len(detectedDbs))
				for _, project := range projects {
					rel, err := filepath.Rel(wd, project.Path)
					if err != nil {
						return err
					}

					relWithDot := "./" + rel
					modifyOptions = append(
						modifyOptions, fmt.Sprintf("%s in %s", projectDisplayName(project), relWithDot))
				}

				displayDbs := maps.Keys(detectedDbs)
				for _, db := range displayDbs {
					modifyOptions = append(modifyOptions, db.Display())
				}

			modifyRemove:
				for {
					modifyIdx, err := i.console.Select(ctx, input.ConsoleOptions{
						Message: "Select the service you want to remove",
						Options: modifyOptions,
					})
					if err != nil {
						return err
					}

					if modifyIdx < len(projects) {
						prj := projects[modifyIdx]
						confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
							Message: fmt.Sprintf(
								"Remove %s in %s?", projectDisplayName(prj), prj.Path),
						})
						if err != nil {
							return err
						}

						if !confirm {
							continue modifyRemove
						}

						projects = append(projects[:modifyIdx], projects[modifyIdx+1:]...)
						break modifyRemove
					} else if modifyIdx < len(projects)+len(detectedDbs) {
						db := displayDbs[modifyIdx-len(projects)]

						confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
							Message: fmt.Sprintf(
								"Remove %s?", db.Display()),
						})
						if err != nil {
							return err
						}

						if confirm {
							delete(detectedDbs, db)
						}

						break modifyRemove
					}
				}

			}
		}
	}

	spec := InfraSpec{}
	for database := range detectedDbs {
	dbPrompt:
		for {
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Input the name of the database (%s)", database.Display()),
				Help: ux.InputHint{
					Title: "Database name",
					Text: "Input a name for the database. This database will be created after running azd provision " +
						"or azd up." + "\nYou may skip this step by hitting enter, " +
						"in which case the database will not be created.",
					Examples: []string{
						"appdb",
						"app-db",
						"app_db_1",
					},
				}.ToString(),
			})
			if err != nil {
				return err
			}

			if strings.ContainsAny(dbName, " ") {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains whitespace. This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return err
				}

				if !confirm {
					continue dbPrompt
				}
			} else if !wellFormedDbNameRegex.MatchString(dbName) {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains special characters. This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return err
				}

				if !confirm {
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
		serviceSpec := ServiceSpec{
			Name: name,
			Port: -1,
		}

		if project.Docker == nil || project.Docker.Path == "" {
			// default buildpack ports:
			// - python: 80
			// - other: 8080
			serviceSpec.Port = 8080
			// if project.Language == appdetect.Python {
			// 	serviceSpec.Port = 80
			// }
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
		spec.Services = append(spec.Services, serviceSpec)
	}

	for idx := range spec.Services {
		if spec.Services[idx].Port == -1 {
			var port int
			for {
				val, err := i.console.Prompt(ctx, input.ConsoleOptions{
					Message: "What port does '" + spec.Services[idx].Name + "' listen on?",
				})
				if err != nil {
					return err
				}

				port, err = strconv.Atoi(val)
				if err != nil {
					i.console.Message(ctx, "Port must be an integer. Try again or press Ctrl+C to cancel")
					continue
				}

				if port < 1 || port > 65535 {
					i.console.Message(ctx, "Port must be a value between 1 and 65535. Try again or press Ctrl+C to cancel")
					continue
				}

				break
			}
			spec.Services[idx].Port = port
		}

		if spec.Services[idx].Frontend == nil && spec.Services[idx].Port != 0 {
			backends = append(backends, spec.Services[idx])
			spec.Services[idx].Backend = &Backend{}
		} else {
			frontends = append(frontends, spec.Services[idx])
		}
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
			Name: bicepName(service.Name) + "Exists",
			Value: fmt.Sprintf("${SERVICE_%s_RESOURCE_EXISTS=false}",
				strings.ReplaceAll(strings.ToUpper(service.Name), "-", "_")),
			Type: "bool",
		})
	}

	err = initializeEnv()
	if err != nil {
		return err
	}

	i.console.Message(ctx, "\n"+output.WithBold("Generating files to run your app on Azure:")+"\n")

	generateProject := func() error {
		title := "Generating " + output.WithHighLightFormat("./"+azdcontext.ProjectFileName)
		i.console.ShowSpinner(ctx, title, input.Step)
		defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
		config, err := DetectionToConfig(wd, projects)
		if err != nil {
			return fmt.Errorf("converting config: %w", err)
		}
		err = project.Save(
			ctx,
			&config,
			filepath.Join(wd, azdcontext.ProjectFileName))
		if err != nil {
			return fmt.Errorf("generating azure.yaml: %w", err)
		}

		return i.writeCoreAssets(ctx, azdCtx)
	}

	err = generateProject()
	if err != nil {
		return err
	}

	target := filepath.Join(azdCtx.ProjectDirectory(), "infra")
	title = "Generating Infrastructure as Code files in " + output.WithHighLightFormat("./infra")
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
		"bicepName":        bicepName,
		"containerAppName": containerAppName,
		"upper":            strings.ToUpper,
		"lower":            strings.ToLower,
	}

	root := "scaffold/templates"
	t, err := template.New("templates").
		Option("missingkey=error").
		Funcs(funcMap).
		ParseFS(resources.ScaffoldTemplates,
			path.Join(root, "*"))
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

	err = execute(t, "init-summary.mdt", spec, filepath.Join(azdCtx.ProjectDirectory(), "next-steps.md"))
	if err != nil {
		return err
	}

	i.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: "Generating " + output.WithHighLightFormat("./next-steps.md"),
	})

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
	for i := range name {
		switch name[i] {
		case '-', '_':
			if separatorStart == -1 {
				separatorStart = i
			}
		default:
			if !isAsciiAlphaNumeric(name[i]) {
				continue
			}
			char := name[i]
			if separatorStart != -1 {
				if separatorStart == 0 {
					char = lowerCase(name[i])
				} else {
					char = upperCase(name[i])
				}
				separatorStart = -1
			}

			if i == 0 {
				char = lowerCase(name[i])
			}

			sb.WriteByte(char)
		}
	}

	return sb.String()
}

func isAsciiAlphaNumeric(c byte) bool {
	return ('0' <= c && c <= '9') || ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')
}

func upperCase(r byte) byte {
	if 'a' <= r && r <= 'z' {
		r -= 'a' - 'A'
	}
	return r
}

func lowerCase(r byte) byte {
	if 'A' <= r && r <= 'Z' {
		r += 'a' - 'A'
	}
	return r
}

// Provide a reasonable limit to avoid name length issues
const containerAppNameMaxLen = 12

// containerAppName returns a name that is valid to be used as an infix for a container app resource.
func containerAppName(name string) string {
	if len(name) > containerAppNameMaxLen {
		name = name[:containerAppNameMaxLen]
	}

	// trim to allowed characters:
	// - only alphanumeric and '-'
	// - no repeated '-'
	// - no '-' as the first or last character
	sb := strings.Builder{}
	i := 0
	for i < len(name) {
		if isAsciiAlphaNumeric(name[i]) {
			sb.WriteByte(lowerCase(name[i]))
		} else if name[i] == '-' || name[i] == '_' {
			j := i + 1
			for j < len(name) && (name[j] == '-' || name[i] == '_') { // find consecutive matches
				j++
			}

			if i != 0 && j != len(name) { // only write '-' if not first or last character
				sb.WriteByte('-')
			}

			i = j
			continue
		}

		i++
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
