// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/otiai10/copy"
	"github.com/psanford/memfs"
)

type ImportManager struct {
	dotNetImporter *DotNetImporter
	env            *environment.Environment
	bicep          bicep.BicepCli
}

func NewImportManager(dotNetImporter *DotNetImporter, env *environment.Environment, bicep bicep.BicepCli) *ImportManager {
	return &ImportManager{
		dotNetImporter: dotNetImporter,
		env:            env,
		bicep:          bicep,
	}
}

func (im *ImportManager) HasService(ctx context.Context, projectConfig *ProjectConfig, name string) (bool, error) {
	services, err := im.ServiceStable(ctx, projectConfig)
	if err != nil {
		return false, err
	}

	for _, svc := range services {
		if svc.Name == name {
			return true, nil
		}
	}

	return false, nil
}

var (
	errNoMultipleServicesWithAppHost = fmt.Errorf(
		"a project may only contain a single Aspire service and no other services at this time.")

	errAppHostMustTargetContainerApp = fmt.Errorf(
		"Aspire services must be configured to target the container app host at this time.")
)

// Retrieves the list of services in the project, in a stable ordering that is deterministic.
func (im *ImportManager) ServiceStable(ctx context.Context, projectConfig *ProjectConfig) ([]*ServiceConfig, error) {
	allServices := make(map[string]*ServiceConfig)

	for name, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				if len(projectConfig.Services) != 1 {
					return nil, errNoMultipleServicesWithAppHost
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, errAppHostMustTargetContainerApp
				}

				services, err := im.dotNetImporter.Services(ctx, projectConfig, svcConfig)
				if err != nil {
					return nil, fmt.Errorf("importing services: %w", err)
				}

				for name, svcConfig := range services {
					// TODO(ellismg): We should consider if we should prefix these services so the are of the form
					// "app:frontend" instead of just "frontend". Perhaps both as the key here and and as the .Name
					// property on the ServiceConfig.  This does have implications for things like service specific
					// property names that translate to environment variables.
					allServices[name] = svcConfig
				}

				continue
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}
		}

		allServices[name] = svcConfig
	}

	// Collect all the services and then sort the resulting list by name. This provides a stable ordering of services.
	allServicesSlice := make([]*ServiceConfig, 0, len(allServices))
	for _, v := range allServices {
		allServicesSlice = append(allServicesSlice, v)
	}

	slices.SortFunc(allServicesSlice, func(x, y *ServiceConfig) int {
		return strings.Compare(x.Name, y.Name)
	})

	return allServicesSlice, nil
}

// defaultOptions for infra settings. These values are applied across provisioning providers.
const (
	DefaultModule = "main"
	DefaultPath   = "infra"
)

// ProjectInfrastructure parses the project configuration and returns the infrastructure configuration.
// The configuration can be explicitly defined on azure.yaml using path and module, or in case these values
// are not explicitly defined, the project importer uses default values to find the infrastructure.
func (im *ImportManager) ProjectInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (*Infra, error) {
	// Use default project values for Infra when not specified in azure.yaml
	if projectConfig.Infra.Module == "" {
		projectConfig.Infra.Module = DefaultModule
	}
	if projectConfig.Infra.Path == "" {
		projectConfig.Infra.Path = DefaultPath
	}

	infraRoot := projectConfig.Infra.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(projectConfig.Path, infraRoot)
	}

	// Allow overriding the infrastructure only when path and module exists.
	if moduleExists, err := pathHasModule(infraRoot, projectConfig.Infra.Module); err == nil && moduleExists {
		log.Printf("using infrastructure from %s directory", infraRoot)
		return &Infra{
			Options: projectConfig.Infra,
		}, nil
	}

	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				if len(projectConfig.Services) != 1 {
					return nil, errNoMultipleServicesWithAppHost
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, errAppHostMustTargetContainerApp
				}

				return im.dotNetImporter.ProjectInfrastructure(ctx, svcConfig)
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}
		}
	}

	infraSpec, err := infraSpec(projectConfig, im.env)
	if err != nil {
		return nil, fmt.Errorf("parsing infrastructure: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	files, err := scaffold.ExecInfraFs(t, *infraSpec)
	if err != nil {
		return nil, fmt.Errorf("executing scaffold templates: %w", err)
	}

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		target := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, contents, d.Type().Perm())
	})
	if err != nil {
		return nil, fmt.Errorf("writing infrastructure: %w", err)
	}

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultModule,
		},
		cleanupDir: tmpDir,
	}, nil
}

