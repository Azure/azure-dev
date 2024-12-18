package repository

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"

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
	appdetect.DbMySql:    {},
	appdetect.DbCosmos:   {},
	appdetect.DbRedis:    {},
}

var featureCompose = alpha.MustFeatureKey("compose")

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
			"**/tools",
		},
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
	for index, prj := range projects {
		if prj.Language == appdetect.DotNetAppHost {
			prjAppHost = append(prjAppHost, prj)
		}

		if prj.Language == appdetect.Java {
			var hasKafkaDep bool
			for depIndex, dep := range prj.AzureDeps {
				if eventHubs, ok := dep.(appdetect.AzureDepEventHubs); ok {
					// prompt spring boot version if not detected for kafka
					if eventHubs.UseKafka {
						hasKafkaDep = true
						springBootVersion := eventHubs.SpringBootVersion
						if springBootVersion == appdetect.UnknownSpringBootVersion {
							springBootVersionInput, err := promptSpringBootVersion(i.console, ctx)
							if err != nil {
								return err
							}
							eventHubs.SpringBootVersion = springBootVersionInput
							prj.AzureDeps[depIndex] = eventHubs
						}
					}
					// prompt event hubs name if not detected
					for property, eventHubsName := range eventHubs.EventHubsNamePropertyMap {
						if eventHubsName == "" {
							promptMissingPropertyAndExit(i.console, ctx, property)
						}
					}
				}
				if storageAccount, ok := dep.(appdetect.AzureDepStorageAccount); ok {
					for property, containerName := range storageAccount.ContainerNamePropertyMap {
						if containerName == "" {
							promptMissingPropertyAndExit(i.console, ctx, property)
						}
					}
				}
			}

			if hasKafkaDep && !prj.Metadata.ContainsDependencySpringCloudAzureStarter {
				err := processSpringCloudAzureDepByPrompt(i.console, ctx, &projects[index])
				if err != nil {
					return err
				}
			}
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
		spec, err := i.infraSpecFromDetect(ctx, &detect)
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
	err = i.genProjectFile(ctx, azdCtx, &detect, infraSpec, composeEnabled)
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
	detect *detectConfirm,
	spec *scaffold.InfraSpec,
	addResources bool) error {
	config, err := i.prjConfigFromDetect(ctx, azdCtx.ProjectDirectory(), detect, spec, addResources)
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
	detect *detectConfirm,
	spec *scaffold.InfraSpec,
	addResources bool) (project.ProjectConfig, error) {
	config := project.ProjectConfig{
		Name: azdcontext.ProjectName(root),
		Metadata: &project.ProjectMetadata{
			Template: fmt.Sprintf("%s@%s", InitGenTemplateId, internal.VersionInfo().Version),
		},
		Services:  map[string]*project.ServiceConfig{},
		Resources: map[string]*project.ResourceConfig{},
	}

	var javaEurekaServerService project.ServiceConfig
	var javaConfigServerService project.ServiceConfig
	var err error
	for _, svc := range detect.Services {
		if svc.Metadata.ContainsDependencySpringCloudEurekaServer {
			javaEurekaServerService, err = ServiceFromDetect(root, svc.Metadata.ApplicationName, svc)
			if err != nil {
				return config, err
			}
		}
		if svc.Metadata.ContainsDependencySpringCloudConfigServer {
			javaConfigServerService, err = ServiceFromDetect(root, svc.Metadata.ApplicationName, svc)
			if err != nil {
				return config, err
			}
		}
	}

	svcMapping := map[string]string{}
	for _, prj := range detect.Services {
		svc, err := ServiceFromDetect(root, prj.Metadata.ApplicationName, prj)
		if err != nil {
			return config, err
		}

		if !addResources {
			for _, db := range prj.DatabaseDeps {
				switch db {
				case appdetect.DbMongo:
					config.Resources["mongo"] = &project.ResourceConfig{
						Type: project.ResourceTypeDbMongo,
						Name: spec.DbCosmosMongo.DatabaseName,
						Props: project.MongoDBProps{
							DatabaseName: spec.DbCosmosMongo.DatabaseName,
						},
					}
				case appdetect.DbPostgres:
					config.Resources["postgres"] = &project.ResourceConfig{
						Type: project.ResourceTypeDbPostgres,
						Name: spec.DbPostgres.DatabaseName,
						Props: project.PostgresProps{
							DatabaseName: spec.DbPostgres.DatabaseName,
							AuthType:     spec.DbPostgres.AuthType,
						},
					}
				case appdetect.DbMySql:
					config.Resources["mysql"] = &project.ResourceConfig{
						Type: project.ResourceTypeDbMySQL,
						Props: project.MySQLProps{
							DatabaseName: spec.DbMySql.DatabaseName,
							AuthType:     spec.DbMySql.AuthType,
						},
					}
				case appdetect.DbRedis:
					config.Resources["redis"] = &project.ResourceConfig{
						Type: project.ResourceTypeDbRedis,
					}
				case appdetect.DbCosmos:
					cosmosDBProps := project.CosmosDBProps{
						DatabaseName: spec.DbCosmos.DatabaseName,
					}
					for _, container := range spec.DbCosmos.Containers {
						cosmosDBProps.Containers = append(cosmosDBProps.Containers, project.CosmosDBContainerProps{
							ContainerName:     container.ContainerName,
							PartitionKeyPaths: container.PartitionKeyPaths,
						})
					}
					config.Resources["cosmos"] = &project.ResourceConfig{
						Type:  project.ResourceTypeDbCosmos,
						Props: cosmosDBProps,
					}
				}

			}
			for _, azureDep := range prj.AzureDeps {
				switch azureDep.(type) {
				case appdetect.AzureDepServiceBus:
					config.Resources["servicebus"] = &project.ResourceConfig{
						Type: project.ResourceTypeMessagingServiceBus,
						Props: project.ServiceBusProps{
							Queues:   spec.AzureServiceBus.Queues,
							IsJms:    spec.AzureServiceBus.IsJms,
							AuthType: spec.AzureServiceBus.AuthType,
						},
					}
				case appdetect.AzureDepEventHubs:
					if spec.AzureEventHubs.UseKafka {
						config.Resources["kafka"] = &project.ResourceConfig{
							Type: project.ResourceTypeMessagingKafka,
							Props: project.KafkaProps{
								Topics:            spec.AzureEventHubs.EventHubNames,
								AuthType:          spec.AzureEventHubs.AuthType,
								SpringBootVersion: spec.AzureEventHubs.SpringBootVersion,
							},
						}
					} else {
						config.Resources["eventhubs"] = &project.ResourceConfig{
							Type: project.ResourceTypeMessagingEventHubs,
							Props: project.EventHubsProps{
								EventHubNames: spec.AzureEventHubs.EventHubNames,
								AuthType:      spec.AzureEventHubs.AuthType,
							},
						}
					}
				case appdetect.AzureDepStorageAccount:
					config.Resources["storage"] = &project.ResourceConfig{
						Type: project.ResourceTypeStorage,
						Props: project.StorageProps{
							Containers: spec.AzureStorageAccount.ContainerNames,
							AuthType:   spec.AzureStorageAccount.AuthType,
						},
					}

				}
			}
		}

		if prj.Metadata.ContainsDependencySpringCloudEurekaClient {
			err := appendJavaEurekaServerEnv(&svc, javaEurekaServerService.Name)
			if err != nil {
				return config, err
			}
		}
		if prj.Metadata.ContainsDependencySpringCloudConfigClient {
			err := appendJavaConfigServerEnv(&svc, javaConfigServerService.Name)
			if err != nil {
				return config, err
			}
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
			var resourceConfig project.ResourceConfig
			var databaseName string
			if database == appdetect.DbRedis {
				databaseName = "redis"
			} else {
				var err error
				databaseName, err = getDatabaseName(database, detect, i.console, ctx)
				if err != nil {
					return config, err
				}
			}
			var authType = internal.AuthTypeUnspecified
			if database == appdetect.DbPostgres || database == appdetect.DbMySql {
				var err error
				authType, err = chooseAuthTypeByPrompt(
					database.Display(),
					[]internal.AuthType{internal.AuthTypeUserAssignedManagedIdentity, internal.AuthTypePassword},
					ctx,
					i.console)
				if err != nil {
					return config, err
				}
				continueProvision, err := checkPasswordlessConfigurationAndContinueProvision(database, authType, detect,
					i.console, ctx)
				if err != nil {
					return config, err
				}
				if !continueProvision {
					continue
				}
			}
			switch database {
			case appdetect.DbRedis:
				resourceConfig = project.ResourceConfig{
					Type: project.ResourceTypeDbRedis,
					Name: "redis",
				}
			case appdetect.DbMongo:
				resourceConfig = project.ResourceConfig{
					Type: project.ResourceTypeDbMongo,
					Name: "mongo",
					Props: project.MongoDBProps{
						DatabaseName: databaseName,
					},
				}
			case appdetect.DbCosmos:
				cosmosDBProps := project.CosmosDBProps{
					DatabaseName: databaseName,
				}
				containers, err := detectCosmosSqlDatabaseContainersInDirectory(detect.root)
				if err != nil {
					return config, err
				}
				for _, container := range containers {
					cosmosDBProps.Containers = append(cosmosDBProps.Containers, project.CosmosDBContainerProps{
						ContainerName:     container.ContainerName,
						PartitionKeyPaths: container.PartitionKeyPaths,
					})
				}
				resourceConfig = project.ResourceConfig{
					Type:  project.ResourceTypeDbCosmos,
					Name:  "cosmos",
					Props: cosmosDBProps,
				}
			case appdetect.DbPostgres:
				resourceConfig = project.ResourceConfig{
					Type: project.ResourceTypeDbPostgres,
					Name: "postgresql",
					Props: project.PostgresProps{
						DatabaseName: databaseName,
						AuthType:     authType,
					},
				}
			case appdetect.DbMySql:
				resourceConfig = project.ResourceConfig{
					Type: project.ResourceTypeDbMySQL,
					Name: "mysql",
					Props: project.MySQLProps{
						DatabaseName: databaseName,
						AuthType:     authType,
					},
				}
			}
			config.Resources[resourceConfig.Name] = &resourceConfig
			dbNames[database] = resourceConfig.Name
		}

		for _, azureDepPair := range detect.AzureDeps {
			azureDep := azureDepPair.first
			authType, err := chooseAuthTypeByPrompt(
				azureDep.ResourceDisplay(),
				[]internal.AuthType{internal.AuthTypeUserAssignedManagedIdentity, internal.AuthTypeConnectionString},
				ctx,
				i.console)
			if err != nil {
				return config, err
			}
			switch azureDep := azureDep.(type) {
			case appdetect.AzureDepServiceBus:
				config.Resources["servicebus"] = &project.ResourceConfig{
					Type: project.ResourceTypeMessagingServiceBus,
					Props: project.ServiceBusProps{
						Queues:   azureDep.Queues,
						IsJms:    azureDep.IsJms,
						AuthType: authType,
					},
				}
			case appdetect.AzureDepEventHubs:
				if azureDep.UseKafka {
					config.Resources["kafka"] = &project.ResourceConfig{
						Type: project.ResourceTypeMessagingKafka,
						Props: project.KafkaProps{
							Topics:            appdetect.DistinctValues(azureDep.EventHubsNamePropertyMap),
							AuthType:          authType,
							SpringBootVersion: azureDep.SpringBootVersion,
						},
					}
				} else {
					config.Resources["eventhubs"] = &project.ResourceConfig{
						Type: project.ResourceTypeMessagingEventHubs,
						Props: project.EventHubsProps{
							EventHubNames: appdetect.DistinctValues(azureDep.EventHubsNamePropertyMap),
							AuthType:      authType,
						},
					}
				}
			case appdetect.AzureDepStorageAccount:
				config.Resources["storage"] = &project.ResourceConfig{
					Type: project.ResourceTypeStorage,
					Props: project.StorageProps{
						Containers: appdetect.DistinctValues(azureDep.ContainerNamePropertyMap),
						AuthType:   authType,
					},
				}
			}
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

			if svc.Metadata.ContainsDependencySpringCloudEurekaClient {
				resSpec.Uses = append(resSpec.Uses, javaEurekaServerService.Name)
			}
			if svc.Metadata.ContainsDependencySpringCloudConfigClient {
				resSpec.Uses = append(resSpec.Uses, javaConfigServerService.Name)
			}

			for _, db := range svc.DatabaseDeps {
				// filter out databases that were removed
				if _, ok := detect.Databases[db]; !ok {
					continue
				}

				resSpec.Uses = append(resSpec.Uses, dbNames[db])
			}

			for _, azureDep := range svc.AzureDeps {
				switch azureDep := azureDep.(type) {
				case appdetect.AzureDepServiceBus:
					resSpec.Uses = append(resSpec.Uses, "servicebus")
				case appdetect.AzureDepEventHubs:
					if azureDep.UseKafka {
						resSpec.Uses = append(resSpec.Uses, "kafka")
					} else {
						resSpec.Uses = append(resSpec.Uses, "eventhubs")
					}
				case appdetect.AzureDepStorageAccount:
					resSpec.Uses = append(resSpec.Uses, "storage")
				}
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

		err := i.addMavenBuildHook(*detect, &config)
		if err != nil {
			return config, err
		}
	}

	return config, nil
}

func checkPasswordlessConfigurationAndContinueProvision(database appdetect.DatabaseDep, authType internal.AuthType,
	detect *detectConfirm, console input.Console, ctx context.Context) (bool, error) {
	if authType != internal.AuthTypeUserAssignedManagedIdentity {
		return true, nil
	}
	for i, prj := range detect.Services {
		if lackedDep := lackedAzureStarterJdbcDependency(prj, database); lackedDep != "" {
			message := fmt.Sprintf("\nError!\n"+
				"You selected '%s' as auth type for '%s'.\n"+
				"For this auth type, this dependency is required:\n"+
				"%s\n"+
				"But this dependency is not found in your project:\n"+
				"%s",
				internal.AuthTypeUserAssignedManagedIdentity, database, lackedDep, prj.Path)
			continueOption, err := console.Select(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("%s\nSelect an option:", message),
				Options: []string{
					"Exit azd and fix problem manually",
					fmt.Sprintf("Continue azd and use %s in this project: %s", database.Display(), prj.Path),
					fmt.Sprintf("Continue azd and not use %s in this project: %s", database.Display(), prj.Path),
				},
			})
			if err != nil {
				return false, err
			}

			switch continueOption {
			case 0:
				os.Exit(0)
			case 1:
				continue
			case 2:
				// remove related database usage
				var result []appdetect.DatabaseDep
				for _, db := range prj.DatabaseDeps {
					if db != database {
						result = append(result, db)
					}
				}
				prj.DatabaseDeps = result
				detect.Services[i] = prj
				// delete database if no other service used
				dbUsed := false
				for _, svc := range detect.Services {
					for _, db := range svc.DatabaseDeps {
						if db == database {
							dbUsed = true
							break
						}
					}
					if dbUsed {
						break
					}
				}
				if !dbUsed {
					console.Message(ctx, fmt.Sprintf(
						"Deleting database %s due to no service used", database.Display()))
					delete(detect.Databases, database)
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func lackedAzureStarterJdbcDependency(project appdetect.Project, database appdetect.DatabaseDep) string {
	if project.Language != appdetect.Java {
		return ""
	}

	useDatabase := false
	for _, db := range project.DatabaseDeps {
		if db == database {
			useDatabase = true
			break
		}
	}
	if !useDatabase {
		return ""
	}
	if database == appdetect.DbMySql && !project.Metadata.ContainsDependencySpringCloudAzureStarterJdbcMysql {
		return "<dependency>\n" +
			"  <groupId>com.azure.spring</groupId>\n" +
			"  <artifactId>spring-cloud-azure-starter-jdbc-mysql</artifactId>\n" +
			"  <version>xxx</version>\n" +
			"</dependency>"
	}
	if database == appdetect.DbPostgres && !project.Metadata.ContainsDependencySpringCloudAzureStarterJdbcPostgresql {
		return "<dependency>\n" +
			"  <groupId>com.azure.spring</groupId>\n" +
			"  <artifactId>spring-cloud-azure-starter-jdbc-postgresql</artifactId>\n" +
			"  <version>xxx</version>\n" +
			"</dependency>"
	}
	return ""
}

func (i *Initializer) addMavenBuildHook(
	detect detectConfirm,
	config *project.ProjectConfig) error {
	wrapperPathMap := map[string][]string{}

	for _, prj := range detect.Services {
		if prj.Language == appdetect.Java && prj.Options["parentPath"] != nil {
			parentPath := prj.Options[appdetect.JavaProjectOptionMavenParentPath].(string)
			posixMavenWrapperPath := prj.Options[appdetect.JavaProjectOptionPosixMavenWrapperPath].(string)
			winMavenWrapperPath := prj.Options[appdetect.JavaProjectOptionWinMavenWrapperPath].(string)
			wrapperPathMap[parentPath] = []string{posixMavenWrapperPath, winMavenWrapperPath}
		}
	}

	for _, wrapperPaths := range wrapperPathMap {
		// Add hooks to build the Java project
		if config.Hooks == nil {
			config.Hooks = project.HooksConfig{}
		}

		config.Hooks["prepackage"] = append(config.Hooks["prepackage"], &ext.HookConfig{
			Posix: &ext.HookConfig{
				Shell: ext.ShellTypeBash,
				Run:   getMavenExecutable(detect.root, wrapperPaths[0], true) + " clean package -DskipTests",
			},
			Windows: &ext.HookConfig{
				Shell: ext.ShellTypePowershell,
				Run:   getMavenExecutable(detect.root, wrapperPaths[1], false) + " clean package -DskipTests",
			},
		})
	}
	return nil
}

func getMavenExecutable(projectPath string, wrapperPath string, isPosix bool) string {
	if wrapperPath == "" {
		return "mvn"
	}

	rel, err := filepath.Rel(projectPath, wrapperPath)
	if err != nil {
		return "mvn"
	}

	if isPosix {
		return "./" + rel
	} else {
		return ".\\" + rel
	}
}

func chooseAuthTypeByPrompt(
	name string,
	authOptions []internal.AuthType,
	ctx context.Context,
	console input.Console) (internal.AuthType, error) {
	var options []string
	for _, option := range authOptions {
		options = append(options, internal.GetAuthTypeDescription(option))
	}
	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Choose auth type for " + name + ":",
		Options: options,
	})
	if err != nil {
		return internal.AuthTypeUnspecified, err
	}
	return authOptions[selection], nil
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
			case appdetect.SpringFrontend:
				svc.OutputPath = ""
				break loop
			}
		}
	}

	return svc, nil
}

func processSpringCloudAzureDepByPrompt(console input.Console, ctx context.Context, project *appdetect.Project) error {
	continueOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Detected Kafka dependency but no spring-cloud-azure-starter found. Select an option",
		Options: []string{
			"Exit then I will manually add this dependency",
			"Continue without this dependency, and provision Azure Event Hubs for Kafka",
			"Continue without this dependency, and not provision Azure Event Hubs for Kafka",
		},
	})
	if err != nil {
		return err
	}

	switch continueOption {
	case 0:
		console.Message(ctx, "you have to manually add dependency com.azure.spring:spring-cloud-azure-starter. "+
			"And use right version according to this page: "+
			"https://github.com/Azure/azure-sdk-for-java/wiki/Spring-Versions-Mapping")
		os.Exit(0)
	case 1:
		return nil
	case 2:
		// remove Kafka Azure Dep
		var result []appdetect.AzureDep
		for _, dep := range project.AzureDeps {
			if eventHubs, ok := dep.(appdetect.AzureDepEventHubs); !(ok && eventHubs.UseKafka) {
				result = append(result, dep)
			}
		}
		project.AzureDeps = result
		return nil
	}
	return nil
}

