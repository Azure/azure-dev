// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestContainerHelperRemoteBuildFallback(t *testing.T) {
	tests := []struct {
		name          string
		runtime       string
		packageImage  string
		metadataImage bool
		emptyPackage  bool
		imageOverride string
		failure       string
		cancelAt      string
		wantError     string
		wantOps       []string
	}{
		{
			name:    "BuildFromSource",
			wantOps: []string{"schedule", "--version", "ps", "build", "package", "tag", "login", "push"},
		},
		{
			name: "PodmanFromSource", runtime: "podman",
			wantOps: []string{"schedule", "--version", "ps", "build", "package", "tag", "login", "push"},
		},
		{
			name: "ImageOverride", imageOverride: "custom/repository:release",
			wantOps: []string{"schedule", "--version", "ps", "build", "package", "tag", "login", "push"},
		},
		{
			name: "SuppliedPackage", packageImage: "existing/image:tested",
			wantOps: []string{"schedule", "--version", "ps", "tag", "login", "push"},
		},
		{
			name: "LegacyPackageMetadata", packageImage: "existing/image:tested", metadataImage: true,
			wantOps: []string{"schedule", "--version", "ps", "tag", "login", "push"},
		},
		{
			name: "MalformedPackage", emptyPackage: true,
			wantError: "failed retrieving package result details",
			wantOps:   []string{"schedule", "--version", "ps"},
		},
		{
			name: "RuntimeUnavailable", failure: "--version", wantError: "local container runtime unavailable",
			wantOps: []string{"schedule", "--version"},
		},
		{
			name: "DaemonUnavailable", failure: "ps", wantError: "local container runtime unavailable",
			wantOps: []string{"schedule", "--version", "ps"},
		},
		{
			name: "BuildFailure", failure: "build", wantError: "building local image",
			wantOps: []string{"schedule", "--version", "ps", "build"},
		},
		{
			name: "PackageFailure", failure: "package", wantError: "packaging local image",
			wantOps: []string{"schedule", "--version", "ps", "build", "package"},
		},
		{
			name: "LoginFailure", failure: "login", wantError: "Local fallback failed",
			wantOps: []string{"schedule", "--version", "ps", "build", "package", "tag", "login"},
		},
		{
			name: "PushFailure", failure: "push", wantError: "Local fallback failed",
			wantOps: []string{"schedule", "--version", "ps", "build", "package", "tag", "login", "push"},
		},
		{
			name: "CanceledDuringScheduling", cancelAt: "schedule", wantError: "context canceled",
			wantOps: []string{"schedule"},
		},
		{
			name: "CanceledAfterReadiness", cancelAt: "ps", wantError: "Local fallback failed",
			wantOps: []string{"schedule", "--version", "ps"},
		},
		{
			name: "CanceledAfterBuild", cancelAt: "build", wantError: "Local fallback failed",
			wantOps: []string{"schedule", "--version", "ps", "build"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := tt.runtime
			if runtime == "" {
				runtime = "docker"
			}
			t.Setenv("AZD_CONTAINER_RUNTIME", runtime)
			t.Setenv("NO_COLOR", "1")
			f := newRemoteBuildFixture(t)
			f.scheduleCode = "TasksOperationsNotAllowed"
			f.failure = tt.failure
			if tt.failure == "login" {
				f.loginCall.Return(f.localError)
			}
			f.cancelAt = tt.cancelAt
			ctx, cancel := context.WithCancel(*f.mocks.Context)
			defer cancel()
			f.cancel = cancel
			f.options.Image = tt.imageOverride

			progress := async.NewNoopProgress[ServiceProgress]()
			defer progress.Done()
			build, err := f.helper.Build(ctx, f.config, f.serviceContext, f.env, progress)
			require.NoError(t, err)
			require.Empty(t, build.Artifacts)
			require.NoError(t, f.serviceContext.Build.Add(build.Artifacts...))
			pkg, err := f.helper.Package(ctx, f.config, f.serviceContext, f.env, progress)
			require.NoError(t, err)
			require.Empty(t, pkg.Artifacts)
			require.NoError(t, f.serviceContext.Package.Add(pkg.Artifacts...))
			require.Empty(t, f.operations)

			if tt.packageImage != "" || tt.emptyPackage {
				artifact := &Artifact{
					Kind: ArtifactKindContainer, LocationKind: LocationKindLocal, Location: tt.packageImage,
				}
				if tt.metadataImage {
					artifact.Location = ""
					artifact.Metadata = map[string]string{"targetImage": tt.packageImage}
				}
				f.serviceContext.Package = append(f.serviceContext.Package, artifact)
			} else {
				require.NoError(t, f.serviceContext.Package.Add(&Artifact{
					Kind: ArtifactKindConfig, LocationKind: LocationKindLocal, Location: "config.json",
				}))
			}
			before, err := json.Marshal(f.serviceContext)
			require.NoError(t, err)
			dockerConfig := f.config.Docker

			result, err := f.publish(ctx)
			require.Equal(t, tt.wantOps, f.operations)
			after, marshalErr := json.Marshal(f.serviceContext)
			require.NoError(t, marshalErr)
			require.JSONEq(t, string(before), string(after))
			require.Equal(t, dockerConfig, f.config.Docker)

			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, result)
				if tt.failure == "--version" || tt.failure == "ps" || tt.cancelAt == "schedule" || tt.cancelAt == "ps" {
					require.Empty(t, f.mocks.Console.Output())
				}
				if tt.cancelAt == "schedule" {
					require.ErrorIs(t, err, context.Canceled)
					return
				}
				responseErr, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				require.Equal(t, "TasksOperationsNotAllowed", responseErr.ErrorCode)
				if tt.failure != "" {
					require.ErrorIs(t, err, f.localError)
				}
				if tt.failure == "push" {
					suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
					require.True(t, ok)
					display := &ux.ErrorWithSuggestion{
						Err: suggestion.Err, Message: suggestion.Message,
						Suggestion: suggestion.Suggestion, Links: suggestion.Links,
					}
					rendered := display.ToString("")
					require.Contains(t, rendered, "TasksOperationsNotAllowed")
					require.Contains(t, rendered, f.localError.Error())
					require.Contains(t, rendered, "docker login")
				}
				if tt.cancelAt != "" {
					require.ErrorIs(t, err, context.Canceled)
				}
				return
			}
			require.NoError(t, err)
			expectedImage := "contoso.azurecr.io/project/app-dev:azd-deploy-0"
			if tt.packageImage != "" {
				expectedImage = "contoso.azurecr.io/" + tt.packageImage
			}
			if tt.imageOverride != "" {
				expectedImage = "contoso.azurecr.io/" + tt.imageOverride
			}
			require.Equal(t, ArtifactCollection{{
				Kind: ArtifactKindContainer, LocationKind: LocationKindRemote, Location: expectedImage,
				Metadata: map[string]string{"remoteImage": expectedImage},
			}}, result.Artifacts)
			require.Equal(t, expectedImage, f.pushedImage)
			require.Len(t, f.mocks.Console.Output(), 1)
			warning := f.mocks.Console.Output()[0]
			require.Contains(t, warning, "TasksOperationsNotAllowed")
			if tt.packageImage == "" {
				require.Contains(t, warning, "Building locally with "+f.helper.docker.Name())
			} else {
				require.Contains(t, warning, "Publishing the existing local image")
			}
		})
	}
}

