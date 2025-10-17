// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
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
	dotnetCli           *dotnet.Cli
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
	dotnetCli *dotnet.Cli,
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

	isAppHost, err := ai.dotnetCli.IsAspireHostProject(ctx, projectPath)
	if err != nil {
		ai.hostCheck[projectPath] = hostCheckResult{
			is:  false,
			err: err,
		}

		return false, err
	}

	ai.hostCheck[projectPath] = hostCheckResult{
		is:  isAppHost,
		err: nil,
	}

	return isAppHost, nil
}

func (ai *DotNetImporter) ProjectInfrastructure(ctx context.Context, svcConfig *ServiceConfig) (*Infra, error) {
	manifest, err := ai.ReadManifest(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating app host manifest: %w", err)
	}

	manifestWarnings := manifest.Warnings()
	if manifestWarnings != "" {
		ai.console.Message(ctx, "")
		ai.console.Message(ctx, manifestWarnings)
		ai.console.Message(ctx, "")
	}

	azdOperationsEnabled := ai.alphaFeatureManager.IsEnabled(provisioning.AzdOperationsFeatureKey)
	files, err := apphost.BicepTemplate("main", manifest, apphost.AppHostOptions{
		AzdOperations: azdOperationsEnabled,
	})
	if err != nil {
		if errors.Is(err, provisioning.ErrAzdOperationsNotEnabled) {
			// Use a warning for this error about azd operations is required for the current project to fully work
			ai.console.Message(ctx, err.Error())
		} else {
			return nil, fmt.Errorf("generating bicep from manifest: %w", err)
		}
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

		return os.WriteFile(target, contents, osutil.PermissionFile)
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

// mapToExpandableStringSlice converts a map of strings to a slice of expandable strings.
// Each key-value pair in the map is converted to a string in the format "key:value",
// where the separator is specified by the `separator` parameter.
// If the value is an empty string, only the key is included in the resulting slice.
// The resulting slice is returned without any string interpolation performed.
func mapToExpandableStringSlice(m map[string]string, separator string) []osutil.ExpandableString {
	var result []osutil.ExpandableString
	for key, value := range m {
		if value == "" {
			result = append(result, osutil.NewExpandableString(key))
		} else {
			result = append(result, osutil.NewExpandableString(key+separator+value))
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
				BuildArgs: mapToExpandableStringSlice(dockerfile.BuildArgs, "="),
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

	buildContainers, err := apphost.BuildContainers(manifest)
	if err != nil {
		return nil, err
	}
	for name, bContainer := range buildContainers {

		defaultLanguage := ServiceLanguageDotNet
		relativePath := svcConfig.RelativePath
		var dOptions DockerProjectOptions

		if bContainer.Build != nil {
			defaultLanguage = ServiceLanguageDocker
			// dockerfiles are copied to the infra folder like bicep files to ensure
			// provision and deploy works after infra gen.
			bContainer.Build.Dockerfile = filepath.Join(
				svcConfig.Project.Path, svcConfig.Project.Infra.Path, name, filepath.Base(bContainer.Build.Dockerfile))

			// If the dockerfile is not in disk, it could have been manually deleted (after infra gen) or
			// infra gen was never run. In any case, use the in-memory generated dockerfile to build the container.
			var inMemDockerfile []byte
			if _, err := os.Stat(bContainer.Build.Dockerfile); errors.Is(err, os.ErrNotExist) {
				// write file in the temp folder
				fileName := filepath.Base(bContainer.Build.Dockerfile)
				// read content from memory fs in the manifest
				data, err := fs.ReadFile(manifest.Files, filepath.Join(name, fileName))
				if err != nil {
					return nil, fmt.Errorf("reading dockerfile for service %s: %w", name, err)
				}
				inMemDockerfile = data
			}

			relPath, err := filepath.Rel(p.Path, filepath.Dir(bContainer.Build.Context))
			if err != nil {
				return nil, err
			}
			relativePath = relPath

			bArgs, err := evaluateBuildArgs(*manifest, bContainer.Build.Args)
			if err != nil {
				return nil, fmt.Errorf("evaluating build args for service %s: %w", name, err)
			}
			bArgsArray, reqEnv, err := buildArgsArrayAndEnv(*manifest, bContainer.Build.Secrets)
			if err != nil {
				return nil, fmt.Errorf("converting build args to array for service %s: %w", name, err)
			}

			dOptions = DockerProjectOptions{
				Path:            bContainer.Build.Dockerfile,
				Context:         bContainer.Build.Context,
				BuildArgs:       mapToExpandableStringSlice(bArgs, "="),
				BuildSecrets:    bArgsArray,
				BuildEnv:        reqEnv,
				InMemDockerfile: inMemDockerfile,
			}
		}

		svc := &ServiceConfig{
			RelativePath: relativePath,
			Language:     defaultLanguage,
			Host:         DotNetContainerAppTarget,
			Docker:       dOptions,
		}

		svc.Name = name
		svc.Project = p
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

		svc.Infra.Provider, err = provisioning.ParseProvider(svc.Infra.Provider)
		if err != nil {
			return nil, fmt.Errorf("parsing service %s: %w", svc.Name, err)
		}

		svc.DotNetContainerApp = &DotNetContainerAppOptions{
			ContainerImage: bContainer.Image,
			Manifest:       manifest,
			ProjectName:    name,
			AppHostPath:    svcConfig.Path(),
		}
		services[svc.Name] = svc

	}
	return services, nil
}

// buildArgsArray produces an array of args to pass to the container build command.
// See: https://docs.docker.com/build/building/secrets/
func buildArgsArrayAndEnv(
	manifest apphost.Manifest,
	bArgs map[string]apphost.ContainerV1BuildSecrets) ([]string, []string, error) {
	var result []string
	var reqEnv []string

	for bArgKey, bArg := range bArgs {
		if bArg.Type != "env" && bArg.Type != "file" {
			return nil, nil, fmt.Errorf("unsupported secret type %q for build arg %q", bArg.Type, bArgKey)
		}

		baseArg := fmt.Sprintf("id=%s", bArgKey)
		if bArg.Type == "file" {
			if bArg.Source == nil {
				return nil, nil, fmt.Errorf("missing source for file secret %q", bArgKey)
			}
			baseArg = fmt.Sprintf("id=%s,src=%s", bArgKey, *bArg.Source)
		}
		if bArg.Type == "env" {
			if bArg.Value == nil {
				return nil, nil, fmt.Errorf("missing value for env secret %q", bArgKey)
			}
			bArgValue, err := evaluateExpressionsFromArg(*bArg.Value, manifest)
			if err != nil {
				return nil, nil, fmt.Errorf("evaluating value for env secret %q: %w", bArgKey, err)
			}
			reqEnv = append(reqEnv, fmt.Sprintf("%s=%s", bArgKey, bArgValue))
		}
		result = append(result, baseArg)
	}

	return result, reqEnv, nil
}

// evaluateBuildArgs evaluates the build args in the manifest, replacing any expressions with their evaluated values.
// If the expression cannot be evaluated at this time, it will be replaced with a placeholder that
// indicates that the value will be resolved later, such as during the container build process.
// The placeholder is in the form of "{infra.parameters.parameterName}".
// If the expression can be evaluated, it will be replaced with the evaluated value.
// If the expression references an environment variable, it will be replaced with the value of that environment
// variable, if it exists. If the environment variable does not exist, the expression will be
// replaced with a placeholder that indicates it will be resolved later.
func evaluateBuildArgs(
	manifest apphost.Manifest, args map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(args))
	for argKey, argValue := range args {
		evaluatedValue, err := evaluateExpressionsFromArg(argValue, manifest)
		if err != nil {
			return nil, err
		}
		result[argKey] = evaluatedValue

	}
	return result, nil
}

// evaluateExpressionsFromArg evaluates the expressions in the given source string.
// It uses the manifest to resolve the expressions, which are expected to be in the form of
// "{resourceName.value}" where resourceName is the name of a resource in the manifest.
// If the expression cannot be resolved, it will return an error.
// If the expression references an environment variable, it will be replaced with the value of that environment
// variable, if it exists. If the environment variable does not exist, the expression will be
// replaced with a placeholder that indicates it will be resolved later.
func evaluateExpressionsFromArg(source string, manifest apphost.Manifest) (string, error) {
	return apphost.EvalString(source, func(match string) (string, error) {
		return evaluateSingleBuildArg(match, manifest)
	})
}

// evaluateSingleBuildArg processes a single build argument expression from a manifest.
// It attempts to resolve the value for a parameter-type resource by checking:
// 1. The manifest's resource value (if it's a constant)
// 2. Environment variables
// 3. Infrastructure parameters
//
// Parameters:
//   - match: The expression string to evaluate in format "resource.value"
//   - manifest: The application host manifest containing resource definitions
//
// Returns:
//   - string: The resolved value or a parameter reference in format "{infra.parameter_name}"
//   - error: An error if the expression is invalid, resource not found, or resource type is unsupported
//
// The function supports parameter names with hyphens, which are converted to underscores
// for compatibility with Bicep parameter naming conventions.
func evaluateSingleBuildArg(
	match string, manifest apphost.Manifest) (string, error) {

	exp := match
	resourceAndPath := strings.SplitN(exp, ".", 2)
	if len(resourceAndPath) != 2 {
		log.Println("malformed binding expression, expected <resource>.<property> but was:", match)
		return "", apphost.UnrecognizedExpressionError{}
	}
	resourceName := resourceAndPath[0]
	resource, has := manifest.Resources[resourceName]
	if !has {
		return match, fmt.Errorf("resource %q not found in manifest", resourceName)
	}
	if resource.Type != "parameter.v0" {
		return match, fmt.Errorf(
			"resource %q is not a parameter. Only parameters are supported for build args expressions",
			resourceName)
	}
	inputParam, err := apphost.InputParameter(resourceName, resource)
	if err != nil {
		return match, fmt.Errorf("getting input parameter for resource %q: %w", resourceName, err)
	}
	if inputParam == nil {
		// parameter not using inputs, has a constant value, use it
		return resource.Value, nil
	}
	fromEnvVar := strings.TrimSuffix(scaffold.EnvFormat(resourceName)[2:], "}")
	if valueInEnv := os.Getenv(fromEnvVar); valueInEnv != "" {
		log.Println("Using value from environment variable", fromEnvVar, "for parameter", resourceName)
		return valueInEnv, nil
	}

	// handle parameters renaming. Hyphens are supported in the manifest, but renamed to underscores for bicep parameters.
	// If the arg is not resolved at this point, the name must be updated considering the renaming.
	finalParamName := strings.ReplaceAll(resourceName, "-", "_")

	// can't resolve the parameter here yet, best we can do is resolve the name of the parameter, removing the path
	// of the resource from the expression, keeping only the name of the expected parameter.
	// The parameter might not be requested at this point, because it could be
	// the first time azd is running for the project.
	return fmt.Sprintf("{%s%s}", infraParametersKey, finalParamName), nil
}

func (ai *DotNetImporter) GenerateAllInfrastructure(ctx context.Context, p *ProjectConfig, svcConfig *ServiceConfig,
) (fs.FS, error) {
	manifest, err := ai.ReadManifest(ctx, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("generating apphost manifest: %w", err)
	}

	generatedFS := memfs.New()

	rootModuleName := DefaultModule
	if p.Infra.Module != "" {
		rootModuleName = p.Infra.Module
	}

	azdOperationsEnabled := ai.alphaFeatureManager.IsEnabled(provisioning.AzdOperationsFeatureKey)
	infraFS, err := apphost.BicepTemplate(rootModuleName, manifest, apphost.AppHostOptions{
		AzdOperations: azdOperationsEnabled,
	})
	if err != nil {
		if errors.Is(err, provisioning.ErrAzdOperationsNotEnabled) {
			// Use a warning for this error about azd operations is required for the current project to fully work
			ai.console.Message(ctx, err.Error())
		} else {
			return nil, fmt.Errorf("generating infra/ folder: %w", err)
		}
	}

	infraPathPrefix := DefaultPath
	if p.Infra.Path != "" {
		infraPathPrefix = p.Infra.Path
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

	// Use canonical paths for Rel comparison due to absolute paths provided by ManifestFromAppHost
	// being possibly symlinked paths.
	root, err := filepath.EvalSymlinks(p.Path)
	if err != nil {
		return nil, err
	}

	// writeManifestForResource writes the containerApp.tmpl.yaml or containerApp.bicepparam for the given resource to the
	// generated filesystem. The manifest is written to a file name "containerApp.tmpl.yaml" or
	// "containerApp.tmpl.bicepparam" in the same directory as the project that produces the
	// container we will deploy.
	writeManifestForResource := func(name string) error {
		normalPath, err := filepath.EvalSymlinks(svcConfig.Path())
		if err != nil {
			return err
		}

		projectRelPath, err := filepath.Rel(root, normalPath)
		if err != nil {
			return err
		}

		containerAppManifest, manifestType, err := apphost.ContainerAppManifestTemplateForProject(
			manifest, name, apphost.AppHostOptions{})
		if err != nil {
			return fmt.Errorf("generating containerApp deployment manifest for resource %s: %w", name, err)
		}

		manifestPath := filepath.Join(filepath.Dir(projectRelPath), "infra", fmt.Sprintf("%s.tmpl.yaml", name))
		if manifestType == apphost.ContainerAppManifestTypeBicep {
			manifestPath = filepath.Join(
				filepath.Dir(projectRelPath), "infra", name, fmt.Sprintf("%s.tmpl.bicepparam", name))
		}

		if err := generatedFS.MkdirAll(filepath.Dir(manifestPath), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		err = generatedFS.WriteFile(manifestPath, []byte(containerAppManifest), osutil.PermissionFileOwnerOnly)
		if err != nil {
			return err
		}

		return nil
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

	bcs, err := apphost.BuildContainers(manifest)
	if err != nil {
		return nil, err
	}
	for name := range bcs {
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
