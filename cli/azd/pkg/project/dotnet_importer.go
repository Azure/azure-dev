package project

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/psanford/memfs"
)

type hostCheckResult struct {
	is  bool
	err error
}

// DotNetImporter is an importer that is able to import projects and infrastructure from a manifest produced by a .NET App.
type DotNetImporter struct {
	dotnetCli      dotnet.DotNetCli
	console        input.Console
	lazyEnv        *lazy.Lazy[*environment.Environment]
	lazyEnvManager *lazy.Lazy[environment.Manager]

	// TODO(ellismg): This cache exists because we end up needing the same manifest multiple times for a single logical
	// operation and it is expensive to generate. We should consider if this is the correct location for the cache or if
	// it should be in some higher level component. Right now the lifetime issues are not too large of a deal, since
	// `azd` processes are short lived.
	cache   map[manifestCacheKey]*apphost.Manifest
	cacheMu sync.Mutex

	hostCheck   map[string]hostCheckResult
	hostCheckMu sync.Mutex
}

// manifestCacheKey is the key we use when caching manifests. It is a combination of the project path and the
// DOTNET_ENVIRONMENT value (which can influence manifest generation)
type manifestCacheKey struct {
	projectPath       string
	dotnetEnvironment string
}

func NewDotNetImporter(
	dotnetCli dotnet.DotNetCli,
	console input.Console,
	lazyEnv *lazy.Lazy[*environment.Environment],
	lazyEnvManager *lazy.Lazy[environment.Manager],
) *DotNetImporter {
	return &DotNetImporter{
		dotnetCli:      dotnetCli,
		console:        console,
		lazyEnv:        lazyEnv,
		lazyEnvManager: lazyEnvManager,
		cache:          make(map[manifestCacheKey]*apphost.Manifest),
		hostCheck:      make(map[string]hostCheckResult),
	}
}

// CanImport returns true when the given project can be imported by this importer. Only some .NET Apps are able
// to produce the manifest that importer expects.
func (ai *DotNetImporter) CanImport(ctx context.Context, projectPath string) (bool, error) {
	ai.hostCheckMu.Lock()
	defer ai.hostCheckMu.Unlock()

	if v, has := ai.hostCheck[projectPath]; has {
		return v.is, v.err
	}

	value, err := ai.dotnetCli.GetMsBuildProperty(ctx, projectPath, "IsAspireHost")
	if err != nil {
		ai.hostCheck[projectPath] = hostCheckResult{
			is:  false,
			err: err,
		}

		return false, err
	}

	ai.hostCheck[projectPath] = hostCheckResult{
		is:  strings.TrimSpace(value) == "true",
		err: nil,
	}

	return strings.TrimSpace(value) == "true", nil
}