func TestContainerHelperRemoteBuildNoFallback(t *testing.T) {
	tests := []struct {
		name         string
		status       armcontainerregistry.RunStatus
		scheduleCode string
		logError     bool
		platform     string
		contextError error
	}{
		{name: "Success", status: armcontainerregistry.RunStatusSucceeded},
		{name: "Failed", status: armcontainerregistry.RunStatusFailed},
		{name: "Error", status: armcontainerregistry.RunStatusError},
		{name: "Timeout", status: armcontainerregistry.RunStatusTimeout},
		{name: "Canceled", status: armcontainerregistry.RunStatusCanceled},
		{name: "BadRequest", scheduleCode: "BadRequest"},
		{name: "AuthorizationFailed", scheduleCode: "AuthorizationFailed"},
		{name: "UnknownOutcome", logError: true},
		{name: "UnsupportedPlatform", platform: "linux/arm64"},
		{name: "ContextCanceled", contextError: context.Canceled},
		{name: "DeadlineExceeded", contextError: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newRemoteBuildFixture(t)
			f.runStatus = tt.status
			f.scheduleCode = tt.scheduleCode
			f.logError = tt.logError
			f.config.Docker.Platform = tt.platform
			ctx := *f.mocks.Context
			if errors.Is(tt.contextError, context.Canceled) {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			} else if errors.Is(tt.contextError, context.DeadlineExceeded) {
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = expired
			}

			result, err := f.publish(ctx)
			if tt.status == armcontainerregistry.RunStatusSucceeded {
				require.NoError(t, err)
				require.Len(t, result.Artifacts, 1)
			} else {
				require.Error(t, err)
				require.Nil(t, result)
				if tt.contextError != nil {
					require.ErrorIs(t, err, tt.contextError)
				}
				if tt.status != "" {
					runErr, ok := errors.AsType[*containerregistry.RemoteBuildRunError](err)
					require.True(t, ok)
					require.Equal(t, tt.status, runErr.Status)
					require.ErrorContains(t, err, "build log")
				}
				_, eligible := errors.AsType[*containerregistry.RemoteBuildUnavailableError](err)
				require.False(t, eligible)
			}
			if tt.platform != "" || tt.contextError != nil {
				require.Empty(t, f.operations)
			} else {
				require.Equal(t, []string{"schedule"}, f.operations)
			}
			require.Empty(t, f.mocks.Console.Output())
		})
	}
}