// pathHasModule returns true if there is a file named "<module>" or "<module.bicep>" in path.
func pathHasModule(path, module string) (bool, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("error while iterating directory: %w", err)
	}

	return slices.ContainsFunc(files, func(file fs.DirEntry) bool {
		fileName := file.Name()
		fileNameNoExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		return !file.IsDir() && fileNameNoExt == module
	}), nil

}

func (im *ImportManager) SynthResource(
	ctx context.Context,
	projectConfig *ProjectConfig,
	res ResourceConfig,
	console input.Console) (ResourceConfig, error) {
	// example
	// "https://github.com/Azure/bicep-registry-modules/blob/avm/res/app/container-app/0.4.1/avm/res/cache/redis/main.bicep"
	// bicepFileUrl := "https://raw.githubusercontent.com/Azure/bicep-registry-modules"
	bicepModule := ""
	bicepVersion := ""
	switch res.Type {
	case ResourceTypeDbMongo:
		bicepModule = "avm/res/document-db/database-account"
		bicepVersion = "0.4.0"
	case ResourceTypeDbPostgres:
		bicepModule = "avm/res/db-for-postgre-sql/flexible-server"
		bicepVersion = "0.1.6"
	case ResourceTypeDbRedis:
		bicepModule = "avm/res/cache/redis"
		bicepVersion = "0.3.2"
	default:
		return ResourceConfig{}, fmt.Errorf("unsupported resource type %s", res.Type)
	}

	infraPathPrefix := DefaultPath
	if projectConfig.Infra.Path != "" {
		infraPathPrefix = projectConfig.Infra.Path
	}

	synthFS, err := im.SynthAllInfrastructure(ctx, projectConfig)
	if err != nil {
		return ResourceConfig{}, err
	}

	staging, err := os.MkdirTemp("", "infra-synth")
	if err != nil {
		return ResourceConfig{}, err
	}
	defer os.RemoveAll(staging)

	err = fs.WalkDir(synthFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		contents, err := fs.ReadFile(synthFS, path)
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		return os.WriteFile(filepath.Join(staging, path), contents, d.Type().Perm())
	})
	if err != nil {
		return ResourceConfig{}, err
	}

	err = im.bicep.Restore(ctx, filepath.Join(staging, infraPathPrefix, "main.bicep"))
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("restoring bicep: %w", err)
	}

	restorePath, err := bicep.ModuleRestoredPath("mcr.microsoft.com", bicepModule, bicepVersion)
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("getting module restored path: %w", err)
	}

	stagingSource := filepath.Join(staging, "source")
	err = os.MkdirAll(stagingSource, osutil.PermissionDirectoryOwnerOnly)
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("creating directory: %w", err)
	}

	// extract files from source.tgz
	moduleZipped := filepath.Join(restorePath, "source.tgz")
	err = rzip.UnzipTarGz(moduleZipped, stagingSource)
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("unzipping source.tgz: %w", err)
	}

	stagingSourceFiles := filepath.Join(stagingSource, "files")

	// copy files from source.tgz to staging
	infraPath := filepath.Join(infraPathPrefix, "db", res.Name)

	skipStagingFiles, err := im.promptForDuplicates(ctx, console, stagingSourceFiles, infraPath)
	if err != nil {
		return ResourceConfig{}, err
	}

	options := copy.Options{}
	options.Skip = func(fileInfo os.FileInfo, src, dest string) (bool, error) {
		var skip bool
		if skipStagingFiles != nil {
			_, skip = skipStagingFiles[src]
		}

		rel, err := filepath.Rel(stagingSourceFiles, src)
		if err != nil {
			return false, fmt.Errorf("computing relative path: %w", err)
		}

		// bicep restore has _cache_ directory for bicep registry linked modules
		if strings.HasPrefix(rel, "_cache_") {
			skip = true
		}

		return skip, nil
	}

	if err := copy.Copy(stagingSourceFiles, infraPath, options); err != nil {
		return ResourceConfig{}, fmt.Errorf("copying contents from temp staging directory: %w", err)
	}

	body, err := os.ReadFile(filepath.Join(infraPath, "main.bicep"))
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("reading bicep file: %w", err)
	}

	lineCount := 0
	wordCount := 0
	// trim metadata headers (four lines)
	for i, b := range body {
		if b == '\n' {
			lineCount++
		}

		if lineCount == 4 {
			wordCount = i + 1
			break
		}
	}

	// trim 4 lines of metadata
	err = os.WriteFile(filepath.Join(infraPath, "main.bicep"), body[wordCount:], osutil.PermissionFile)
	if err != nil {
		return ResourceConfig{}, fmt.Errorf("writing bicep file: %w", err)
	}

	res.Module = path.Join("db", res.Name, "main.bicep")
	return res, nil
}

