package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/otiai10/copy"
)

var languageMap = map[appdetect.Language]project.ServiceLanguageKind{
	appdetect.DotNet:     project.ServiceLanguageDotNet,
	appdetect.Java:       project.ServiceLanguageJava,
	appdetect.JavaScript: project.ServiceLanguageJavaScript,
	appdetect.TypeScript: project.ServiceLanguageTypeScript,
	appdetect.Python:     project.ServiceLanguagePython,
}

var dbMap = map[appdetect.DatabaseDep]struct{}{
	appdetect.DbMongo:    {},
	appdetect.DbPostgres: {},
	appdetect.DbRedis:    {},
}

var ErrNoServicesDetected = errors.New("no services detected in the current directory")

// InitFromApp initializes the infra directory and project file from the current existing app.
func (i *Initializer) InitFromApp(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	initializeEnv func() error) error {
	i.console.Message(ctx, "")
	title := "Scanning app code in current directory"
	i.console.ShowSpinner(ctx, title, input.Step)
	wd := azdCtx.ProjectDirectory()

	projects := []appdetect.Project{}
	start := time.Now()
	sourceDir := filepath.Join(wd, "src")
	tracing.SetUsageAttributes(fields.AppInitLastStep.String("detect"))
	// Prioritize src directory if it exists
	if ent, err := os.Stat(sourceDir); err == nil && ent.IsDir() {
		prj, err := appdetect.Detect(sourceDir)
		if err == nil && len(prj) > 0 {
			projects = prj
		}
	}

	if len(projects) == 0 {
		prj, err := appdetect.Detect(wd, appdetect.WithExcludePatterns([]string{
			"**/eng",
			"**/tool",
			"**/tools"},
			false))
		if err != nil {
			i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
			return err
		}

		projects = prj
	}

	end := time.Since(start)
	if i.console.IsSpinnerInteractive() {
		// If the spinner is interactive, we want to show it for at least 1 second
		time.Sleep((1 * time.Second) - end)
	}
	i.console.StopSpinner(ctx, title, input.StepDone)

	detect := detectConfirm{console: i.console}
	detect.Init(projects, wd)
	if len(detect.Services) == 0 {
		return ErrNoServicesDetected
	}

	tracing.SetUsageAttributes(fields.AppInitLastStep.String("modify"))

	// Confirm selection of services and databases
	err := detect.Confirm(ctx)
	if err != nil {
		return err
	}

	tracing.SetUsageAttributes(fields.AppInitLastStep.String("config"))

	// Create the infra spec
	spec, err := i.infraSpecFromDetect(ctx, detect)
	if err != nil {
		return err
	}

	// Prompt for environment before proceeding with generation
	err = initializeEnv()
	if err != nil {
		return err
	}

	tracing.SetUsageAttributes(fields.AppInitLastStep.String("generate"))

	i.console.Message(ctx, "\n"+output.WithBold("Generating files to run your app on Azure:")+"\n")
	err = i.genProjectFile(ctx, azdCtx, detect)
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

	skipStagingFiles, err := i.promptForDuplicates(ctx, staging, infra)
	if err != nil {
		return err
	}

	options := copy.Options{}
	if skipStagingFiles != nil {
		options.Skip = func(fileInfo os.FileInfo, src, dest string) (bool, error) {
			_, skip := skipStagingFiles[src]
			return skip, nil
		}
	}

	if err := copy.Copy(staging, infra, options); err != nil {
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

func (i *Initializer) genProjectFile(
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

const InitGenTemplateId = "azd-init"

func prjConfigFromDetect(
	root string,
	detect detectConfirm) (project.ProjectConfig, error) {
	config := project.ProjectConfig{
		Name: filepath.Base(root),
		Metadata: &project.ProjectMetadata{
			Template: fmt.Sprintf("%s@%s", InitGenTemplateId, internal.VersionInfo().Version),
		},
		Services: map[string]*project.ServiceConfig{},
	}
	for _, prj := range detect.Services {
		rel, err := filepath.Rel(root, prj.Path)
		if err != nil {
			return project.ProjectConfig{}, err
		}

		svc := project.ServiceConfig{}
		svc.Host = project.ContainerAppTarget
		svc.RelativePath = rel

		language, supported := languageMap[prj.Language]
		if !supported {
			continue
		}
		svc.Language = language

		if prj.Docker != nil {
			relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
			if err != nil {
				return project.ProjectConfig{}, err
			}

			svc.Docker = project.DockerProjectOptions{
				Path: relDocker,
			}
		}

		if prj.HasWebUIFramework() {
			// By default, use 'dist'. This is common for frameworks such as:
			// - TypeScript
			// - Vue.js
			svc.OutputPath = "dist"

		loop:
			for _, dep := range prj.Dependencies {
				switch dep {
				case appdetect.JsReact:
					// react uses 'build'
					svc.OutputPath = "build"
					break loop
				case appdetect.JsAngular:
					// angular uses dist/<project name>
					svc.OutputPath = "dist/" + filepath.Base(rel)
					break loop
				}
			}
		}

		name := filepath.Base(rel)
		if name == "." {
			name = config.Name
		}
		config.Services[name] = &svc
	}

	return config, nil
}