func promptSpringBootVersion(console input.Console, ctx context.Context) (string, error) {
	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "No spring boot version detected, what is your spring boot version?",
		Options: []string{
			"Spring Boot 2.x",
			"Spring Boot 3.x",
		},
	})
	if err != nil {
		return "", err
	}

	switch selection {
	case 0:
		return "2.x", nil
	case 1:
		return "3.x", nil
	default:
		return appdetect.UnknownSpringBootVersion, nil
	}
}

func promptMissingPropertyAndExit(console input.Console, ctx context.Context, key string) {
	console.Message(ctx, fmt.Sprintf("No value was provided for %s. Please update the configuration file "+
		"(like application.properties or application.yaml) with a valid value.", key))
	os.Exit(0)
}

func appendJavaEurekaServerEnv(svc *project.ServiceConfig, eurekaServerName string) error {
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	clientEnvs := scaffold.GetServiceBindingEnvsForEurekaServer(eurekaServerName)
	for _, env := range clientEnvs {
		svc.Env[env.Name] = env.Value
	}
	return nil
}

func appendJavaConfigServerEnv(svc *project.ServiceConfig, configServerName string) error {
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	clientEnvs := scaffold.GetServiceBindingEnvsForConfigServer(configServerName)
	for _, env := range clientEnvs {
		svc.Env[env.Name] = env.Value
	}
	return nil
}
