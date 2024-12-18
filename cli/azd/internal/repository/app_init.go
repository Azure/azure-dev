package repository

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
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

var LanguageMap = map[appdetect.Language]project.ServiceLanguageKind{
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

var featureCompose = alpha.MustFeatureKey("compose")

// parseDockerfileForArgs parses a Dockerfile to extract ARG instructions and returns them as ExpandableString values.
func parseDockerfileForArgs(dockerfilePath string) ([]osutil.ExpandableString, error) {
	var buildArgs []osutil.ExpandableString

	file, err := os.Open(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open Dockerfile: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "ARG ") {
			argLine := strings.TrimPrefix(line, "ARG ")
			if len(argLine) > 0 {
				buildArgs = append(buildArgs, osutil.NewExpandableString(argLine))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading Dockerfile: %w", err)
	}

	return buildArgs, nil
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
	var infraSpec *scaffold.InfraSpec
	composeEnabled := i.features.IsEnabled(featureCompose)
	if !composeEnabled { // backwards compatibility
		spec, err := i.infraSpecFromDetect(ctx, detect)
		if err != nil {
			return err
		}
		infraSpec = &spec

		// Prompt for environment before proceeding with generation
		_, err = initializeEnv()
		if err != nil {
			return err
		}
	}

	for idx := range detect.Services {
		servicePath := detect.Services[idx].Path
		dockerfilePath := filepath.Join(servicePath, "Dockerfile")

		if _, err := os.Stat(dockerfilePath); err == nil {

			buildArgs, err := parseDockerfileForArgs(dockerfilePath)
			if err != nil {
				return fmt.Errorf("failed to parse Dockerfile ARGs at %s: %w", dockerfilePath, err)
			}

			if len(buildArgs) > 0 {
				detect.Services[idx].Docker.BuildArgs = buildArgs
			}
		}
	}

	tracing.SetUsageAttributes(fields.AppInitLastStep.String("generate"))

	title = "Generating " + output.WithHighLightFormat("./"+azdcontext.ProjectFileName)
	i.console.ShowSpinner(ctx, title, input.Step)
	err = i.genProjectFile(ctx, azdCtx, detect, composeEnabled)
	if err != nil {
		i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
		return err
	}
	i.console.Message(ctx, "\n"+output.WithBold("Generating files to run your app on Azure:")+"\n")
	i.console.StopSpinner(ctx, title, input.StepDone)

	if infraSpec != nil {
		title = "Generating Infrastructure as Code files in " + output.WithHighLightFormat("./infra")
		i.console.ShowSpinner(ctx, title, input.Step)
		err = i.genFromInfra(ctx, azdCtx, *infraSpec)
		if err != nil {
			i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
			return err
		}
		i.console.StopSpinner(ctx, title, input.StepDone)
	} else {
		t, err := scaffold.Load()
		if err != nil {
			return fmt.Errorf("loading scaffold templates: %w", err)
		}

		err = scaffold.Execute(t, "next-steps-alpha.md", nil, filepath.Join(azdCtx.ProjectDirectory(), "next-steps.md"))
		if err != nil {
			return err
		}

		i.console.MessageUxItem(ctx, &ux.DoneMessage{
			Message: "Generating " + output.WithHighLightFormat("./next-steps.md"),
		})
	}

	return nil
}

func (i *Initializer) genFromInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	spec scaffold.InfraSpec) error {
	infra := filepath.Join(azdCtx.ProjectDirectory(), "infra")
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
	detect detectConfirm,
	addResources bool) error {
	config, err := i.prjConfigFromDetect(ctx, azdCtx.ProjectDirectory(), detect, addResources)
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

func (i *Initializer) prjConfigFromDetect(
	ctx context.Context,
	root string,
	detect detectConfirm,
	addResources bool) (project.ProjectConfig, error) {
	config := project.ProjectConfig{
		Name: azdcontext.ProjectName(root),
		Metadata: &project.ProjectMetadata{
			Template: fmt.Sprintf("%s@%s", InitGenTemplateId, internal.VersionInfo().Version),
		},
		Services: map[string]*project.ServiceConfig{},
	}

	svcMapping := map[string]string{}
	for _, prj := range detect.Services {
		svc, err := ServiceFromDetect(root, "", prj)
		if err != nil {
			return config, err
		}

		config.Services[svc.Name] = &svc
		svcMapping[prj.Path] = svc.Name
	}

	if addResources {
		config.Resources = map[string]*project.ResourceConfig{}
		dbNames := map[appdetect.DatabaseDep]string{}

		databases := slices.SortedFunc(maps.Keys(detect.Databases),
			func(a appdetect.DatabaseDep, b appdetect.DatabaseDep) int {
				return strings.Compare(string(a), string(b))
			})

		for _, database := range databases {
			if database == appdetect.DbRedis {
				redis := project.ResourceConfig{
					Type: project.ResourceTypeDbRedis,
					Name: "redis",
				}
				config.Resources[redis.Name] = &redis
				dbNames[database] = redis.Name
				continue
			}

			var dbType project.ResourceType
			switch database {
			case appdetect.DbMongo:
				dbType = project.ResourceTypeDbMongo
			case appdetect.DbPostgres:
				dbType = project.ResourceTypeDbPostgres
			}

			db := project.ResourceConfig{
				Type: dbType,
			}

			for {
				dbName, err := promptDbName(i.console, ctx, database)
				if err != nil {
					return config, err
				}

				if dbName == "" {
					i.console.Message(ctx, "Database name is required.")
					continue
				}

				db.Name = dbName
				break
			}

			config.Resources[db.Name] = &db
			dbNames[database] = db.Name
		}

		backends := []*project.ResourceConfig{}
		frontends := []*project.ResourceConfig{}

		for _, svc := range detect.Services {
			name := svcMapping[svc.Path]
			resSpec := project.ResourceConfig{
				Type: project.ResourceTypeHostContainerApp,
			}

			props := project.ContainerAppProps{
				Port: -1,
			}

			port, err := PromptPort(i.console, ctx, name, svc)
			if err != nil {
				return config, err
			}
			props.Port = port

			for _, db := range svc.DatabaseDeps {
				// filter out databases that were removed
				if _, ok := detect.Databases[db]; !ok {
					continue
				}

				resSpec.Uses = append(resSpec.Uses, dbNames[db])
			}

			resSpec.Name = name
			resSpec.Props = props
			config.Resources[name] = &resSpec

			frontend := svc.HasWebUIFramework()
			if frontend {
				frontends = append(frontends, &resSpec)
			} else {
				backends = append(backends, &resSpec)
			}
		}

		for _, frontend := range frontends {
			for _, backend := range backends {
				frontend.Uses = append(frontend.Uses, backend.Name)
			}
		}
	}

	return config, nil
}

// ServiceFromDetect creates a ServiceConfig from an appdetect project.
func ServiceFromDetect(
	root string,
	svcName string,
	prj appdetect.Project) (project.ServiceConfig, error) {
	svc := project.ServiceConfig{
		Name: svcName,
	}
	rel, err := filepath.Rel(root, prj.Path)
	if err != nil {
		return svc, err
	}

	if svc.Name == "" {
		dirName := filepath.Base(rel)
		if dirName == "." {
			dirName = filepath.Base(root)
		}

		svc.Name = names.LabelName(dirName)
	}

	svc.Host = project.ContainerAppTarget
	svc.RelativePath = rel

	language, supported := LanguageMap[prj.Language]
	if !supported {
		return svc, fmt.Errorf("unsupported language: %s", prj.Language)
	}

	svc.Language = language

	if prj.Docker != nil {
		relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
		if err != nil {
			return svc, err
		}

		svc.Docker = project.DockerProjectOptions{
			Path:      relDocker,
			BuildArgs: prj.Docker.BuildArgs,
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

	return svc, nil
}
