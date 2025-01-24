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
	"github.com/azure/azure-dev/cli/azd/internal/cmd/add"
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

var featureCompose = alpha.MustFeatureKey("compose")

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

	for _, proj := range projects {
		isSpringBootProject := false
		for _, dep := range proj.RawDependencies {
			if dep.Kind == appdetect.RawDependencyKindMaven && dep.Name == appdetect.MavenDependencyNameSpringBootMavenPlugin {
				isSpringBootProject = true
				break
			}
		}
		if !isSpringBootProject {
			continue
		}
		for _, dep := range proj.RawDependencies {
			if dep.Kind != appdetect.RawDependencyKindMaven {
				continue
			}
			switch dep.Name {
			case appdetect.MavenDependencyNamePostgresql, appdetect.MavenDependencyNameSpringCloudAzureStarterJdbcPostgresql:
				err := addPostgresqlConnectionProperties(proj.Path)
				if err != nil {
					return err
				}
			}
		}
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
		svc, err := add.ServiceFromDetect(root, "", prj)
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

		promptOpts := add.PromptOptions{PrjConfig: &config}

		for _, database := range databases {
			db := project.ResourceConfig{
				Type: add.DbMap[database],
			}

			configured, err := add.Configure(ctx, &db, i.console, promptOpts)
			if err != nil {
				return config, err
			}

			config.Resources[configured.Name] = &db
			dbNames[database] = configured.Name
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

			port, err := add.PromptPort(i.console, ctx, name, svc)
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

type property struct {
	key   string
	value string
}
type propertyMergeFunc func(string, string) string

const azureProfileName = "azure"

var placeholderPostgresJdbcUrl = "jdbc:postgresql://" + internal.ToEnvPlaceHolder(internal.EnvNamePostgresHost) +
	":" + internal.ToEnvPlaceHolder(internal.EnvNamePostgresPort) +
	"/" + internal.ToEnvPlaceHolder(internal.EnvNamePostgresDatabase)

var postgresqlProperties = []property{
	{"spring.datasource.url", placeholderPostgresJdbcUrl},
	{"spring.datasource.username", internal.ToEnvPlaceHolder(internal.EnvNamePostgresUsername)},
	{"spring.datasource.password", internal.ToEnvPlaceHolder(internal.EnvNamePostgresPassword)},
}

var applicationPropertiesRelativePath = filepath.Join("src", "main", "resources",
	"application.properties")
var applicationAzurePropertiesRelativePath = filepath.Join("src", "main", "resources",
	"application-"+azureProfileName+".properties")

// todo: support other file suffix. Example: application.yml, application.yaml
func addPostgresqlConnectionProperties(projectPath string) error {
	err := addPostgresqlConnectionPropertiesIntoPropertyFile(projectPath)
	if err != nil {
		return err
	}
	return activeAzureProfile(projectPath)
}

func addPostgresqlConnectionPropertiesIntoPropertyFile(projectPath string) error {
	filePath := filepath.Join(projectPath, applicationAzurePropertiesRelativePath)
	return updatePropertyFile(filePath, postgresqlProperties, keepNewValue)
}

func keepNewValue(_ string, newValue string) string {
	return newValue
}

func activeAzureProfile(projectPath string) error {
	filePath := filepath.Join(projectPath, applicationPropertiesRelativePath)
	var newProperties = []property{
		{"spring.profiles.active", azureProfileName},
	}
	return updatePropertyFile(filePath, newProperties, appendToCommaSeparatedValues)
}

func appendToCommaSeparatedValues(commaSeparatedValues string, newValue string) string {
	if commaSeparatedValues == "" {
		return newValue
	}
	var values []string
	for _, value := range strings.SplitN(commaSeparatedValues, ",", -1) {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	if !contains(values, azureProfileName) {
		values = append(values, azureProfileName)
	}
	return strings.Join(values, ",")
}

func contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func updatePropertyFile(filePath string, newProperties []property, function propertyMergeFunc) error {
	err := createFileIfNotExist(filePath)
	if err != nil {
		return err
	}
	properties, err := readProperties(filePath)
	if err != nil {
		return err
	}
	properties = updateProperties(properties, newProperties, function)
	err = writeProperties(filePath, properties)
	if err != nil {
		return err
	}
	return nil
}

func createFileIfNotExist(filePath string) error {
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		file, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer file.Close()
	}
	return nil
}

func readProperties(filePath string) ([]property, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var properties []property
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			properties = append(properties, property{key, value})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return properties, nil
}

func updateProperties(properties []property,
	newProperties []property, function propertyMergeFunc) []property {
	for _, newProperty := range newProperties {
		if index := getKeyIndex(properties, newProperty.key); index != -1 {
			properties[index].value = function(properties[index].value, newProperty.value)
		} else {
			properties = append(properties, newProperty)
		}
	}
	return properties
}

func getKeyIndex(properties []property, key string) int {
	for i, prop := range properties {
		if prop.key == key {
			return i
		}
	}
	return -1
}

func writeProperties(filePath string, properties []property) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, p := range properties {
		_, err := writer.WriteString(fmt.Sprintf("%s=%s\n", p.key, p.value))
		if err != nil {
			return err
		}
	}
	return writer.Flush()
}
