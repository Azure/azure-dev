// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	skipGitMetadataMavenOption = "-Dmaven.gitcommitid.skip=true"
)

type javaFrameworkService struct {
	client *azdext.AzdClient
	runner azdexec.CommandRunner

	mu      sync.Mutex
	service *project.ServiceConfig
}

// NewJavaFrameworkService creates a Java framework provider that preserves azd's native Maven behavior.
func NewJavaFrameworkService(client *azdext.AzdClient) azdext.FrameworkServiceProvider {
	return newJavaFrameworkService(client, azdexec.NewCommandRunner(&azdexec.RunnerOptions{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}))
}

func newJavaFrameworkService(client *azdext.AzdClient, runner azdexec.CommandRunner) *javaFrameworkService {
	return &javaFrameworkService{
		client: client,
		runner: runner,
	}
}

func (p *javaFrameworkService) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	service, err := nativeServiceConfig(serviceConfig)
	if err != nil {
		return err
	}
	p.service = service

	framework := p.nativeFramework(environment.New(""))
	if err := framework.Initialize(ctx, service); err != nil {
		return err
	}
	for _, tool := range framework.RequiredExternalTools(ctx, service) {
		if err := tool.CheckInstalled(ctx); err != nil {
			return fmt.Errorf("checking required Java tool %s: %w", tool.Name(), err)
		}
	}
	return nil
}

func (p *javaFrameworkService) RequiredExternalTools(
	ctx context.Context,
	_ *azdext.ServiceConfig,
) ([]*azdext.ExternalTool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.service == nil {
		return nil, errors.New("Java framework service is not initialized")
	}
	framework := p.nativeFramework(environment.New(""))
	if err := framework.Initialize(ctx, p.service); err != nil {
		return nil, err
	}
	tools := framework.RequiredExternalTools(ctx, p.service)
	result := make([]*azdext.ExternalTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, &azdext.ExternalTool{
			Name:       tool.Name(),
			InstallUrl: tool.InstallUrl(),
		})
	}
	return result, nil
}

func (p *javaFrameworkService) Requirements() (*azdext.FrameworkRequirements, error) {
	requirements := project.NewMavenProject(
		nil,
		maven.NewCli(p.runner),
		javac.NewCli(p.runner),
	).Requirements()
	return &azdext.FrameworkRequirements{
		Package: &azdext.FrameworkPackageRequirements{
			RequireRestore: requirements.Package.RequireRestore,
			RequireBuild:   requirements.Package.RequireBuild,
		},
	}, nil
}

func (p *javaFrameworkService) Restore(
	ctx context.Context,
	_ *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServiceRestoreResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	framework, service, err := p.frameworkForOperation(ctx, nil)
	if err != nil {
		return nil, err
	}
	result, err := runWithProgress(ctx, progress, func(nativeProgress *async.Progress[project.ServiceProgress]) (
		*project.ServiceRestoreResult,
		error,
	) {
		return framework.Restore(ctx, service, nativeServiceContext(serviceContext), nativeProgress)
	})
	if err != nil {
		return nil, err
	}
	return &azdext.ServiceRestoreResult{Artifacts: extensionArtifacts(result.Artifacts)}, nil
}

func (p *javaFrameworkService) Build(
	ctx context.Context,
	_ *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServiceBuildResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	framework, service, err := p.frameworkForOperation(ctx, nil)
	if err != nil {
		return nil, err
	}
	result, err := runWithProgress(ctx, progress, func(nativeProgress *async.Progress[project.ServiceProgress]) (
		*project.ServiceBuildResult,
		error,
	) {
		return framework.Build(ctx, service, nativeServiceContext(serviceContext), nativeProgress)
	})
	if err != nil {
		return nil, err
	}
	return &azdext.ServiceBuildResult{Artifacts: extensionArtifacts(result.Artifacts)}, nil
}