func TestContainerHelperRemoteBuildParallelFallback(t *testing.T) {
	t.Setenv("AZD_CONTAINER_RUNTIME", "docker")
	fixtures := []*remoteBuildFixture{newRemoteBuildFixture(t), newRemoteBuildFixture(t)}
	var wg sync.WaitGroup
	results := make([]*ServicePublishResult, len(fixtures))
	errs := make([]error, len(fixtures))
	for i, f := range fixtures {
		f.scheduleCode = "TasksOperationsNotAllowed"
		f.helper.docker = fixtures[0].helper.docker
		wg.Go(func() {
			results[i], errs[i] = f.publish(*f.mocks.Context)
		})
	}
	wg.Wait()
	for i := range fixtures {
		require.NoError(t, errs[i])
		require.Len(t, results[i].Artifacts, 1)
		require.Empty(t, fixtures[i].serviceContext.Build)
		require.Empty(t, fixtures[i].serviceContext.Package)
	}
}

type remoteBuildFixture struct {
	mocks          *mocks.MockContext
	helper         *ContainerHelper
	config         *ServiceConfig
	serviceContext *ServiceContext
	env            *environment.Environment
	target         *environment.TargetResource
	options        *PublishOptions
	scheduleCode   string
	runStatus      armcontainerregistry.RunStatus
	logError       bool
	failure        string
	cancelAt       string
	cancel         context.CancelFunc
	localError     error
	loginCall      *mock.Call
	mu             sync.Mutex
	operations     []string
	pushedImage    string
}

func (f *remoteBuildFixture) record(operation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.operations = append(f.operations, operation)
	if f.cancelAt == operation {
		f.cancel()
	}
	if f.failure == operation {
		return f.localError
	}
	return nil
}

func (f *remoteBuildFixture) publish(ctx context.Context) (*ServicePublishResult, error) {
	progress := async.NewNoopProgress[ServiceProgress]()
	defer progress.Done()
	return f.helper.Publish(ctx, f.config, f.serviceContext, f.target, f.env, progress, f.options)
}