func (im *ImportManager) promptForDuplicates(
	ctx context.Context, console input.Console, staging string, target string) (skipSourceFiles map[string]struct{}, err error) {
	log.Printf(
		"synth checking for duplicates. source: %s target: %s",
		staging,
		target,
	)

	duplicateFiles, err := determineDuplicates(staging, target)
	if err != nil {
		return nil, fmt.Errorf("checking for overwrites: %w", err)
	}

	if len(duplicateFiles) > 0 {
		console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: "The following files would be overwritten:",
		})

		for _, file := range duplicateFiles {
			console.Message(ctx, fmt.Sprintf(" * %s", file))
		}

		selection, err := console.Select(ctx, input.ConsoleOptions{
			Message: "What would you like to do with these files?",
			Options: []string{
				"Overwrite files",
				"Keep my existing files unchanged",
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting to overwrite: %w", err)
		}

		switch selection {
		case 0: // overwrite
			return nil, nil
		case 1: // keep
			skipSourceFiles = make(map[string]struct{}, len(duplicateFiles))
			for _, file := range duplicateFiles {
				// this also cleans the result, which is important for matching
				sourceFile := filepath.Join(staging, file)
				skipSourceFiles[sourceFile] = struct{}{}
			}
			return skipSourceFiles, nil
		}
	}

	return nil, nil
}

// Returns files that are both present in source and target.
// The files returned are expressed in their relative paths to source/target.
func determineDuplicates(source string, target string) ([]string, error) {
	var duplicateFiles []string
	if err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		partial, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		if _, err := os.Stat(filepath.Join(target, partial)); err == nil {
			duplicateFiles = append(duplicateFiles, partial)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("enumerating template files: %w", err)
	}
	return duplicateFiles, nil
}

