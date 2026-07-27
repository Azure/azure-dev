// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func Test_ServiceResults_Json_Marshal(t *testing.T) {
	deployResult := &ServiceDeployResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDeployment,
				Location:     "https://myapp.azurewebsites.net",
				LocationKind: LocationKindRemote,
				Metadata:     nil,
			},
		},
	}

	jsonBytes, err := json.Marshal(deployResult)
	require.NoError(t, err)
	require.NotEmpty(t, string(jsonBytes))
}

func TestArtifactCollection(t *testing.T) {
	// Create a new service context
	ctx := NewServiceContext()

	// Add some artifacts using the available Add method
	err := ctx.Build.Add(&Artifact{
		Kind:         ArtifactKindDirectory,
		Location:     "/path/to/app.exe",
		LocationKind: LocationKindLocal,
		Metadata:     nil,
	})
	require.NoError(t, err)

	err = ctx.Package.Add(&Artifact{
		Kind:         ArtifactKindArchive,
		Location:     "/path/to/package.zip",
		LocationKind: LocationKindLocal,
		Metadata:     nil,
	})
	require.NoError(t, err)

	err = ctx.Package.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "registry.io/myapp:latest",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{"digest": "sha256:abc123"},
	})
	require.NoError(t, err)

	// Test finding artifacts using available Find method
	buildArtifacts := ctx.Build.Find()
	require.Len(t, buildArtifacts, 1, "Expected 1 build artifact")
	require.Equal(t, "/path/to/app.exe", buildArtifacts[0].Location)

	// Test package artifacts
	packageArtifacts := ctx.Package.Find()
	require.Len(t, packageArtifacts, 2, "Expected 2 package artifacts")

	// Test that deploy collection is empty
	deployArtifacts := ctx.Deploy.Find()
	require.Len(t, deployArtifacts, 0, "Expected deploy to be empty")
}

func TestArtifactKindEnums(t *testing.T) {
	// Test that all well-known kinds are strings
	kinds := []ArtifactKind{
		ArtifactKindDirectory,
		ArtifactKindArchive,
		ArtifactKindContainer,
		ArtifactKindDeployment,
		ArtifactKindConfig,
		ArtifactKindEndpoint,
		ArtifactKindResource,
	}

	for _, kind := range kinds {
		require.NotEmpty(t, string(kind), "ArtifactKind should not be empty string")
	}

	// Test that string conversion works
	require.Equal(t, "container", string(ArtifactKindContainer))
}

func Test_containerAppTarget_Package(t *testing.T) {
	at := &containerAppTarget{}
	progress := async.NewProgress[ServiceProgress]()
	go func() {
		for range progress.Progress() {
		}
	}()

	result, err := at.Package(t.Context(), nil, nil, progress)
	progress.Done()

	require.NoError(t, err)
	require.NotNil(t, result)
	// containerAppTarget.Package returns empty result
	assert.Empty(t, result.Artifacts)
}

// fakeServiceTargetStub implements ServiceTarget with configurable results.
type fakeServiceTargetStub struct {
	packageResult *ServicePackageResult
	packageErr    error
	publishResult *ServicePublishResult
	publishErr    error
	deployResult  *ServiceDeployResult
	deployErr     error
	endpoints     []string
	endpointsErr  error
}

func (f *fakeServiceTargetStub) Package(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return f.packageResult, f.packageErr
}

func (f *fakeServiceTargetStub) Publish(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *environment.TargetResource,
	_ *async.Progress[ServiceProgress], _ *PublishOptions,
) (*ServicePublishResult, error) {
	return f.publishResult, f.publishErr
}

func (f *fakeServiceTargetStub) Deploy(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *environment.TargetResource,
	_ *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return f.deployResult, f.deployErr
}

// helper to create a progress channel and drain it to avoid blocking.
func newDrainedProgress() *async.Progress[ServiceProgress] {
	p := async.NewProgress[ServiceProgress]()
	go func() {
		for range p.Progress() {
		}
	}()
	return p
}

func Test_ServiceManager_Deploy_HappyPath(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployResult:  &ServiceDeployResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Deploy_WithOverriddenEndpoints(t *testing.T) {
	// Set SERVICE_WEB_ENDPOINTS in dotenv
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_ENDPOINTS": `["http://example.com","http://other.com"]`,
	})

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployResult:  &ServiceDeployResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Overridden endpoints should be added as artifacts
	assert.GreaterOrEqual(t, len(result.Artifacts), 2)
}

func Test_ServiceManager_Deploy_TargetError(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployErr:     errors.New("deploy-failed"),
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	_, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed deploying service")
}

func Test_ServiceManager_Publish_HappyPath(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Publish(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}