func newRemoteBuildFixture(t *testing.T) *remoteBuildFixture {
	t.Helper()
	m := mocks.NewMockContext(t.Context())
	m.ArmClientOptions.Retry.MaxRetries = -1
	f := &remoteBuildFixture{
		mocks: m, config: createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript),
		serviceContext: NewServiceContext(), env: environment.NewWithValues("dev", map[string]string{}),
		target:  environment.NewTargetResource("SUBSCRIPTION_ID", "RESOURCE_GROUP", "app", "Microsoft.App/containerApps"),
		options: &PublishOptions{}, runStatus: armcontainerregistry.RunStatusSucceeded,
		localError: errors.New("local operation failed"),
	}
	f.config.Project.Path = t.TempDir()
	f.config.Project.Name = "project"
	f.config.Name = "app"
	f.config.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
	f.config.Docker.RemoteBuild = true
	require.NoError(t, os.MkdirAll(f.config.Path(), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(f.config.Path(), "Dockerfile"), []byte("FROM scratch"), 0600))
	m.CommandRunner.MockToolInPath("docker", nil)
	m.CommandRunner.MockToolInPath("podman", nil)
	m.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "docker" || args.Cmd == "podman"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		operation := args.Args[0]
		if operation == "tag" && args.Args[1] == "IMAGE_ID" {
			operation = "package"
		}
		if err := f.record(operation); err != nil {
			return exec.RunResult{}, err
		}
		switch operation {
		case "--version":
			version := "Docker version 20.10.17, build 100c701"
			if args.Cmd == "podman" {
				version = "podman version 4.9.0"
			}
			return exec.NewRunResult(0, version, ""), nil
		case "build":
			index := slices.Index(args.Args, "--iidfile")
			require.GreaterOrEqual(t, index, 0)
			return exec.RunResult{}, os.WriteFile(args.Args[index+1], []byte("IMAGE_ID"), 0600)
		case "push":
			f.mu.Lock()
			f.pushedImage = args.Args[1]
			f.mu.Unlock()
		case "ps", "package", "tag":
		default:
			return exec.RunResult{}, errors.New("unexpected container operation: " + operation)
		}
		return exec.RunResult{}, nil
	})
	registry := &mockContainerRegistryService{}
	registry.On("FindContainerRegistryResourceGroup", mock.Anything, "SUBSCRIPTION_ID", "contoso").
		Return("REGISTRY_RG", nil)
	f.loginCall = registry.On("Login", mock.Anything, mock.Anything, "contoso.azurecr.io").
		Return(nil).Run(func(mock.Arguments) {
		_ = f.record("login")
	})
	f.helper = NewContainerHelper(
		clock.NewMock(), registry,
		containerregistry.NewRemoteBuildManager(m.SubscriptionCredentialProvider, m.ArmClientOptions),
		m.CommandRunner, docker.NewCli(m.CommandRunner), dotnet.NewCli(m.CommandRunner), m.Console, cloud.AzurePublic(),
	)
	m.HttpClient.When(func(*http.Request) bool { return true }).RespondFn(f.respond)
	return f
}

func (f *remoteBuildFixture) respond(request *http.Request) (*http.Response, error) {
	const buildLog = "build log\n"
	errorResponse := func(code string) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusForbidden, map[string]any{
			"error": map[string]string{"code": code, "message": "ACR request refused"},
		})
	}
	switch {
	case strings.Contains(request.URL.Path, "listBuildSourceUploadUrl"):
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armcontainerregistry.SourceUploadDefinition{
			UploadURL: new("https://upload.example.com/source.tar.gz"), RelativePath: new("source.tar.gz"),
		})
	case strings.HasSuffix(request.URL.Path, "/source.tar.gz"):
		_, err := io.Copy(io.Discard, request.Body)
		if err != nil {
			return nil, err
		}
		response, err := mocks.CreateEmptyHttpResponse(request, http.StatusCreated)
		if err == nil {
			response.Header.Set("ETag", `"etag"`)
		}
		return response, err
	case strings.Contains(request.URL.Path, "scheduleRun"):
		if err := f.record("schedule"); err != nil {
			return nil, err
		}
		if f.scheduleCode != "" {
			return errorResponse(f.scheduleCode)
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armcontainerregistry.Run{
			Properties: &armcontainerregistry.RunProperties{RunID: new("run-id")},
		})
	case strings.Contains(request.URL.Path, "listLogSasUrl"):
		if f.logError {
			return errorResponse("TasksOperationsNotAllowed")
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armcontainerregistry.RunGetLogResult{
			LogLink: new("https://logs.example.com/run.log"),
		})
	case strings.HasSuffix(request.URL.Path, "/run.log"):
		response, err := mocks.CreateEmptyHttpResponse(request, http.StatusOK)
		if err != nil {
			return nil, err
		}
		response.Header.Set("Content-Length", strconv.Itoa(len(buildLog)))
		response.Header.Set("x-ms-meta-complete", "true")
		if request.Method == http.MethodGet {
			response.StatusCode = http.StatusPartialContent
			response.Body = io.NopCloser(strings.NewReader(buildLog))
		}
		return response, nil
	case strings.Contains(request.URL.Path, "/runs/run-id"):
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armcontainerregistry.Run{
			Properties: &armcontainerregistry.RunProperties{Status: new(f.runStatus)},
		})
	default:
		return nil, errors.New("unexpected remote build request: " + request.URL.Path)
	}
}