func (p *javaFrameworkService) Package(
	ctx context.Context,
	_ *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServicePackageResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	framework, service, err := p.frameworkForOperation(ctx, progress)
	if err != nil {
		return nil, err
	}
	result, err := runWithProgress(ctx, progress, func(nativeProgress *async.Progress[project.ServiceProgress]) (
		*project.ServicePackageResult,
		error,
	) {
		return framework.Package(ctx, service, nativeServiceContext(serviceContext), nativeProgress)
	})
	if err != nil {
		return nil, err
	}
	return &azdext.ServicePackageResult{Artifacts: extensionArtifacts(result.Artifacts)}, nil
}

func (p *javaFrameworkService) frameworkForOperation(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (project.FrameworkService, *project.ServiceConfig, error) {
	if p.service == nil {
		return nil, nil, errors.New("Java framework service is not initialized")
	}
	if p.client == nil {
		return nil, nil, errors.New("azd client is required")
	}

	name, values, err := (&grpcEnvironmentStore{client: p.client}).values(ctx)
	if err != nil {
		return nil, nil, err
	}
	env := environment.NewWithValues(name, values)
	env.DotenvSet(environment.EnvNameEnvVarName, name)
	framework := p.nativeFrameworkWithProgress(env, progress)
	if err := framework.Initialize(ctx, p.service); err != nil {
		return nil, nil, err
	}
	return framework, p.service, nil
}

func (p *javaFrameworkService) nativeFramework(env *environment.Environment) project.FrameworkService {
	return p.nativeFrameworkWithProgress(env, nil)
}

func (p *javaFrameworkService) nativeFrameworkWithProgress(
	env *environment.Environment,
	progress grpcbroker.ProgressFunc,
) project.FrameworkService {
	runner := &mavenPackageCommandRunner{
		base:     p.runner,
		spring:   p.service != nil && p.service.Host == project.ServiceTargetKind(SpringHost),
		progress: progress,
	}
	return project.NewMavenProject(env, maven.NewCli(runner), javac.NewCli(runner))
}

type mavenPackageCommandRunner struct {
	base     azdexec.CommandRunner
	spring   bool
	progress grpcbroker.ProgressFunc
}

func (r *mavenPackageCommandRunner) Run(ctx context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
	if r.spring && isNativeMavenPackage(args) {
		unborn, err := hasUnbornGitHead(ctx, r.base, args.Cwd, args.Env)
		if err != nil {
			return azdexec.RunResult{}, err
		}
		if unborn {
			if r.progress != nil {
				r.progress("Packaging Maven project without Git commit metadata because the repository has no commits.")
			}
			args.Args = append(args.Args, skipGitMetadataMavenOption)
		}
	}
	result, err := r.base.Run(ctx, args)
	return result, nonDisclosureError(err)
}

func (r *mavenPackageCommandRunner) RunList(
	ctx context.Context,
	commands []string,
	args azdexec.RunArgs,
) (azdexec.RunResult, error) {
	result, err := r.base.RunList(ctx, commands, args)
	return result, nonDisclosureError(err)
}

func (r *mavenPackageCommandRunner) ToolInPath(name string) error {
	return r.base.ToolInPath(name)
}

func isNativeMavenPackage(args azdexec.RunArgs) bool {
	return args.Cwd != "" &&
		len(args.Args) == 2 &&
		args.Args[0] == "package" &&
		args.Args[1] == "-DskipTests"
}

func hasUnbornGitHead(
	ctx context.Context,
	runner azdexec.CommandRunner,
	servicePath string,
	commandEnv []string,
) (bool, error) {
	hasMetadata, err := hasGitMetadata(servicePath, commandEnv)
	if err != nil {
		return false, nonDisclosureError(fmt.Errorf("inspecting Git metadata: %w", err))
	}
	if !hasMetadata {
		return false, nil
	}

	result, err := runner.Run(ctx, azdexec.NewRunArgs(
		"git",
		"--no-optional-locks",
		"status",
		"--porcelain=v2",
		"--branch",
		"--untracked-files=no",
	).WithCwd(servicePath).WithEnv(gitInspectionEnvironment(commandEnv)))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nonDisclosureError(fmt.Errorf("inspecting Git HEAD: %w", err))
	}
	if result.ExitCode != 0 {
		return false, nonDisclosureError(fmt.Errorf(
			"inspecting Git HEAD failed with exit code %d: %s",
			result.ExitCode,
			strings.TrimSpace(result.Stderr),
		))
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(result.Stdout, "\r\n", "\n"), "\n") {
		if line == "# branch.oid (initial)" {
			return true, nil
		}
	}
	return false, nil
}

