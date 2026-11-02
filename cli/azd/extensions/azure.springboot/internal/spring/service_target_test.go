// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type packagingOutputStore struct {
	data   map[string]string
	writes []string
}

func (s *packagingOutputStore) values(context.Context) (string, map[string]string, error) {
	return "dev", s.data, nil
}

func (s *packagingOutputStore) set(_ context.Context, _, key, value string) error {
	s.data[key] = value
	s.writes = append(s.writes, key)
	return nil
}

type packagingModuleDeployer struct {
	request *moduleDeploymentRequest
	outputs map[string]string
}

func (d *packagingModuleDeployer) Deploy(
	_ context.Context, request *moduleDeploymentRequest, _ azdext.ProgressReporter,
) (*moduleDeploymentResult, error) {
	d.request = request
	return &moduleDeploymentResult{Outputs: d.outputs}, nil
}

type packagingResources struct {
	target *azdext.TargetResource
}

func (r *packagingResources) Resolve(
	context.Context, string, string, map[string]string, *azdext.ServiceConfig,
	func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	return r.target, nil
}

func (r *packagingResources) Endpoints(context.Context, *azdext.TargetResource) ([]string, error) {
	return []string{"https://actual.azurecontainerapps.io"}, nil
}

func (r *packagingResources) WaitReady(context.Context, *azdext.TargetResource) error {
	return nil
}

func TestServiceDeployPreservesModuleImageBindingAndEndpointArtifacts(t *testing.T) {
	for _, invalidEndpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "authored endpoint", true: "invalid endpoint"}[invalidEndpoint], func(t *testing.T) {
			project, service := moduleFixture(t, "application")
			delete(project.Services, service.Name)
			service.Name = "spring-petclinic-rest-app"
			project.Services[service.Name] = service
			store := &packagingOutputStore{data: map[string]string{}}
			endpoint := "https://actual.azurecontainerapps.io/petclinic"
			if invalidEndpoint {
				// #nosec G101 -- dummy credentials intentionally exercise endpoint redaction.
				endpoint = "https://user:secret@foreign.example/petclinic?sig=secret"
			}
			deployer := &packagingModuleDeployer{outputs: map[string]string{
				"SERVICE_SPRING_PETCLINIC_REST_APP_URI": endpoint,
			}}
			resource := &azdext.TargetResource{
				SubscriptionId: "sub", ResourceGroupName: "rg", ResourceName: "actual",
				ResourceType: "Microsoft.App/containerApps", Metadata: map[string]string{"resourceId": "app-id"},
			}
			target := newServiceTarget(packagingTestClient(t, project), &Runtime{environments: store},
				deployer, &packagingResources{target: resource})
			target.resolvedModules = map[string]*resolvedServiceModule{
				service.Name: fixtureResolvedModule(t, project, service, "application"),
			}
			result, err := target.Deploy(t.Context(), service, &azdext.ServiceContext{
				Publish: []*azdext.Artifact{{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
					LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
					Location:     "registry.example/petclinic:tag",
				}},
			}, resource, func(string) {})
			requireSameModuleFile(
				t, filepath.Join(project.Path, "infra", "application.bicep"), deployer.request.Module.BicepPath,
			)
			require.Equal(t, "registry.example/petclinic:tag",
				deployer.request.EnvironmentValue["SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME"])
			if invalidEndpoint {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
				require.Empty(t, store.writes)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.Artifacts, 2)
			require.Equal(t, azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT, result.Artifacts[1].Kind)
			require.Equal(t, endpoint, result.Artifacts[1].Location)
			endpoints, err := target.Endpoints(t.Context(), service, resource)
			require.NoError(t, err)
			require.Equal(t, []string{endpoint}, endpoints)
		})
	}
}

func TestAuthoredDockerTargetReusesCorePackageWithoutDuplicateArtifacts(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{"database": map[string]any{}}).AdditionalProperties
	service.Docker = &azdext.DockerProjectOptions{Path: "Dockerfile", Context: ".", Platform: "linux/amd64"}
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "Dockerfile"), []byte("FROM authored"), 0o600))
	client := packagingTestClient(t, project)
	target := newServiceTarget(client, nil, nil, nil)
	serviceContext := coreDockerPackageContext()
	before := proto.Clone(serviceContext)
	result, err := target.Package(t.Context(), service, serviceContext, nil)
	require.NoError(t, err)
	require.Empty(t, result.Artifacts)
	require.True(t, proto.Equal(before, serviceContext))
	require.Len(t, serviceContext.Package, 1)
	// No Container service is registered: a duplicate Build or Package RPC would fail this test.
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
	serviceContext.Package = nil
	_, err = target.Package(t.Context(), service, serviceContext, nil)
	require.ErrorContains(t, err, "requires a local core Docker")
}

