package repository

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/otiai10/copy"
)

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
	Servers []ServiceSpec
}

type ServiceSpec struct {
	Name string
	Port int

	// Front-end properties.
	Frontend *Frontend

	// Connection to a database. Only one should be set.
	DbPostgres *DatabasePostgres
	DbCosmos   *DatabaseCosmos
}

func (i *Initializer) InitializeInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) error {
	selection, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Where is your app code located?",
		Options: []string{
			"In my current directory (local)",
			"In a GitHub repository (remote)",
		},
	})
	if err != nil {
		return err
	}

	switch selection {
	case 0:
		wd := azdCtx.ProjectDirectory()
		title := "Detecting languages and databases in " + output.WithHighLightFormat(wd)
		var err error
		i.console.ShowSpinner(ctx, title, input.Step)
		projects, err := appdetect.Detect(wd)
		i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

		if err != nil {
			return err
		}

		i.console.Message(ctx, "\nApp detection summary:\n")
		projectsByLanguage := make(map[string][]appdetect.Project, 0)
		for _, project := range projects {
			name := project.Language.Display()
			for _, framework := range project.Frameworks {
				if framework.IsWebUIFramework() {
					name = framework.Display()
				}
			}

			projectsByLanguage[name] = append(projectsByLanguage[name], project)
		}

		for key, projects := range projectsByLanguage {
			i.console.Message(ctx, "  "+output.WithBold(key))
			i.console.Message(ctx, "    "+"Detected in:")
			for _, project := range projects {
				i.console.Message(ctx, "    "+"- "+output.WithHighLightFormat(project.Path))
			}

			i.console.Message(ctx, "    "+"Recommended service: "+"Azure Container Apps")
			i.console.Message(ctx, "")
		}

		// handle databases
		databases := make(map[appdetect.Framework]struct{})
		for _, project := range projects {
			for _, framework := range project.Frameworks {
				if framework.IsDatabaseDriver() {
					if _, recorded := databases[framework]; recorded {
						continue
					}

					recommended := "CosmosDB API for MongoDB"
					switch framework {
					case appdetect.DbPostgres:
						recommended = "Azure Database for PostgreSQL flexible server"
					}
					i.console.Message(ctx, "  "+output.WithBold(framework.Display()))
					i.console.Message(ctx, "    "+"Recommended service: "+recommended)
					i.console.Message(ctx, "")

					databases[framework] = struct{}{}
				}
			}
		}

		spec := InfraSpec{}
		for database := range databases {
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: "What is the name of the database to be created? (Empty skips database creation)",
			})
			if err != nil {
				return err
			}

			switch database {
			case appdetect.DbMongo:
				spec.DbCosmos = &DatabaseCosmos{
					DatabaseName: dbName,
				}
			case appdetect.DbPostgres:
				spec.DbPostgres = &DatabasePostgres{
					DatabaseName: dbName,
				}

				spec.Parameters = append(spec.Parameters,
					Parameter{
						Name:   "sqlAdminPassword",
						Value:  "$(secretOrRandomPassword)",
						Secret: true,
					},
					Parameter{
						Name:   "appUserPassword",
						Value:  "$(secretOrRandomPassword)",
						Secret: true,
					})
			}
		}

		backends := []ServiceSpec{}
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
			}

			spec.Services = append(spec.Services, serviceSpec)
		}

		// Link front-ends to back-ends
		for _, service := range spec.Services {
			if service.Frontend != nil {
				service.Frontend.Servers = backends
			}
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

		switch confirm {
		case 0:
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

			err := generateProject()
			if err != nil {
				return err
			}

			target := filepath.Join(azdCtx.ProjectDirectory(), "infra")
			title := "Generating Infrastructure as Code files in " + output.WithHighLightFormat(target)
			i.console.ShowSpinner(ctx, title, input.Step)
			defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

			staging, err := os.MkdirTemp("", "azd-infra")
			if err != nil {
				return fmt.Errorf("mkdir temp: %w", err)
			}

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

			if spec.DbCosmos != nil {
				t, err := template.New("cosmos").
					Option("missingkey=error").
					Funcs(funcMap).
					Parse(string(resources.DbCosmosTempl))
				if err != nil {
					return fmt.Errorf("parsing template: %w", err)
				}

				buf := bytes.NewBufferString("")
				err = t.Execute(buf, spec.DbCosmos)
				if err != nil {
					return fmt.Errorf("executing template: %w", err)
				}

				err = os.WriteFile(filepath.Join(stagingApp, "db-cosmos.bicep"), buf.Bytes(), osutil.PermissionFile)
				if err != nil {
					return fmt.Errorf("writing service file: %w", err)
				}
			}

			if spec.DbPostgres != nil {
				t, err := template.New("postgres").
					Option("missingkey=error").
					Funcs(funcMap).
					Parse(string(resources.DbPostgresTempl))
				if err != nil {
					return fmt.Errorf("parsing template: %w", err)
				}

				buf := bytes.NewBufferString("")
				err = t.Execute(buf, spec.DbPostgres)
				if err != nil {
					return fmt.Errorf("executing template: %w", err)
				}

				err = os.WriteFile(filepath.Join(stagingApp, "db-postgres.bicep"), buf.Bytes(), osutil.PermissionFile)
				if err != nil {
					return fmt.Errorf("writing service file: %w", err)
				}
			}

			for _, svc := range spec.Services {
				t, err := template.New(svc.Name).
					//Option("missingkey=error").
					Funcs(funcMap).
					Parse(string(resources.ContainerAppBicepTempl))
				if err != nil {
					return fmt.Errorf("parsing template: %w", err)
				}

				buf := bytes.NewBufferString("")
				err = t.Execute(buf, svc)
				if err != nil {
					return fmt.Errorf("executing template: %w", err)
				}

				err = os.WriteFile(filepath.Join(stagingApp, svc.Name+".bicep"), buf.Bytes(), osutil.PermissionFile)
				if err != nil {
					return fmt.Errorf("writing service file: %w", err)
				}
			}

			t, err := template.New("main.bicep").
				Option("missingkey=error").
				Funcs(funcMap).
				Parse(string(resources.MainBicepTempl))
			if err != nil {
				return fmt.Errorf("parsing template: %w", err)
			}

			buf := bytes.NewBufferString("")
			err = t.Execute(buf, spec)
			if err != nil {
				return fmt.Errorf("executing template: %w", err)
			}

			err = os.WriteFile(filepath.Join(staging, "main.bicep"), buf.Bytes(), osutil.PermissionFile)
			if err != nil {
				return fmt.Errorf("writing main file: %w", err)
			}

			t, err = template.New("main.parameters.json").
				Option("missingkey=error").
				Funcs(funcMap).
				Parse(string(resources.MainParametersTempl))
			if err != nil {
				return fmt.Errorf("parsing template: %w", err)
			}

			buf = bytes.NewBufferString("")
			err = t.Execute(buf, spec)
			if err != nil {
				return fmt.Errorf("executing template: %w", err)
			}

			err = os.WriteFile(filepath.Join(staging, "main.parameters.json"), buf.Bytes(), osutil.PermissionFile)
			if err != nil {
				return fmt.Errorf("writing main file: %w", err)
			}

			if err := os.MkdirAll(target, osutil.PermissionDirectory); err != nil {
				return err
			}

			if err := copy.Copy(staging, target); err != nil {
				return fmt.Errorf("copying contents from temp staging directory: %w", err)
			}
		default:
			panic("unimplemented")
		}
	}

	return nil
}

func bicepName(name string) (string, error) {
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
				return "", err
			}
		}
	}

	return sb.String(), nil
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