func hasGitMetadata(servicePath string, commandEnv []string) (bool, error) {
	if gitDir, set := effectiveEnvironmentValue(commandEnv, "GIT_DIR"); set && gitDir != "" {
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(servicePath, gitDir)
		}
		if _, err := os.Lstat(gitDir); err != nil {
			return false, fmt.Errorf("accessing explicit Git directory %q: %w", gitDir, err)
		}
		return true, nil
	}

	current, err := filepath.Abs(servicePath)
	if err != nil {
		return false, fmt.Errorf("resolving service path: %w", err)
	}
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("accessing Git metadata under %q: %w", current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func gitInspectionEnvironment(commandEnv []string) []string {
	result := []string{"GIT_OPTIONAL_LOCKS=0"}
	for _, name := range []string{
		"GIT_DIR",
		"GIT_WORK_TREE",
		"GIT_COMMON_DIR",
		"GIT_CEILING_DIRECTORIES",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM",
	} {
		if value, set := environmentSliceValue(commandEnv, name); set {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func effectiveEnvironmentValue(commandEnv []string, name string) (string, bool) {
	if value, set := environmentSliceValue(commandEnv, name); set {
		return value, true
	}
	return os.LookupEnv(name)
}

func environmentSliceValue(values []string, name string) (string, bool) {
	for i := len(values) - 1; i >= 0; i-- {
		key, value, found := strings.Cut(values[i], "=")
		if found && key == name {
			return value, true
		}
	}
	return "", false
}

func nativeServiceConfig(serviceConfig *azdext.ServiceConfig) (*project.ServiceConfig, error) {
	if serviceConfig == nil {
		return nil, errors.New("service configuration is required")
	}
	projectDir, err := azdext.GetProjectDir()
	if err != nil {
		return nil, fmt.Errorf("getting project directory: %w", err)
	}

	serviceEnv := make(osutil.ExpandableMap, len(serviceConfig.GetEnvironment()))
	for key, value := range serviceConfig.GetEnvironment() {
		serviceEnv[key] = osutil.NewExpandableString(value)
	}

	return &project.ServiceConfig{
		Project: &project.ProjectConfig{
			Path:     projectDir,
			Services: map[string]*project.ServiceConfig{},
		},
		Name:              serviceConfig.GetName(),
		ResourceGroupName: osutil.NewExpandableString(serviceConfig.GetResourceGroupName()),
		ResourceName:      osutil.NewExpandableString(serviceConfig.GetResourceName()),
		ApiVersion:        serviceConfig.GetApiVersion(),
		RelativePath:      serviceConfig.GetRelativePath(),
		Host:              project.ServiceTargetKind(serviceConfig.GetHost()),
		Language:          project.ServiceLanguageKind(serviceConfig.GetLanguage()),
		OutputPath:        serviceConfig.GetOutputPath(),
		Image:             osutil.NewExpandableString(serviceConfig.GetImage()),
		Uses:              append([]string(nil), serviceConfig.GetUses()...),
		Environment:       serviceEnv,
		Config:            structMap(serviceConfig.GetConfig()),
		AdditionalProperties: structMap(
			serviceConfig.GetAdditionalProperties(),
		),
	}, nil
}

func structMap(value *structpb.Struct) map[string]any {
	if value == nil {
		return nil
	}
	return value.AsMap()
}

func nativeServiceContext(serviceContext *azdext.ServiceContext) *project.ServiceContext {
	if serviceContext == nil {
		return project.NewServiceContext()
	}
	return &project.ServiceContext{
		Restore: nativeArtifacts(serviceContext.GetRestore()),
		Build:   nativeArtifacts(serviceContext.GetBuild()),
		Package: nativeArtifacts(serviceContext.GetPackage()),
		Publish: nativeArtifacts(serviceContext.GetPublish()),
		Deploy:  nativeArtifacts(serviceContext.GetDeploy()),
	}
}

func nativeArtifacts(artifacts []*azdext.Artifact) project.ArtifactCollection {
	result := make(project.ArtifactCollection, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		result = append(result, &project.Artifact{
			Kind:         nativeArtifactKind(artifact.GetKind()),
			Location:     artifact.GetLocation(),
			LocationKind: nativeLocationKind(artifact.GetLocationKind()),
			Metadata:     cloneStringMap(artifact.GetMetadata()),
		})
	}
	return result
}

func extensionArtifacts(artifacts project.ArtifactCollection) []*azdext.Artifact {
	result := make([]*azdext.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		result = append(result, &azdext.Artifact{
			Kind:         extensionArtifactKind(artifact.Kind),
			Location:     artifact.Location,
			LocationKind: extensionLocationKind(artifact.LocationKind),
			Metadata:     cloneStringMap(artifact.Metadata),
		})
	}
	return result
}

func nativeArtifactKind(kind azdext.ArtifactKind) project.ArtifactKind {
	switch kind {
	case azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY:
		return project.ArtifactKindDirectory
	case azdext.ArtifactKind_ARTIFACT_KIND_CONFIG:
		return project.ArtifactKindConfig
	case azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE:
		return project.ArtifactKindArchive
	case azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER:
		return project.ArtifactKindContainer
	case azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT:
		return project.ArtifactKindEndpoint
	case azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT:
		return project.ArtifactKindDeployment
	case azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE:
		return project.ArtifactKindResource
	default:
		return project.ArtifactKind("")
	}
}

func extensionArtifactKind(kind project.ArtifactKind) azdext.ArtifactKind {
	switch kind {
	case project.ArtifactKindDirectory:
		return azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY
	case project.ArtifactKindConfig:
		return azdext.ArtifactKind_ARTIFACT_KIND_CONFIG
	case project.ArtifactKindArchive:
		return azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE
	case project.ArtifactKindContainer:
		return azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER
	case project.ArtifactKindEndpoint:
		return azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT
	case project.ArtifactKindDeployment:
		return azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT
	case project.ArtifactKindResource:
		return azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE
	default:
		return azdext.ArtifactKind_ARTIFACT_KIND_UNSPECIFIED
	}
}

func nativeLocationKind(kind azdext.LocationKind) project.LocationKind {
	switch kind {
	case azdext.LocationKind_LOCATION_KIND_LOCAL:
		return project.LocationKindLocal
	case azdext.LocationKind_LOCATION_KIND_REMOTE:
		return project.LocationKindRemote
	default:
		return project.LocationKind("")
	}
}

func extensionLocationKind(kind project.LocationKind) azdext.LocationKind {
	switch kind {
	case project.LocationKindLocal:
		return azdext.LocationKind_LOCATION_KIND_LOCAL
	case project.LocationKindRemote:
		return azdext.LocationKind_LOCATION_KIND_REMOTE
	default:
		return azdext.LocationKind_LOCATION_KIND_UNSPECIFIED
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	maps.Copy(result, values)
	return result
}

func runWithProgress[T any](
	ctx context.Context,
	report grpcbroker.ProgressFunc,
	run func(*async.Progress[project.ServiceProgress]) (T, error),
) (T, error) {
	progress := async.NewProgress[project.ServiceProgress]()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for update := range progress.Progress() {
			if report != nil {
				report(update.Message)
			}
		}
	}()

	result, err := run(progress)
	progress.Done()
	select {
	case <-done:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	return result, err
}
