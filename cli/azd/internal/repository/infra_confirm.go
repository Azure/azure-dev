package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// A regex that matches against "likely" well-formed database names
var wellFormedDbNameRegex = regexp.MustCompile(`^[a-zA-Z\-_0-9]*$`)

// prjConfigFromDetect creates a project config from the results of app detection confirmation,
// prompting for additional inputs if necessary.
func (i *Initializer) prjConfigFromDetect(
	ctx context.Context,
	root string,
	detect detectConfirm) (project.ProjectConfig, error) {
	prj := project.ProjectConfig{
		Name: filepath.Base(root),
		Metadata: &project.ProjectMetadata{
			Template: fmt.Sprintf("%s@%s", InitGenTemplateId, internal.VersionInfo().Version),
		},
		Services:  map[string]*project.ServiceConfig{},
		Resources: map[string]*project.ResourceConfig{},
	}

	dbNames := map[appdetect.DatabaseDep]string{}
	for database := range detect.Databases {
		if database == appdetect.DbRedis {
			redis := project.ResourceConfig{
				Type: project.ResourceTypeDbRedis,
				Name: "redis",
			}
			prj.Resources[redis.Name] = &redis
			dbNames[database] = redis.Name
			continue
		}

	dbPrompt:
		for {
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Input the name of the app database (%s)", database.Display()),
				Help: "Hint: App database name\n\n" +
					"Name of the database that the app connects to. " +
					"This database will be created after running azd provision or azd up." +
					"\nYou may be able to skip this step by hitting enter, in which case the database will not be created.",
			})
			if err != nil {
				return prj, err
			}

			if dbName == "" {
				i.console.Message(ctx, "Database name is required.")
				continue
			}

			if strings.ContainsAny(dbName, " ") {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains whitespace. This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return prj, err
				}

				if !confirm {
					continue dbPrompt
				}
			} else if !wellFormedDbNameRegex.MatchString(dbName) {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains special characters. " +
						"This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return prj, err
				}

				if !confirm {
					continue dbPrompt
				}
			}

			var dbType project.ResourceType
			switch database {
			case appdetect.DbMongo:
				dbType = project.ResourceTypeDbMongo
			case appdetect.DbPostgres:
				dbType = project.ResourceTypeDbRedis
			}

			db := project.ResourceConfig{
				Type: dbType,
				Name: dbName,
			}
			prj.Resources[db.Name] = &db
			dbNames[database] = dbName
			break dbPrompt
		}
	}

	backends := []*project.ServiceConfig{}
	frontends := []*project.ServiceConfig{}
	for _, svc := range detect.Services {
		name := filepath.Base(svc.Path)
		rel, err := filepath.Rel(root, svc.Path)
		if err != nil {
			return project.ProjectConfig{}, err
		}

		svcSpec := project.ServiceConfig{
			Port: -1,
		}
		svcSpec.Host = project.ContainerAppTarget
		svcSpec.RelativePath = rel

		language, supported := languageMap[svc.Language]
		if !supported {
			continue
		}
		svcSpec.Language = language

		if svc.Docker == nil || svc.Docker.Path == "" {
			// default builder always specifies port 80
			svcSpec.Port = 80

			if svc.Language == appdetect.Java {
				svcSpec.Port = 8080
			}
		}

		if svcSpec.Port == -1 {
			var port int
			for {
				val, err := i.console.Prompt(ctx, input.ConsoleOptions{
					Message: "What port does '" + name + "' listen on?",
				})
				if err != nil {
					return prj, err
				}

				port, err = strconv.Atoi(val)
				if err != nil {
					i.console.Message(ctx, "Port must be an integer.")
					continue
				}

				if port < 1 || port > 65535 {
					i.console.Message(ctx, "Port must be a value between 1 and 65535.")
					continue
				}

				break
			}
			svcSpec.Port = port
		}

		if svc.Docker != nil {
			relDocker, err := filepath.Rel(svc.Path, svc.Docker.Path)
			if err != nil {
				return project.ProjectConfig{}, err
			}

			svcSpec.Docker = project.DockerProjectOptions{
				Path: relDocker,
			}
		}

		for _, db := range svc.DatabaseDeps {
			// filter out databases that were removed
			if _, ok := detect.Databases[db]; !ok {
				continue
			}

			svcSpec.Uses = append(svcSpec.Uses, dbNames[db])
		}

		if svc.HasWebUIFramework() {
			// By default, use 'dist'. This is common for frameworks such as:
			// - TypeScript
			// - Vite
			svcSpec.OutputPath = "dist"

		loop:
			for _, dep := range svc.Dependencies {
				switch dep {
				case appdetect.JsNext:
					// next.js works as SSR with default node configuration without static build output
					svcSpec.OutputPath = ""
					break loop
				case appdetect.JsVite:
					svcSpec.OutputPath = "dist"
					break loop
				case appdetect.JsReact:
					// react from create-react-app uses 'build' when used, but this can be overridden
					// by choice of build tool, such as when using Vite.
					svcSpec.OutputPath = "build"
				case appdetect.JsAngular:
					// angular uses dist/<project name>
					svcSpec.OutputPath = "dist/" + filepath.Base(rel)
					break loop
				}
			}

			frontends = append(frontends, &svcSpec)
		} else {
			backends = append(backends, &svcSpec)
		}

		if name == "." {
			name = prj.Name
		}
		svcSpec.Name = name
		prj.Services[name] = &svcSpec
	}

	for _, frontend := range frontends {
		for _, backend := range backends {
			frontend.Uses = append(frontend.Uses, backend.Name)
		}
	}

	return prj, nil
}