func (ai *DotNetImporter) ProjectInfrastructure(ctx context.Context, svcConfig *ServiceConfig) (*Infra, error) {
	manifest, err := ai.ReadManifestEnsureExposedServices(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating app host manifest: %w", err)
	}

	files, err := apphost.BicepTemplate(manifest)
	if err != nil {
		return nil, fmt.Errorf("generating bicep from manifest: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
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

// mapToStringSlice converts a map of strings to a slice of strings.
// Each key-value pair in the map is converted to a string in the format "key:value",
// where the separator is specified by the `separator` parameter.
// If the value is an empty string, only the key is included in the resulting slice.
// The resulting slice is returned.
func mapToStringSlice(m map[string]string, separator string) []string {
	var result []string
	for key, value := range m {
		if value == "" {
			result = append(result, key)
		} else {
			result = append(result, key+separator+value)
		}
	}
	return result
}

func (ai *DotNetImporter) Services(
	ctx context.Context, p *ProjectConfig, svcConfig *ServiceConfig,
) (map[string]*ServiceConfig, error) {
	services := make(map[string]*ServiceConfig)

	manifest, err := ai.ReadManifestEnsureExposedServices(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating app host manifest: %w", err)
	}

	projects := apphost.ProjectPaths(manifest)
	for name, path := range projects {
		relPath, err := filepath.Rel(p.Path, path)
		if err != nil {
			return nil, err
		}

		// TODO(ellismg): Some of this code is duplicated from project.Parse, we should centralize this logic long term.
		svc := &ServiceConfig{
			RelativePath: relPath,
			Language:     ServiceLanguageDotNet,
			Host:         DotNetContainerAppTarget,
		}

		svc.Name = name
		svc.Project = p
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

		svc.Infra.Provider, err = provisioning.ParseProvider(svc.Infra.Provider)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.DotNetContainerApp = &DotNetContainerAppOptions{
			Manifest:    manifest,
			ProjectName: name,
			ProjectPath: svcConfig.Path(),
		}

		services[svc.Name] = svc
	}

	dockerfiles := apphost.Dockerfiles(manifest)
	for name, dockerfile := range dockerfiles {
		relPath, err := filepath.Rel(p.Path, filepath.Dir(dockerfile.Path))
		if err != nil {
			return nil, err
		}

		// TODO(ellismg): Some of this code is duplicated from project.Parse, we should centralize this logic long term.
		svc := &ServiceConfig{
			RelativePath: relPath,
			Language:     ServiceLanguageDocker,
			Host:         DotNetContainerAppTarget,
			Docker: DockerProjectOptions{
				Path:      dockerfile.Path,
				Context:   dockerfile.Context,
				BuildArgs: mapToStringSlice(dockerfile.BuildArgs, "="),
			},
		}

		svc.Name = name
		svc.Project = p
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

		svc.Infra.Provider, err = provisioning.ParseProvider(svc.Infra.Provider)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.DotNetContainerApp = &DotNetContainerAppOptions{
			Manifest:    manifest,
			ProjectName: name,
			ProjectPath: svcConfig.Path(),
		}

		services[svc.Name] = svc
	}
	return services, nil
}

func (ai *DotNetImporter) SynthAllInfrastructure(
	ctx context.Context, p *ProjectConfig, svcConfig *ServiceConfig,
) (fs.FS, error) {
	manifest, err := ai.ReadManifestEnsureExposedServices(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating apphost manifest: %w", err)
	}

	generatedFS := memfs.New()

	infraFS, err := apphost.BicepTemplate(manifest)
	if err != nil {
		return nil, fmt.Errorf("generating infra/ folder: %w", err)
	}

	err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		err = generatedFS.MkdirAll(filepath.Join("infra", filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		contents, err := fs.ReadFile(infraFS, path)
		if err != nil {
			return err
		}

		return generatedFS.WriteFile(filepath.Join("infra", path), contents, d.Type().Perm())

	})
	if err != nil {
		return nil, err
	}

	// Use canonical paths for Rel comparison due to absolute paths provided by ManifestFromAppHost
	// being possibly symlinked paths.
	root, err := filepath.EvalSymlinks(p.Path)
	if err != nil {
		return nil, err
	}

	// writeManifestForResource writes the containerApp.tmpl.yaml for the given resource to the generated filesystem. The
	// manifest is written to a file name "containerApp.tmpl.yaml" in the same directory as the project that produces the
	// container we will deploy.
	writeManifestForResource := func(name string, path string) error {
		containerAppManifest, err := apphost.ContainerAppManifestTemplateForProject(manifest, name)
		if err != nil {
			return fmt.Errorf("generating containerApp.tmpl.yaml for resource %s: %w", name, err)
		}

		normalPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}

		projectRelPath, err := filepath.Rel(root, normalPath)
		if err != nil {
			return err
		}

		manifestPath := filepath.Join(filepath.Dir(projectRelPath), "manifests", "containerApp.tmpl.yaml")

		if err := generatedFS.MkdirAll(filepath.Dir(manifestPath), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		return generatedFS.WriteFile(manifestPath, []byte(containerAppManifest), osutil.PermissionFileOwnerOnly)
	}

	for name, path := range apphost.ProjectPaths(manifest) {
		if err := writeManifestForResource(name, path); err != nil {
			return nil, err
		}
	}

	for name, docker := range apphost.Dockerfiles(manifest) {
		if err := writeManifestForResource(name, docker.Path); err != nil {
			return nil, err
		}
	}

	return generatedFS, nil
}

// ReadManifest reads the manifest for the given app host service, and caches the result.
func (ai *DotNetImporter) ReadManifest(ctx context.Context, svcConfig *ServiceConfig) (*apphost.Manifest, error) {
	ai.cacheMu.Lock()
	defer ai.cacheMu.Unlock()

	var dotnetEnv string

	if env, err := ai.lazyEnv.GetValue(); err == nil {
		dotnetEnv = env.Getenv("DOTNET_ENVIRONMENT")
	}

	cacheKey := manifestCacheKey{
		projectPath:       svcConfig.Path(),
		dotnetEnvironment: dotnetEnv,
	}

	if cached, has := ai.cache[cacheKey]; has {
		return cached, nil
	}

	ai.console.ShowSpinner(ctx, "Analyzing Aspire Application (this might take a moment...)", input.Step)
	manifest, err := apphost.ManifestFromAppHost(ctx, svcConfig.Path(), ai.dotnetCli, dotnetEnv)
	ai.console.StopSpinner(ctx, "", input.Step)
	if err != nil {
		return nil, err
	}

	ai.cache[cacheKey] = manifest
	return manifest, nil
}

// ReadManifestEnsureExposedServices calls ReadManifest. It also reads the value of
// the `services.<name>.config.exposedServices` property from the environment and sets the `External` property on
// each binding for the exposed services. If this key does not exist in the config for the environment, the user
// is prompted to select which services should be exposed. This can happen after an environment is created with
// `azd env new`.
func (ai *DotNetImporter) ReadManifestEnsureExposedServices(
	ctx context.Context,
	svcConfig *ServiceConfig) (*apphost.Manifest, error) {
	manifest, err := ai.ReadManifest(ctx, svcConfig)
	if err != nil {
		return nil, err
	}

	env, err := ai.lazyEnv.GetValue()
	if err == nil {
		if cfgValue, has := env.Config.Get(fmt.Sprintf("services.%s.config.exposedServices", svcConfig.Name)); has {
			if exposedServices, is := cfgValue.([]interface{}); !is {
				log.Printf("services.%s.config.exposedServices is not an array, ignoring setting.", svcConfig.Name)
			} else {
				for idx, name := range exposedServices {
					if strName, ok := name.(string); !ok {
						log.Printf("services.%s.config.exposedServices[%d] is not a string, ignoring value.",
							svcConfig.Name, idx)
					} else {
						// This can happen if the user has removed a service from their app host that they previously
						// had and had exposed (or changed the service such that it no longer has any bindings).
						if binding, has := manifest.Resources[strName]; !has || binding.Bindings == nil {
							log.Printf("service %s does not exist or has no bindings, ignoring value.", strName)
							continue
						}

						for _, binding := range manifest.Resources[strName].Bindings {
							binding.External = true
						}
					}
				}
			}
		} else {
			selector := apphost.NewIngressSelector(manifest, ai.console)
			exposed, err := selector.SelectPublicServices(ctx)
			if err != nil {
				return nil, fmt.Errorf("selecting public services: %w", err)
			}

			for _, name := range exposed {
				for _, binding := range manifest.Resources[name].Bindings {
					binding.External = true
				}
			}

			err = env.Config.Set(fmt.Sprintf("services.%s.config.exposedServices", svcConfig.Name), exposed)
			if err != nil {
				return nil, err
			}

			envManager, err := ai.lazyEnvManager.GetValue()
			if err != nil {
				return nil, err
			}

			if err := envManager.Save(ctx, env); err != nil {
				return nil, err
			}

		}
	} else {
		log.Printf("unexpected error fetching environment: %s, exposed services may not be correct", err)
	}

	return manifest, nil
}
