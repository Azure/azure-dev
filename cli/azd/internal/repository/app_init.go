package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
	appdetect.DbMySql:    {},
	appdetect.DbRedis:    {},
}

var azureDepMap = map[string]struct{}{
	appdetect.AzureDepServiceBus{}.ResourceDisplay():     {},
	appdetect.AzureDepEventHubs{}.ResourceDisplay():      {},
	appdetect.AzureDepStorageAccount{}.ResourceDisplay(): {},
}

// InitFromApp initializes the infra directory and project file from the current existing app.
func (i *Initializer) InitFromApp(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	initializeEnv func() (*environment.Environment, error)) error {
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
		prj, err := appdetect.Detect(ctx, sourceDir)
		if err == nil && len(prj) > 0 {
			projects = prj
		}
	}

	if len(projects) == 0 {
		prj, err := appdetect.Detect(ctx, wd, appdetect.WithExcludePatterns([]string{
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

	appHostManifests := make(map[string]*apphost.Manifest)
	appHostForProject := make(map[string]string)

	// Load the manifests for all the App Host projects we detected, we use the manifest as part of infrastructure
	// generation.
	for _, prj := range projects {
		if prj.Language != appdetect.DotNetAppHost {
			continue
		}

		manifest, err := apphost.ManifestFromAppHost(ctx, prj.Path, i.dotnetCli, "")
		if err != nil {
			return fmt.Errorf("failed to generate manifest from app host project: %w", err)
		}
		appHostManifests[prj.Path] = manifest
		for _, path := range apphost.ProjectPaths(manifest) {
			appHostForProject[filepath.Dir(path)] = prj.Path
		}
	}

	// Filter out all the projects owned by an App Host.
	{
		var filteredProject []appdetect.Project
		for _, prj := range projects {
			if _, has := appHostForProject[prj.Path]; !has {
				filteredProject = append(filteredProject, prj)
			}
		}
		projects = filteredProject
	}

	end := time.Since(start)
	if i.console.IsSpinnerInteractive() {
		// If the spinner is interactive, we want to show it for at least 1 second
		time.Sleep((1 * time.Second) - end)
	}
	i.console.StopSpinner(ctx, title, input.StepDone)

	var prjAppHost []appdetect.Project
	for _, prj := range projects {
		if prj.Language == appdetect.DotNetAppHost {
			prjAppHost = append(prjAppHost, prj)
		}
	}

	if len(prjAppHost) > 1 {
		relPaths := make([]string, 0, len(prjAppHost))
		for _, appHost := range prjAppHost {
			rel, _ := filepath.Rel(wd, appHost.Path)
			relPaths = append(relPaths, rel)
		}
		return fmt.Errorf(
			"found multiple Aspire app host projects: %s. To fix, rerun `azd init` in each app host project directory",
			ux.ListAsText(relPaths))
	}

	if len(prjAppHost) == 1 {
		appHost := prjAppHost[0]

		otherProjects := make([]string, 0, len(projects))
		for _, prj := range projects {
			if prj.Language != appdetect.DotNetAppHost {
				rel, _ := filepath.Rel(wd, prj.Path)
				otherProjects = append(otherProjects, rel)
			}
		}

		if len(otherProjects) > 0 {
			i.console.Message(
				ctx,
				output.WithWarningFormat(
					"\nIgnoring other projects present but not referenced by app host: %s",
					ux.ListAsText(otherProjects)))
		}

		detect := detectConfirmAppHost{console: i.console}
		detect.Init(appHost, wd)

		if err := detect.Confirm(ctx); err != nil {
			return err
		}

		tracing.SetUsageAttributes(fields.AppInitLastStep.String("config"))

		// Prompt for environment before proceeding with generation
		newEnv, err := initializeEnv()
		if err != nil {
			return err
		}
		envManager, err := i.lazyEnvManager.GetValue()
		if err != nil {
			return err
		}
		if err := envManager.Save(ctx, newEnv); err != nil {
			return err
		}

		i.console.Message(ctx, "\n"+output.WithBold("Generating files to run your app on Azure:")+"\n")

		files, err := apphost.GenerateProjectArtifacts(
			ctx,
			azdCtx.ProjectDirectory(),
			azdcontext.ProjectName(azdCtx.ProjectDirectory()),
			appHostManifests[appHost.Path],
			appHost.Path,
		)
		if err != nil {
			return err
		}

		staging, err := os.MkdirTemp("", "azd-infra")
		if err != nil {
			return fmt.Errorf("mkdir temp: %w", err)
		}

		defer func() { _ = os.RemoveAll(staging) }()
		for path, file := range files {
			if err := os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectory); err != nil {
				return err
			}

			if err := os.WriteFile(filepath.Join(staging, path), []byte(file.Contents), file.Mode); err != nil {
				return err
			}
		}

		skipStagingFiles, err := i.promptForDuplicates(ctx, staging, azdCtx.ProjectDirectory())
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

		if err := copy.Copy(staging, azdCtx.ProjectDirectory(), options); err != nil {
			return fmt.Errorf("copying contents from temp staging directory: %w", err)
		}

		i.console.MessageUxItem(ctx, &ux.DoneMessage{
			Message: "Generating " + output.WithHighLightFormat("./azure.yaml"),
		})

		i.console.MessageUxItem(ctx, &ux.DoneMessage{
			Message: "Generating " + output.WithHighLightFormat("./next-steps.md"),
		})

		return i.writeCoreAssets(ctx, azdCtx)
	}

	detect := detectConfirm{console: i.console}
	detect.Init(projects, wd)
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
	_, err = initializeEnv()
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
		Name: azdcontext.ProjectName(root),
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
			// - Vite
			svc.OutputPath = "dist"

		loop:
			for _, dep := range prj.Dependencies {
				switch dep {
				case appdetect.JsNext:
					// next.js works as SSR with default node configuration without static build output
					svc.OutputPath = ""
					break loop
				case appdetect.JsVite:
					svc.OutputPath = "dist"
					break loop
				case appdetect.JsReact:
					// react from create-react-app uses 'build' when used, but this can be overridden
					// by choice of build tool, such as when using Vite.
					svc.OutputPath = "build"
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
		name = names.LabelName(name)
		config.Services[name] = &svc
	}

	return config, nil
}
