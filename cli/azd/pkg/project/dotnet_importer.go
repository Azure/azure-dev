package project

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
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
	dotnetCli           dotnet.DotNetCli
	console             input.Console
	lazyEnv             *lazy.Lazy[*environment.Environment]
	lazyEnvManager      *lazy.Lazy[environment.Manager]
	alphaFeatureManager *alpha.FeatureManager

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
	alphaFeatureManager *alpha.FeatureManager,
) *DotNetImporter {
	return &DotNetImporter{
		dotnetCli:           dotnetCli,
		console:             console,
		lazyEnv:             lazyEnv,
		lazyEnvManager:      lazyEnvManager,
		alphaFeatureManager: alphaFeatureManager,
		cache:               make(map[manifestCacheKey]*apphost.Manifest),
		hostCheck:           make(map[string]hostCheckResult),
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
	manifest, err := ai.ReadManifest(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating app host manifest: %w", err)
	}

	files, err := apphost.BicepTemplate(manifest, apphost.AppHostOptions{
		AspireDashboard: apphost.IsAspireDashboardEnabled(ai.alphaFeatureManager),
	})
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

	manifest, err := ai.ReadManifest(ctx, svcConfig)
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
			AppHostPath: svcConfig.Path(),
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
			AppHostPath: svcConfig.Path(),
		}

		services[svc.Name] = svc
	}

	containers := apphost.Containers(manifest)
	for name, container := range containers {
		// TODO(ellismg): Some of this code is duplicated from project.Parse, we should centralize this logic long term.
		svc := &ServiceConfig{
			RelativePath: svcConfig.RelativePath,
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
			ContainerImage: container.Image,
			Manifest:       manifest,
			ProjectName:    name,
			AppHostPath:    svcConfig.Path(),
		}

		services[svc.Name] = svc
	}
	return services, nil
}

var autoConfigureDataProtectionFeature = alpha.MustFeatureKey("aspire.autoConfigureDataProtection")

func (ai *DotNetImporter) SynthAllInfrastructure(
	ctx context.Context, p *ProjectConfig, svcConfig *ServiceConfig,
) (fs.FS, error) {
	manifest, err := ai.ReadManifest(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating apphost manifest: %w", err)
	}

	generatedFS := memfs.New()

	infraFS, err := apphost.BicepTemplate(manifest, apphost.AppHostOptions{
		AspireDashboard: apphost.IsAspireDashboardEnabled(ai.alphaFeatureManager),
	})
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
	writeManifestForResource := func(name string) error {
		containerAppManifest, err := apphost.ContainerAppManifestTemplateForProject(
			manifest, name, apphost.AppHostOptions{
				AutoConfigureDataProtection: ai.alphaFeatureManager.IsEnabled(autoConfigureDataProtectionFeature),
			})
		if err != nil {
			return fmt.Errorf("generating containerApp.tmpl.yaml for resource %s: %w", name, err)
		}

		normalPath, err := filepath.EvalSymlinks(svcConfig.Path())
		if err != nil {
			return err
		}

		projectRelPath, err := filepath.Rel(root, normalPath)
		if err != nil {
			return err
		}

		manifestPath := filepath.Join(filepath.Dir(projectRelPath), "infra", fmt.Sprintf("%s.tmpl.yaml", name))

		if err := generatedFS.MkdirAll(filepath.Dir(manifestPath), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		return generatedFS.WriteFile(manifestPath, []byte(containerAppManifest), osutil.PermissionFileOwnerOnly)
	}

	for name := range apphost.ProjectPaths(manifest) {
		if err := writeManifestForResource(name); err != nil {
			return nil, err
		}
	}

	for name := range apphost.Dockerfiles(manifest) {
		if err := writeManifestForResource(name); err != nil {
			return nil, err
		}
	}

	for name := range apphost.Containers(manifest) {
		if err := writeManifestForResource(name); err != nil {
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
