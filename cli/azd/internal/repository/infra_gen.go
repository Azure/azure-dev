package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/otiai10/copy"
)

// A regex that matches against "likely" well-formed database names
var wellFormedDbNameRegex = regexp.MustCompile(`^[a-zA-Z\-_0-9]*$`)

var NoServicesDetectedError = errors.New("no services detected")

func (i *Initializer) InitializeInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	initializeEnv func() error) error {
	i.console.Message(ctx, "")
	title := "Scanning app code in current directory"
	i.console.ShowSpinner(ctx, title, input.Step)
	wd := azdCtx.ProjectDirectory()

	// Prioritize src directory if it exists
	sourceDir := filepath.Join(wd, "src")
	projects := []appdetect.Project{}
	if ent, err := os.Stat(sourceDir); err == nil && ent.IsDir() {
		prj, err := appdetect.Detect(sourceDir)
		if err == nil && len(prj) > 0 {
			projects = prj
		}
	}

	if len(projects) == 0 {
		prj, err := appdetect.Detect(wd)
		if err != nil {
			i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
			return err
		}

		projects = prj
	}

	time.Sleep(1 * time.Second)
	i.console.StopSpinner(ctx, title, input.StepDone)

	detect := detectConfirm{console: i.console, root: wd}
	detect.init(projects)
	if len(detect.services) == 0 {
		return NoServicesDetectedError
	}

	err := detect.confirm(ctx)
	if err != nil {
		return err
	}

	spec, err := i.infraSpecFromDetect(ctx, detect)
	if err != nil {
		return err
	}

	err = initializeEnv()
	if err != nil {
		return err
	}

	i.console.Message(ctx, "\n"+output.WithBold("Generating files to run your app on Azure:")+"\n")
	err = i.initProject(ctx, azdCtx, detect)
	if err != nil {
		return err
	}

	infra := filepath.Join(azdCtx.ProjectDirectory(), "infra")
	title = "Generating Infrastructure as Code files in " + output.WithHighLightFormat("./infra")
	i.console.ShowSpinner(ctx, title, input.Step)
	defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

	staging, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}

	defer func() { _ = os.RemoveAll(staging) }()
	t, err := scaffold.Load()
	if err != nil {
		return fmt.Errorf("loading scaffold templates: %w", err)
	}

	err = scaffold.ExecInfra(t, spec, staging)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(infra, osutil.PermissionDirectory); err != nil {
		return err
	}

	if err := copy.Copy(staging, infra); err != nil {
		return fmt.Errorf("copying contents from temp staging directory: %w", err)
	}

	err = scaffold.Execute(t, "next-steps.md", spec, filepath.Join(azdCtx.ProjectDirectory(), "next-steps.md"))
	if err != nil {
		return err
	}

	i.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: "Generating " + output.WithHighLightFormat("./next-steps.md"),
	})

	return nil
}

func (i *Initializer) initProject(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	detect detectConfirm) error {
	title := "Generating " + output.WithHighLightFormat("./"+azdcontext.ProjectFileName)

	i.console.ShowSpinner(ctx, title, input.Step)
	var err error
	defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

	config, err := prjConfigFromDetect(azdCtx.ProjectDirectory(), detect)
	if err != nil {
		return fmt.Errorf("converting config: %w", err)
	}
	err = project.Save(
		ctx,
		&config,
		azdCtx.ProjectPath())
	if err != nil {
		return fmt.Errorf("generating %s: %w", azdcontext.ProjectFileName, err)
	}

	return i.writeCoreAssets(ctx, azdCtx)
}

func (i *Initializer) infraSpecFromDetect(ctx context.Context, detect detectConfirm) (scaffold.InfraSpec, error) {
	spec := scaffold.InfraSpec{}
	for database := range detect.databases {
	dbPrompt:
		for {
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Input the name of the database (%s)", database.Display()),
				Help: "Hint: Database name\n\n" +
					"Input a name for the database. This database will be created after running azd provision or azd up." +
					"\nYou may skip this step by hitting enter, in which case the database will not be created." +
					"\n\nExamples:\n" + strings.Join([]string{"appdb", "appdb-1"}, "\n"),
			})
			if err != nil {
				return scaffold.InfraSpec{}, err
			}

			if strings.ContainsAny(dbName, " ") {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains whitespace. This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return scaffold.InfraSpec{}, err
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
					return scaffold.InfraSpec{}, err
				}

				if !confirm {
					continue dbPrompt
				}
			}

			switch database {
			case appdetect.DbMongo:
				spec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
					DatabaseName: dbName,
				}

				break dbPrompt
			case appdetect.DbPostgres:
				spec.DbPostgres = &scaffold.DatabasePostgres{
					DatabaseName: dbName,
				}
			}
		}
	}

	backends := []scaffold.ServiceReference{}
	frontends := []scaffold.ServiceReference{}
	for _, project := range detect.services {
		name := filepath.Base(project.Path)
		serviceSpec := scaffold.ServiceSpec{
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

		for _, framework := range project.Dependencies {
			if framework.IsWebUIFramework() {
				serviceSpec.Frontend = &scaffold.Frontend{}
			}
		}

		for _, db := range project.DatabaseDeps {
			switch db {
			case appdetect.DbMongo:
				serviceSpec.DbCosmosMongo = &scaffold.DatabaseReference{
					DatabaseName: spec.DbCosmosMongo.DatabaseName,
				}
			case appdetect.DbPostgres:
				serviceSpec.DbPostgres = &scaffold.DatabaseReference{
					DatabaseName: spec.DbPostgres.DatabaseName,
				}
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
					return scaffold.InfraSpec{}, err
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
			backends = append(backends, scaffold.ServiceReference{
				Name: spec.Services[idx].Name,
			})

			spec.Services[idx].Backend = &scaffold.Backend{}
		} else {
			frontends = append(frontends, scaffold.ServiceReference{
				Name: spec.Services[idx].Name,
			})
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
	}

	return spec, nil
}