func TestTargetInitializeRejectsInvalidModuleBeforeRuntimeAccess(t *testing.T) {
	project, service := createMavenProject(t)
	service.Language = "java"
	service.Docker = &azdext.DockerProjectOptions{Path: "Dockerfile", Context: ".", Platform: "linux/amd64"}
	project.Infra = &azdext.InfraOptions{Provider: "bicep", Path: "infra"}
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "Dockerfile"), []byte("FROM authored"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "azure.yaml"), []byte(`
services:
  petclinic:
    module: null
`), 0o600))
	target := newServiceTarget(packagingTestClient(t, project), nil, nil, nil)
	require.ErrorContains(t, target.Initialize(t.Context(), service), "module must be a nonempty")
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
}

func coreDockerPackageContext() *azdext.ServiceContext {
	return &azdext.ServiceContext{
		Build: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			Location:     "sha256:built",
			Metadata:     map[string]string{"framework": "docker", "imageId": "sha256:built"},
		}},
		Package: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			Location:     "petclinic:tag",
			Metadata:     map[string]string{"imageHash": "sha256:built", "targetImage": "petclinic:tag", "sourceImage": ""},
		}},
	}
}

func TestCoreContainerPackageValidation(t *testing.T) {
	serviceContext := coreDockerPackageContext()
	before := proto.Clone(serviceContext)
	require.NoError(t, validateCoreContainerPackage(serviceContext))
	require.True(t, proto.Equal(before, serviceContext))
	for _, invalid := range []*azdext.ServiceContext{
		nil,
		{},
		{Package: serviceContext.Package},
		{Build: serviceContext.Build, Package: append(serviceContext.Package, serviceContext.Package...)},
		{Build: serviceContext.Build, Package: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "foreign:tag",
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		}}},
	} {
		require.Error(t, validateCoreContainerPackage(invalid))
	}
	serviceContext.Package[0].Metadata["imageHash"] = "sha256:stale"
	require.ErrorContains(t, validateCoreContainerPackage(serviceContext), "does not match")
}

func TestServiceEndpointsPreserveAuthoredContextPath(t *testing.T) {
	origin := []string{"https://actual.azurecontainerapps.io"}
	key := "SERVICE_SPRING_PETCLINIC_REST_APP_URI"
	endpoint := origin[0] + "/petclinic"
	got, err := selectServiceEndpoints("spring-petclinic-rest-app", origin, map[string]string{key: endpoint})
	require.NoError(t, err)
	require.Equal(t, []string{endpoint}, got)
	got, err = selectServiceEndpoints("spring-petclinic-rest-app", origin, nil)
	require.NoError(t, err)
	require.Equal(t, origin, got)
	for _, invalid := range []string{
		"", "http://actual.azurecontainerapps.io/petclinic", "https://foreign.example/petclinic",
		"https://user:secret@actual.azurecontainerapps.io/petclinic",
		"https://actual.azurecontainerapps.io/petclinic?sig=secret",
		"https://actual.azurecontainerapps.io/petclinic#secret",
		"https://actual.azurecontainerapps.io:8443/petclinic", "https://%invalid/secret",
	} {
		_, err := selectServiceEndpoints("spring-petclinic-rest-app", origin, map[string]string{key: invalid})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		require.NotContains(t, err.Error(), "user:")
		require.NotContains(t, err.Error(), "sig=")
	}
	_, err = selectServiceEndpoints("spring-petclinic-rest-app", origin,
		map[string]string{key: endpoint, "SERVICE_SPRING_PETCLINIC_REST_APP_URL": endpoint + "/different"})
	require.ErrorContains(t, err, "conflict")
}

func TestRemoteContainerImageSelectsRemoteContainerArtifact(t *testing.T) {
	image, err := remoteContainerImage([]*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "maven-package",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		},
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "registry.example/petclinic:tag",
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"remoteImage": "registry.example/petclinic:tag"},
		},
	})

	require.NoError(t, err)
	require.Equal(t, "registry.example/petclinic:tag", image)
}

func TestPublishedContainerImageDoesNotHideInvalidArtifacts(t *testing.T) {
	_, err := publishedContainerImage(
		&azdext.ServiceContext{Publish: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "not-an-image",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		}}},
		map[string]string{
			serviceImageEnvironmentKey("petclinic"): "registry.example/stale:tag",
		},
		"petclinic",
	)

	require.ErrorContains(t, err, "no remote image")
}

func TestPersistDeploymentOutputsUsesCanonicalEndpointName(t *testing.T) {
	outputs, err := deploymentOutputs(
		azure.ArmTemplateOutputs{
			"SERVICE_PETCLINIC_URI": {Type: "String"},
		},
		map[string]any{
			"servicE_PETCLINIC_URI": map[string]any{
				"type":  "String",
				"value": "https://petclinic.example",
			},
		},
	)
	require.NoError(t, err)

	store := &packagingOutputStore{data: map[string]string{}}
	require.NoError(t, persistDeploymentOutputs(t.Context(), store, "dev", outputs))

	require.Equal(t, "https://petclinic.example", store.data["SERVICE_PETCLINIC_URI"])
	require.NotContains(t, store.data, "servicE_PETCLINIC_URI")
	require.Equal(t, []string{"SERVICE_PETCLINIC_URI"}, store.writes)
}