func (im *ImportManager) SynthAllInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (fs.FS, error) {
	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if len(projectConfig.Services) != 1 {
				return nil, errNoMultipleServicesWithAppHost
			}

			return im.dotNetImporter.SynthAllInfrastructure(ctx, projectConfig, svcConfig)
		}
	}

	infraSpec, err := infraSpec(projectConfig, im.env)
	if err != nil {
		return nil, fmt.Errorf("parsing infrastructure: %w", err)
	}

	if len(infraSpec.Services) > 0 {
		generatedFS := memfs.New()
		t, err := scaffold.Load()
		if err != nil {
			return nil, fmt.Errorf("loading scaffold templates: %w", err)
		}

		infraFS, err := scaffold.ExecInfraFs(t, *infraSpec)
		if err != nil {
			return nil, fmt.Errorf("executing scaffold templates: %w", err)
		}

		infraPathPrefix := DefaultPath
		if projectConfig.Infra.Path != "" {
			infraPathPrefix = projectConfig.Infra.Path
		}

		err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			err = generatedFS.MkdirAll(filepath.Join(infraPathPrefix, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
			if err != nil {
				return err
			}

			contents, err := fs.ReadFile(infraFS, path)
			if err != nil {
				return err
			}

			return generatedFS.WriteFile(filepath.Join(infraPathPrefix, path), contents, d.Type().Perm())
		})
		if err != nil {
			return nil, err
		}

		return generatedFS, nil
	}

	return nil, fmt.Errorf("this project does not contain any infrastructure to synthesize")
}

// Infra represents the (possibly temporarily generated) infrastructure. Call [Cleanup] when done with infrastructure,
// which will cause any temporarily generated files to be removed.
type Infra struct {
	Options    provisioning.Options
	cleanupDir string
}

func (i *Infra) Cleanup() error {
	if i.cleanupDir != "" {
		return os.RemoveAll(i.cleanupDir)
	}

	return nil
}

func infraSpec(projectConfig *ProjectConfig, env *environment.Environment) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}
	backendMapping := map[string]string{}

	for _, res := range projectConfig.Resources {
		switch res.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{
				Module: res.Module,
			}
		case ResourceTypeDbMongo:
			// todo: support servers and databases
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Name,
				Module:       res.Module,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Name,
				DatabaseUser: "pgadmin",
				Module:       res.Module,
			}
		}
	}

	for _, svc := range projectConfig.Services {
		svcSpec := scaffold.ServiceSpec{
			Name: svc.Name,
			Port: -1,
		}

		processedEnv := map[string]string{}
		for _, envVar := range svc.Env {
			val, err := envVar.Value.Envsubst(env.Getenv)
			if err != nil {
				return nil, fmt.Errorf("evaluating environment variable %s for service %s: %w", envVar.Name, svc.Name, err)
			}

			processedEnv[envVar.Name] = val
		}

		svcSpec.Env = processedEnv

		if svc.Port != 0 {
			port := svc.Port
			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("port value %d for service %s must be between 1 and 65535", svc.Port, svc.Name)
			}

			svcSpec.Port = port
		} else if svc.Docker.Path == "" {
			// default builder always specifies port 80
			svcSpec.Port = 80

			if svc.Language == ServiceLanguageJava {
				svcSpec.Port = 8080
			}
		}

		for _, use := range svc.Uses {
			useRes, isRes := projectConfig.Resources[use]
			if isRes {
				switch useRes.Type {
				case ResourceTypeDbMongo:
					svcSpec.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
				case ResourceTypeDbPostgres:
					svcSpec.DbPostgres = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
				case ResourceTypeDbRedis:
					svcSpec.DbRedis = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
				}
				continue
			}

			_, ok := projectConfig.Services[use]
			if ok {
				if svcSpec.Frontend == nil {
					svcSpec.Frontend = &scaffold.Frontend{}
				}

				svcSpec.Frontend.Backends = append(svcSpec.Frontend.Backends,
					scaffold.ServiceReference{Name: use})
				backendMapping[use] = svc.Name
				continue
			}

			return nil, fmt.Errorf("service %s uses %s, which does not exist", svc.Name, use)
		}

		infraSpec.Services = append(infraSpec.Services, svcSpec)
	}

	// create reverse mapping
	for _, svc := range infraSpec.Services {
		if front, ok := backendMapping[svc.Name]; ok {
			if svc.Backend == nil {
				svc.Backend = &scaffold.Backend{}
			}

			svc.Backend.Frontends = append(svc.Backend.Frontends, scaffold.ServiceReference{Name: front})
		}
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}
